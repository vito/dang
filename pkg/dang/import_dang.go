package dang

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vito/dang/v2/pkg/hm"
)

// dangModule is a compiled native Dang module: its inferred public type scope
// plus the parsed file blocks needed to evaluate its code. Under the per-module
// identity model, one is produced per importing module per build: all files of a
// single module share one *dangModule per imported directory (keyed by resolved
// absolute dir in that module's ctx-scoped Dang-module cache), so its types and
// homeModule identity are stable within the module. But two DIFFERENT modules
// importing the same directory each get their own *dangModule — foreign types
// deliberately do not unify across the module boundary; behavior is shared via
// interfaces instead.
//
// The module is frozen once compiled: its top-level code runs at most once per
// instance (on first evaluation), exactly like the prelude. A directory imported
// by N distinct modules therefore compiles and runs N times, once per instance.
type dangModule struct {
	dir       string
	typeScope TypeScope
	blocks    []*FileBlock

	once  sync.Once
	value ValueScope
	err   error
}

type dangModuleCacheKey struct{}

// WithDangModuleCache attaches a dir-keyed cache of compiled Dang modules to
// ctx. Consulted by loadDangModule so all files of one module reuse a single
// compiled *dangModule per imported directory (and thus one set of *Type
// identities). Mirrors WithSchemaModuleCache. ContextWithImportConfigs
// auto-creates one when none is attached; moduleImportContext installs a FRESH
// one per compiled module, which is what keeps each module's imports distinct
// (the per-module identity model).
func WithDangModuleCache(ctx context.Context, cache *sync.Map) context.Context {
	return context.WithValue(ctx, dangModuleCacheKey{}, cache)
}

func dangModuleCacheFromContext(ctx context.Context) *sync.Map {
	c, _ := ctx.Value(dangModuleCacheKey{}).(*sync.Map)
	return c
}

type compileStackKey struct{}

// compileStackFromContext returns the chain of module directories currently
// mid-compile, outermost first. It threads through ctx across the module
// boundary (unlike the per-module Dang-module cache, which is freshened per
// module) so a re-entrant import of a directory already on the chain is detected
// as a cycle — a shared-cache marker cannot do this job here, because each
// module compiles its dependencies with its OWN fresh cache and A->B->A would
// otherwise expand a new instance every hop, forever.
func compileStackFromContext(ctx context.Context) []string {
	s, _ := ctx.Value(compileStackKey{}).([]string)
	return s
}

func pushCompileStack(ctx context.Context, dir string) context.Context {
	prev := compileStackFromContext(ctx)
	next := make([]string, len(prev)+1)
	copy(next, prev)
	next[len(prev)] = dir
	return context.WithValue(ctx, compileStackKey{}, next)
}

// loadDangModule returns the compiled module rooted at dir, compiling it on a
// cache miss. The result is cached (keyed by resolved absolute dir) in the
// importing module's cache, so its files' repeated imports of that directory
// unify. Import cycles are reported via the ctx compile-stack rather than a
// cache marker, because each module compiles its deps with a fresh cache and a
// marker would not survive the boundary.
func loadDangModule(ctx context.Context, dir string) (*dangModule, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	// Cycle detection against the active compile path. See compileStackFromContext.
	stack := compileStackFromContext(ctx)
	for i, d := range stack {
		if d == dir {
			chain := append(append([]string(nil), stack[i:]...), dir)
			return nil, fmt.Errorf("import cycle detected: %s", strings.Join(chain, " -> "))
		}
	}

	cache := dangModuleCacheFromContext(ctx)
	if cache != nil {
		if v, ok := cache.Load(dir); ok {
			return v.(*dangModule), nil
		}
	}

	mod := &dangModule{dir: dir}
	if err := mod.compile(pushCompileStack(ctx, dir)); err != nil {
		return nil, err
	}

	// Store only after a successful compile: a failed load leaves no marker, so a
	// later pass (e.g. the LSP re-inferring after an edit) retries from scratch.
	// Imports infer sequentially per directory, so no LoadOrStore race here — the
	// same discipline the schema-module cache relies on.
	if cache != nil {
		cache.Store(dir, mod)
	}
	return mod, nil
}

