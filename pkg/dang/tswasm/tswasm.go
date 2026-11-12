//go:build !js

// Package tswasm runs Dang's tree-sitter grammar without cgo: the C
// tree-sitter runtime and the grammar, compiled to WebAssembly (dang.wasm,
// see build.sh and csrc/shim.c), run by wazero. Its trees are the C runtime's,
// because it is the C runtime.
//
// The module is compiled once per process, on first use (tens of
// milliseconds). Instances are pooled: each parse takes one, so concurrent
// parses are safe, and an instance whose parse was cut short by its context
// is thrown away rather than reused.
package tswasm

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed dang.wasm
var module []byte

// Node is a tree-sitter syntax node.
type Node struct {
	// Kind is the node's type, as tree-sitter names it (ts_node_type):
	// "ERROR" for an error, the expected token for a missing one.
	Kind string

	Missing  bool // inserted by error recovery (ts_node_is_missing)
	Error    bool // an ERROR node (ts_node_is_error)
	HasError bool // an ERROR or missing node is at or beneath it
	Extra    bool
	Named    bool

	StartByte, EndByte    uint32
	StartRow, StartColumn uint32 // 0-based; columns count bytes
	EndRow, EndColumn     uint32
	Children              []*Node
}

// Parse parses source. With prune, a node with no errors beneath it comes
// back without its children, which is all error reporting needs.
//
// Parsing stops with ctx: past its deadline Parse returns ctx's error.
func Parse(ctx context.Context, source []byte, prune bool) (*Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e, err := loadEngine()
	if err != nil {
		return nil, err
	}
	inst, err := e.get()
	if err != nil {
		return nil, err
	}
	root, err := inst.parse(ctx, source, prune)
	if err != nil || ctx.Err() != nil {
		// wazero closes the instance when ctx ends, perhaps mid-parse, so
		// it can't go back in the pool.
		inst.mod.Close(context.Background())
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, err
		}
		return root, nil
	}
	e.put(inst)
	return root, nil
}

// engine is the compiled module and a pool of its instances.
type engine struct {
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	idle     chan *instance
}

// maxIdle bounds the instances kept for reuse; each holds its own memory.
const maxIdle = 4

// maxIdleMemory bounds the memory of an instance kept for reuse. WebAssembly
// memory only grows, so an instance that parsed a large source keeps that
// much until it is closed: rather than holding it for good, it is closed.
const maxIdleMemory = 16 << 20

var loadEngine = sync.OnceValues(func() (*engine, error) {
	ctx := context.Background()
	// Closing on context done is what lets a deadline stop a parse.
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return nil, fmt.Errorf("tswasm: instantiate WASI: %w", err)
	}
	compiled, err := rt.CompileModule(ctx, module)
	if err != nil {
		return nil, fmt.Errorf("tswasm: compile: %w", err)
	}
	return &engine{runtime: rt, compiled: compiled, idle: make(chan *instance, maxIdle)}, nil
})

func (e *engine) get() (*instance, error) {
	select {
	case inst := <-e.idle:
		return inst, nil
	default:
	}
	// An empty name lets any number of instances coexist.
	cfg := wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize")
	mod, err := e.runtime.InstantiateModule(context.Background(), e.compiled, cfg)
	if err != nil {
		return nil, fmt.Errorf("tswasm: instantiate: %w", err)
	}
	return &instance{
		mod:        mod,
		alloc:      mod.ExportedFunction("dang_alloc"),
		free:       mod.ExportedFunction("dang_free"),
		parseFn:    mod.ExportedFunction("dang_parse"),
		symbolName: mod.ExportedFunction("dang_symbol_name"),
	}, nil
}

func (e *engine) put(inst *instance) {
	if inst.mod.Memory().Size() > maxIdleMemory {
		inst.mod.Close(context.Background())
		return
	}
	select {
	case e.idle <- inst:
	default:
		inst.mod.Close(context.Background())
	}
}

