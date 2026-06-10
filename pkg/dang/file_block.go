package dang

import (
	"context"
	"errors"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// FileBlock represents a top-level file that supports forward references
// and phased evaluation. Unlike Block, which evaluates forms in textual order,
// FileBlock hoists declarations to enable forward references across the file.
type FileBlock struct {
	InferredTypeHolder
	Forms []Node
	// Evaluate forms in the current scope, not a nested one.
	Inline bool
	Loc    *SourceLocation

	// Filled in during inference phase
	TypeScope TypeScope
}

var _ hm.Expression = (*FileBlock)(nil)
var _ Evaluator = (*FileBlock)(nil)
var _ Node = (*FileBlock)(nil)
var _ Hoister = (*FileBlock)(nil)

func (m *FileBlock) DeclaredSymbols() []string {
	return nil // FileBlocks don't declare symbols directly (their forms do)
}

func (m *FileBlock) ReferencedSymbols() []string {
	var symbols []string

	// Add symbols from all forms in the file
	for _, form := range m.Forms {
		symbols = append(symbols, form.ReferencedSymbols()...)
	}

	return symbols
}

func (m *FileBlock) Body() hm.Expression { return m }

func (m *FileBlock) GetSourceLocation() *SourceLocation { return m.Loc }

func (m *FileBlock) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, depth int) error {
	newEnv := env
	if !m.Inline {
		newEnv = env.Clone()
	}

	var errs []error
	for _, form := range m.Forms {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, newEnv, fresh, depth); err != nil {
				errs = append(errs, fmt.Errorf("FileBlock.Hoist: %w", err))
			}
		}
	}
	return errors.Join(errs...)
}

var _ hm.Inferer = (*FileBlock)(nil)

func (m *FileBlock) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(m, func() (hm.Type, error) {
		newEnv := env
		if !m.Inline {
			newEnv = env.Clone()
		}

		forms := m.Forms
		if len(forms) == 0 {
			forms = append(forms, &Null{})
		}

		// Use phased inference approach for proper dependency handling
		return InferFormsWithPhases(ctx, forms, newEnv, fresh)
	})
}

func (m *FileBlock) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	newScope := scope
	if !m.Inline {
		newScope = scope.Derive(false)
	}

	forms := m.Forms
	if len(forms) == 0 {
		return NullValue{}, nil
	}

	// Use phased evaluation to match the inference order
	return EvaluateFormsWithPhases(ctx, forms, newScope)
}

func (m *FileBlock) Walk(fn func(Node) bool) {
	if !fn(m) {
		return
	}
	for _, form := range m.Forms {
		form.Walk(fn)
	}
}
