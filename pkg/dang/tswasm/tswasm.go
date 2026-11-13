//go:build !js

// Package tswasm runs Dang's tree-sitter grammar: the C tree-sitter runtime
// and the grammar, compiled to WebAssembly (dang.wasm, see build.sh and
// csrc/shim.c), run by wazero, so Dang needs no cgo. Its trees are the C
// runtime's, because it is the C runtime.
//
// The module is compiled once per process, on first use, and the compiled
// code is cached on disk (see compilationCacheDir), so later processes skip
// most of that. Instances are pooled: each parse or query takes one, so
// concurrent calls are safe. A Document keeps one of its own, to reparse a
// changing source incrementally.
//
// A call stops when its context is done: parsing and queries poll it.
package tswasm

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed dang.wasm
var module []byte

// Parse parses source and returns its tree, pruned as prune says.
func Parse(ctx context.Context, source []byte, prune Prune) (*Node, error) {
	var root *Node
	err := withInstance(ctx, func(inst *instance) error {
		if err := inst.parse(ctx, source, false); err != nil {
			return err
		}
		var err error
		root, err = inst.nodes(ctx, prune)
		return err
	})
	return root, err
}

// Inspect parses source and calls f with its tree, whose nodes' children
// are read from the parser as f asks for them (Node.Children): a caller
// that looks at only part of a tree pays only for that part. The tree is
// f's alone, and only until f returns; then its unread children read as
// none. Inspect returns f's error, else any error reading the tree.
func Inspect(ctx context.Context, source []byte, f func(root *Node) error) error {
	return withInstance(ctx, func(inst *instance) error {
		if err := inst.parse(ctx, source, false); err != nil {
			return err
		}
		return inst.inspect(ctx, f)
	})
}

// withInstance runs f with an instance from the pool.
func withInstance(ctx context.Context, f func(*instance) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e, err := loadEngine()
	if err != nil {
		return err
	}
	inst, err := e.get()
	if err != nil {
		return err
	}
	if err := f(inst); err != nil {
		// A trap leaves the instance in no state to reuse; an interrupted
		// call leaves it fine, but it's rare enough not to matter.
		inst.close()
		return err
	}
	e.put(inst)
	return nil
}

// engine is the compiled module and a pool of its instances.
type engine struct {
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	idle     chan *instance

	// symbols and fields name the grammar's symbols and fields, by id.
	symbols, fields []string
}

// maxIdle bounds the instances kept for reuse; each holds its own memory.
const maxIdle = 4

// maxIdleMemory bounds the memory of an instance kept for reuse. WebAssembly
// memory only grows, so an instance that parsed a large source keeps that
// much until it is closed: rather than holding it for good, it is closed.
const maxIdleMemory = 16 << 20

// compilationCacheDir is where the compiled module is cached across
// processes, or "" where there is nowhere.
func compilationCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "dang", "wazero")
}

var loadEngine = sync.OnceValues(func() (*engine, error) {
	return newEngine(compilationCacheDir())
})

// Load compiles the module now rather than on first use, for a long-lived
// caller that would rather not make its first parse wait for that. Calls
// after the first, and parses already waiting on it, share its result.
func Load() error {
	_, err := loadEngine()
	return err
}

