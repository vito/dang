package dang

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/vito/dang/v2/pkg/hm"
)

// dangModule is a compiled native Dang module: its inferred public type scope
// plus the parsed file blocks needed to evaluate its code. Exactly one is
// produced per module directory per build and shared by every importer (keyed
// by resolved absolute dir in the ctx-scoped Dang-module cache), so its types
// unify across the dependency graph and its homeModule identity is stable —
// the same guarantee the schema-module cache gives GraphQL imports.
//
// The module is frozen once compiled: its top-level code runs at most once (on
// first evaluation), exactly like the prelude.
type dangModule struct {
	dir       string
	typeScope TypeScope
	blocks    []*FileBlock

	// inProgress marks a module whose inference is mid-flight, so a re-entrant
	// load (an import cycle) is detected and errored rather than looping.
	inProgress bool

	once  sync.Once
	value ValueScope
	err   error
}

type dangModuleCacheKey struct{}

// WithDangModuleCache attaches a dir-keyed cache of compiled Dang modules to
// ctx. Consulted by loadDangModule so every importer of the same module
// directory reuses one compiled *dangModule (and thus one set of *Type
// identities). Mirrors WithSchemaModuleCache. ContextWithImportConfigs
// auto-creates one when none is attached.
func WithDangModuleCache(ctx context.Context, cache *sync.Map) context.Context {
	return context.WithValue(ctx, dangModuleCacheKey{}, cache)
}

func dangModuleCacheFromContext(ctx context.Context) *sync.Map {
	c, _ := ctx.Value(dangModuleCacheKey{}).(*sync.Map)
	return c
}

// loadDangModule returns the compiled module rooted at dir, compiling it on a
// cache miss. The result is cached (keyed by resolved absolute dir) so repeated
// and cross-file imports of the same module unify. Compilation is marked
// in-progress in the cache so an import cycle is reported rather than looping.
func loadDangModule(ctx context.Context, dir string) (*dangModule, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	cache := dangModuleCacheFromContext(ctx)
	if cache != nil {
		if v, ok := cache.Load(dir); ok {
			mod := v.(*dangModule)
			if mod.inProgress {
				return nil, fmt.Errorf("import cycle detected: Dang module %s imports itself (directly or transitively)", dir)
			}
			return mod, nil
		}
	}

	mod := &dangModule{dir: dir, inProgress: true}
	if cache != nil {
		// Publish the in-progress marker before inference so a transitive import
		// back to this directory is detected as a cycle.
		if actual, loaded := cache.LoadOrStore(dir, mod); loaded {
			existing := actual.(*dangModule)
			if existing.inProgress {
				return nil, fmt.Errorf("import cycle detected: Dang module %s imports itself (directly or transitively)", dir)
			}
			return existing, nil
		}
	}

	if err := mod.compile(ctx); err != nil {
		if cache != nil {
			// Drop the failed marker so a later pass (e.g. the LSP re-inferring
			// after an edit) can retry from scratch.
			cache.Delete(dir)
		}
		return nil, err
	}
	mod.inProgress = false
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
// the module's own. The Dang-module cache is intentionally left in place so
// nested Dang modules unify across the whole build.
func moduleImportContext(ctx context.Context, dir string) (context.Context, error) {
	// Clear the importer's schema imports and give the module a fresh schema
	// cache so imported GraphQL types don't alias across the module boundary.
	ctx = context.WithValue(ctx, importConfigsKey{}, []ImportConfig(nil))
	ctx = WithSchemaModuleCache(ctx, &sync.Map{})

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
