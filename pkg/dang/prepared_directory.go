package dang

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"
)

// ErrUnpreparableDirectory means a directory uses declarations whose evaluation
// still writes into the inferred type graph. Callers can use RunDir instead.
var ErrUnpreparableDirectory = errors.New("directory cannot be prepared")

// PreparedDirectory is a fully inferred source snapshot. Evaluation creates new
// values, closures, services and import bindings each time. No evaluated module
// environment or GraphQL client is retained by the prepared source.
//
// This experimental API deliberately excludes scalar hooks, static namespaces
// and native Dang imports until their runtime state is separated from types.
type PreparedDirectory struct {
	dir    string
	blocks []*FileBlock
	types  TypeScope
	module *moduleScope
}

// PrepareDir parses and completely infers a directory, including interface and
// effect checking. The caller must prepare again when source or import schemas
// change. Errors, including unsupported declarations, never evaluate user code.
func PrepareDir(ctx context.Context, dir string) (*PreparedDirectory, error) {
	ctx, blocks, err := parseDirBlocks(ctx, dir)
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("no .dang files found in directory: %s", dir)
	}
	if err := checkPreparable(blocks); err != nil {
		return nil, err
	}
	module := &moduleScope{label: "dir:" + dir}
	types := NewPreludeTypeScope("")
	ctx = withModuleScope(ctx, module)
	if err := InferDirectoryFiles(ctx, blocks, types, hm.NewSimpleFresher()); err != nil {
		return nil, ConvertInferError(err)
	}
	// Inference injects automatic imports and resolves native module imports.
	if err := checkPreparable(blocks); err != nil {
		return nil, err
	}
	for _, block := range blocks {
		block.Walk(func(node Node) bool {
			if imp, ok := node.(*ImportDecl); ok {
				imp.prepared = true
				imp.client = nil
			}
			return true
		})
	}
	return &PreparedDirectory{dir: dir, blocks: blocks, types: types, module: module}, nil
}

func checkPreparable(blocks []*FileBlock) error {
	var err error
	for _, block := range blocks {
		block.Walk(func(node Node) bool {
			if err != nil {
				return false
			}
			switch n := node.(type) {
			case *ScalarDecl, *SelfBlockDecl:
				err = fmt.Errorf("%w: %T", ErrUnpreparableDirectory, node)
			case *ImportDecl:
				if n.dangMod != nil {
					err = fmt.Errorf("%w: native import %s", ErrUnpreparableDirectory, n.Name.Name)
				}
			}
			return err == nil
		})
	}
	return err
}

// Evaluate runs all module initializers again and returns a fresh environment.
// Imports must provide the same schemas used for preparation, with the clients
// and cancellation context belonging to this invocation. It is safe to call
// Evaluate concurrently; the completed source/type graph is read-only.
func (p *PreparedDirectory) Evaluate(ctx context.Context) (ValueScope, error) {
	ctx, err := ensureProjectImports(ctx, p.dir)
	if err != nil {
		return nil, err
	}
	ctx, services := ensureServiceRegistry(ctx)
	if services != nil {
		defer services.StopAll()
	}
	ctx = withModuleScope(ctx, p.module)
	evalCtx := NewEvalContext(p.dir, "")
	ctx = WithEvalContext(ctx, evalCtx)
	values := NewValueScope(p.types)
	if err := evaluateDirectoryFiles(ctx, p.blocks, values); err != nil {
		if translated, ok := translateBoundaryEvalError(err, evalCtx); ok {
			return nil, translated
		}
		return nil, err
	}
	return values, nil
}

// A pointer comparison is the common case for an immutable schema cache. The
// structural fallback also permits equivalent schemas supplied by new clients.
func samePreparedSchema(left, right *introspection.Schema) bool {
	return left == right || reflect.DeepEqual(left, right)
}