// compile parses and type-checks the module's directory under its OWN module
// identity. The withModuleScope call is what makes cross-module `let` privacy
// work: the module's declared types are stamped with this scope, so a consumer
// compiled under a different scope is rejected by Select.Infer when it touches a
// `let` member (and permitted for a `pub` one). See moduleScope.
func (m *dangModule) compile(ctx context.Context) error {
	subCtx, blocks, err := parseModuleBlocks(ctx, m.dir)
	if err != nil {
		return err
	}
	if len(blocks) == 0 {
		return fmt.Errorf("no .dang files found in Dang module directory: %s", m.dir)
	}

	typeScope := NewPreludeTypeScope("")
	fresh := hm.NewSimpleFresher()

	subCtx = withModuleScope(subCtx, &moduleScope{label: "module:" + m.dir})

	if err := InferDirectoryFiles(subCtx, blocks, typeScope, fresh); err != nil {
		return ConvertInferError(err)
	}

	m.typeScope = typeScope
	m.blocks = blocks
	return nil
}

// valueScope evaluates the module's code exactly once and returns the resulting
// public value scope. Subsequent calls return the cached scope, so a module's
// top-level side effects run a single time regardless of how many importers it
// has. The module's own imports resolve from their inference-time cached node
// state, so the importer's ctx configs do not leak in here.
func (m *dangModule) valueScope(ctx context.Context) (ValueScope, error) {
	m.once.Do(func() {
		vs := NewValueScope(m.typeScope)
		// Give the module its own eval context so runtime errors point at the
		// module's files rather than the importer's.
		mctx := WithEvalContext(ctx, NewEvalContext(m.dir, ""))
		if err := evaluateDirectoryFiles(mctx, m.blocks, vs); err != nil {
			m.err = err
			return
		}
		m.value = vs
	})
	return m.value, m.err
}

// parseModuleBlocks parses a native Dang module's .dang files with an import
// context isolated from the importer: the module resolves its OWN dang.toml (at
// its root) and never inherits the importer's imports or auto-imports (e.g. a
// Dagger session the importer configured but the module never asked for).
func parseModuleBlocks(ctx context.Context, dir string) (context.Context, []*FileBlock, error) {
	subCtx, err := moduleImportContext(ctx, dir)
	if err != nil {
		return ctx, nil, err
	}
	return parseDirBlocks(subCtx, dir)
}

// moduleImportContext strips the importer's project/import configs and attaches
// the module's own, and gives the module FRESH schema and Dang-module caches.
// The fresh Dang-module cache is the per-module identity model: a module
// resolves its OWN dependencies into its OWN cache, so two different modules
// importing the same directory each compile a distinct instance and never share
// a *Type identity. Within a single module all files share this one cache, so a
// module's repeated / cross-file imports of one dependency still unify. Cross-
// module cycle detection deliberately does NOT rely on this cache — it uses the
// ctx compile-stack (see loadDangModule) precisely because the cache is fresh
// per module and could not carry a cycle marker across the boundary.
func moduleImportContext(ctx context.Context, dir string) (context.Context, error) {
	// Clear the importer's schema imports and give the module fresh schema and
	// Dang-module caches so neither GraphQL nor Dang types alias across the
	// module boundary.
	ctx = context.WithValue(ctx, importConfigsKey{}, []ImportConfig(nil))
	ctx = WithSchemaModuleCache(ctx, &sync.Map{})
	ctx = WithDangModuleCache(ctx, &sync.Map{})

	configPath := filepath.Join(dir, "dang.toml")
	if _, statErr := os.Stat(configPath); statErr == nil {
		config, err := LoadProjectConfig(configPath)
		if err != nil {
			return ctx, err
		}
		ctx = ContextWithProjectConfig(ctx, configPath, config)
		resolved, err := ResolveImportConfigs(ctx, config, dir)
		if err != nil {
			return ctx, err
		}
		if len(resolved) > 0 {
			ctx = ContextWithImportConfigs(ctx, resolved...)
		}
		return ctx, nil
	}

	// No module-level dang.toml: install an empty project config so
	// parseDirBlocks' ensureProjectImports treats the module as fully resolved
	// and does not walk UP into the importer's directory tree.
	ctx = ContextWithProjectConfig(ctx, configPath, &ProjectConfig{})
	return ctx, nil
}