// newEngine compiles the module, caching the compiled code in cacheDir
// unless it's "".
func newEngine(cacheDir string) (*engine, error) {
	ctx := context.Background()
	var rt wazero.Runtime
	var compiled wazero.CompiledModule
	var err error
	if cacheDir != "" {
		if cache, cacheErr := wazero.NewCompilationCacheWithDir(cacheDir); cacheErr == nil {
			rt, compiled, err = compile(ctx, wazero.NewRuntimeConfig().WithCompilationCache(cache))
			if err != nil {
				// A corrupt entry would fail every process after this one
				// too; clear the way for the next to cache afresh.
				os.RemoveAll(cacheDir) //nolint:errcheck
			}
		}
	}
	if compiled == nil {
		// No cache, or one that failed (unwritable, or a corrupt entry): the
		// cache only saves time, so do without.
		rt, compiled, err = compile(ctx, wazero.NewRuntimeConfig())
	}
	if err != nil {
		return nil, err
	}
	e := &engine{runtime: rt, compiled: compiled, idle: make(chan *instance, maxIdle)}
	inst, err := e.instantiate()
	if err != nil {
		return nil, err
	}
	if e.symbols, err = inst.names(ctx, "dang_symbol_count", "dang_symbol_name", 0); err != nil {
		return nil, err
	}
	// Field ids count from 1; 0 is none.
	if e.fields, err = inst.names(ctx, "dang_field_count", "dang_field_name", 1); err != nil {
		return nil, err
	}
	e.put(inst)
	return e, nil
}

// compile sets up a runtime with cfg and compiles the module in it, closing
// the runtime if that fails.
func compile(ctx context.Context, cfg wazero.RuntimeConfig) (_ wazero.Runtime, _ wazero.CompiledModule, err error) {
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	defer func() {
		if err != nil {
			rt.Close(ctx)
		}
	}()
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return nil, nil, fmt.Errorf("tswasm: instantiate WASI: %w", err)
	}
	// dang_interrupted is how a parse or query learns that its context is
	// done. (wazero's own WithCloseOnContextDone would do, but it makes
	// every call several times slower.)
	_, err = rt.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context) uint32 {
			if ctx.Err() != nil {
				return 1
			}
			return 0
		}).
		Export("dang_interrupted").
		Instantiate(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("tswasm: instantiate host module: %w", err)
	}
	compiled, err := rt.CompileModule(ctx, module)
	if err != nil {
		return nil, nil, fmt.Errorf("tswasm: compile: %w", err)
	}
	return rt, compiled, nil
}

func (e *engine) get() (*instance, error) {
	select {
	case inst := <-e.idle:
		return inst, nil
	default:
	}
	return e.instantiate()
}

func (e *engine) instantiate() (*instance, error) {
	// An empty name lets any number of instances coexist.
	cfg := wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize")
	mod, err := e.runtime.InstantiateModule(context.Background(), e.compiled, cfg)
	if err != nil {
		return nil, fmt.Errorf("tswasm: instantiate: %w", err)
	}
	fn := mod.ExportedFunction
	return &instance{
		engine:            e,
		mod:               mod,
		alloc:             fn("dang_alloc"),
		free:              fn("dang_free"),
		edit:              fn("dang_edit"),
		parseFn:           fn("dang_parse"),
		nodesFn:           fn("dang_nodes"),
		rootFn:            fn("dang_root"),
		childrenFn:        fn("dang_children"),
		queryNew:          fn("dang_query_new"),
		queryCaptureCount: fn("dang_query_capture_count"),
		queryPatternCount: fn("dang_query_pattern_count"),
		queryCaptureName:  fn("dang_query_capture_name"),
		queryStringValue:  fn("dang_query_string_value"),
		queryPredicates:   fn("dang_query_predicates"),
		capturesExec:      fn("dang_captures_exec"),
		capturesNext:      fn("dang_captures_next"),
		capturesRemove:    fn("dang_captures_remove"),
		matchesNext:       fn("dang_matches_next"),
		resultFn:          fn("dang_result"),
		queries:           map[*Query]uint32{},
	}, nil
}

func (e *engine) put(inst *instance) {
	if inst.mod.Memory().Size() > maxIdleMemory {
		inst.close()
		return
	}
	select {
	case e.idle <- inst:
	default:
		inst.close()
	}
}

