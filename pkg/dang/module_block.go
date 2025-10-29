package dang

import (
	"context"
	"errors"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

// ModuleBlock represents a top-level file that supports forward references
// and phased evaluation. Unlike Block, which evaluates forms in textual order,
// ModuleBlock hoists declarations to enable forward references across the module.
type ModuleBlock struct {
	InferredTypeHolder
	Forms []Node
	// Evaluate forms in the current scope, not a nested one.
	Inline bool
	Loc    *SourceLocation

	// Filled in during inference phase
	Env Env
}

var _ hm.Expression = (*ModuleBlock)(nil)
var _ Evaluator = (*ModuleBlock)(nil)
var _ Node = (*ModuleBlock)(nil)
var _ Hoister = (*ModuleBlock)(nil)

func (m *ModuleBlock) DeclaredSymbols() []string {
	return nil // ModuleBlocks don't declare symbols directly (their forms do)
}

func (m *ModuleBlock) ReferencedSymbols() []string {
	var symbols []string

	// Add symbols from all forms in the module
	for _, form := range m.Forms {
		symbols = append(symbols, form.ReferencedSymbols()...)
	}

	return symbols
}

func (m *ModuleBlock) Body() hm.Expression { return m }

func (m *ModuleBlock) GetSourceLocation() *SourceLocation { return m.Loc }

func (m *ModuleBlock) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, depth int) error {
	newEnv := env
	if !m.Inline {
		newEnv = env.Clone()
	}

	var errs []error
	for _, form := range m.Forms {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, newEnv, fresh, depth); err != nil {
				errs = append(errs, fmt.Errorf("ModuleBlock.Hoist: %w", err))
			}
		}
	}
	return errors.Join(errs...)
}

var _ hm.Inferer = (*ModuleBlock)(nil)

func (m *ModuleBlock) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
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

func (m *ModuleBlock) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	newEnv := env
	if !m.Inline {
		newEnv = env.Clone()
	}

	forms := m.Forms
	if len(forms) == 0 {
		return NullValue{}, nil
	}

	// Use phased evaluation to match the inference order
	return EvaluateFormsWithPhases(ctx, forms, newEnv)
}

func (m *ModuleBlock) Walk(fn func(Node) bool) {
	if !fn(m) {
		return
	}
	for _, form := range m.Forms {
		form.Walk(fn)
	}
}