// instance is one instantiation of the module, used by one parse at a time.
type instance struct {
	mod        api.Module
	alloc      api.Function
	free       api.Function
	parseFn    api.Function
	symbolName api.Function
}

// nodeWords is the size of csrc/shim.c's node struct, in uint32s.
const nodeWords = 9

const (
	flagMissing = 1 << iota
	flagError
	flagHasError
	flagExtra
	flagNamed
)

var errParse = errors.New("tswasm: parse failed")

func (inst *instance) parse(ctx context.Context, source []byte, prune bool) (*Node, error) {
	res, err := inst.alloc.Call(ctx, uint64(len(source)))
	if err != nil {
		return nil, err
	}
	src := uint32(res[0])
	if src == 0 {
		return nil, errParse
	}
	if !inst.mod.Memory().Write(src, source) {
		return nil, errParse
	}
	var pruneArg uint64
	if prune {
		pruneArg = 1
	}
	res, err = inst.parseFn.Call(ctx, uint64(src), uint64(len(source)), pruneArg)
	if err != nil {
		return nil, err
	}
	if _, err := inst.free.Call(ctx, uint64(src)); err != nil {
		return nil, err
	}
	if res[0] == 0 {
		return nil, errParse
	}
	header, ok := inst.mod.Memory().Read(uint32(res[0]), 8)
	if !ok {
		return nil, errParse
	}
	count := binary.LittleEndian.Uint32(header)
	data, ok := inst.mod.Memory().Read(binary.LittleEndian.Uint32(header[4:]), count*nodeWords*4)
	if !ok || count == 0 {
		return nil, errParse
	}
	d := decoder{inst: inst, ctx: ctx, data: data}
	root := d.node()
	if d.err != nil {
		return nil, d.err
	}
	return root, nil
}

// decoder rebuilds the tree from the shim's preorder node array.
type decoder struct {
	inst *instance
	ctx  context.Context
	data []byte
	next int
	err  error
}

func (d *decoder) word(i int) uint32 {
	return binary.LittleEndian.Uint32(d.data[(d.next*nodeWords+i)*4:])
}

func (d *decoder) node() *Node {
	if d.err != nil {
		return nil
	}
	if (d.next+1)*nodeWords*4 > len(d.data) {
		d.err = errParse
		return nil
	}
	flags := d.word(1)
	n := &Node{
		Missing:     flags&flagMissing != 0,
		Error:       flags&flagError != 0,
		HasError:    flags&flagHasError != 0,
		Extra:       flags&flagExtra != 0,
		Named:       flags&flagNamed != 0,
		StartByte:   d.word(2),
		EndByte:     d.word(3),
		StartRow:    d.word(4),
		StartColumn: d.word(5),
		EndRow:      d.word(6),
		EndColumn:   d.word(7),
	}
	n.Kind, d.err = d.inst.kind(d.ctx, d.word(0))
	children := int(d.word(8))
	d.next++
	if children > 0 {
		n.Children = make([]*Node, children)
		for i := range n.Children {
			n.Children[i] = d.node()
		}
	}
	return n
}

// symbolNames caches symbol names, which are the same in every instance.
var symbolNames sync.Map // uint32 → string

func (inst *instance) kind(ctx context.Context, symbol uint32) (string, error) {
	if name, ok := symbolNames.Load(symbol); ok {
		return name.(string), nil
	}
	res, err := inst.symbolName.Call(ctx, uint64(symbol))
	if err != nil {
		return "", err
	}
	name, err := inst.cString(uint32(res[0]))
	if err != nil {
		return "", err
	}
	symbolNames.Store(symbol, name)
	return name, nil
}

func (inst *instance) cString(ptr uint32) (string, error) {
	if ptr == 0 {
		return "", nil
	}
	mem := inst.mod.Memory()
	for n := uint32(0); ; n++ {
		b, ok := mem.ReadByte(ptr + n)
		if !ok {
			return "", errParse
		}
		if b == 0 {
			s, _ := mem.Read(ptr, n)
			return string(s), nil
		}
	}
}