// instance is one instantiation of the module, used by one call at a time.
type instance struct {
	engine *engine
	mod    api.Module

	alloc, free                          api.Function
	edit, parseFn                        api.Function
	nodesFn, rootFn, childrenFn          api.Function
	queryNew                             api.Function
	queryCaptureCount, queryPatternCount api.Function
	queryCaptureName, queryStringValue   api.Function
	queryPredicates                      api.Function
	capturesExec, capturesNext           api.Function
	capturesRemove, matchesNext          api.Function
	resultFn                             api.Function

	// resultPtr is the address of the shim's result words, once read.
	resultPtr uint32

	// queries holds the queries compiled in this instance.
	queries map[*Query]uint32
}

func (inst *instance) close() {
	inst.mod.Close(context.Background())
}

var errFailed = errors.New("tswasm: call failed")

// call calls f, returning its first result. Past ctx, it returns ctx's
// error: a call stopped short returns nothing to trust.
func call(ctx context.Context, f api.Function, args ...uint64) (uint32, error) {
	res, err := f.Call(ctx, args...)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, nil
	}
	return uint32(res[0]), nil
}

// write copies data into a fresh allocation; the caller frees it.
func (inst *instance) write(ctx context.Context, data []byte) (uint32, error) {
	ptr, err := call(ctx, inst.alloc, uint64(len(data)))
	if err != nil {
		return 0, err
	}
	if ptr == 0 || !inst.mod.Memory().Write(ptr, data) {
		return 0, errFailed
	}
	return ptr, nil
}

// words reads n uint32s at ptr.
func (inst *instance) words(ptr, n uint32) ([]byte, error) {
	data, ok := inst.mod.Memory().Read(ptr, n*4)
	if !ok {
		return nil, errFailed
	}
	return data, nil
}

func word(data []byte, i int) uint32 {
	return binary.LittleEndian.Uint32(data[i*4:])
}

// result reads the shim's result words.
func (inst *instance) result(ctx context.Context) ([2]uint32, error) {
	if inst.resultPtr == 0 {
		ptr, err := call(ctx, inst.resultFn)
		if err != nil {
			return [2]uint32{}, err
		}
		inst.resultPtr = ptr
	}
	data, err := inst.words(inst.resultPtr, 2)
	if err != nil {
		return [2]uint32{}, err
	}
	return [2]uint32{word(data, 0), word(data, 1)}, nil
}

// names reads the names a count function and a name function give, of ids
// from 0 to the count plus extra.
func (inst *instance) names(ctx context.Context, countFn, nameFn string, extra uint32) ([]string, error) {
	count, err := call(ctx, inst.mod.ExportedFunction(countFn))
	if err != nil {
		return nil, err
	}
	f := inst.mod.ExportedFunction(nameFn)
	names := make([]string, count+extra)
	for i := range names {
		ptr, err := call(ctx, f, uint64(i))
		if err != nil {
			return nil, err
		}
		if names[i], err = inst.cString(ptr); err != nil {
			return nil, err
		}
	}
	return names, nil
}

func (inst *instance) cString(ptr uint32) (string, error) {
	if ptr == 0 {
		return "", nil
	}
	mem := inst.mod.Memory()
	for n := uint32(0); ; n++ {
		b, ok := mem.ReadByte(ptr + n)
		if !ok {
			return "", errFailed
		}
		if b == 0 {
			s, _ := mem.Read(ptr, n)
			return string(s), nil
		}
	}
}

// parse parses source into the instance's kept tree, reusing the last one
// (edited to match) if reuse is set.
func (inst *instance) parse(ctx context.Context, source []byte, reuse bool) error {
	src, err := inst.write(ctx, source)
	if err != nil {
		return err
	}
	var reuseArg uint64
	if reuse {
		reuseArg = 1
	}
	ok, err := call(ctx, inst.parseFn, uint64(src), uint64(len(source)), reuseArg)
	if _, freeErr := inst.free.Call(ctx, uint64(src)); err == nil {
		err = freeErr
	}
	if err != nil {
		return err
	}
	if ok == 0 {
		return errFailed
	}
	return nil
}
