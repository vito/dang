package dang

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/vito/dang/pkg/hm"
)

func Infer(ctx context.Context, env hm.Env, expr hm.Expression, hoist bool) (*hm.Scheme, error) {
	if expr == nil {
		return nil, errors.Errorf("Cannot infer a nil expression")
	}

	if env == nil {
		env = hm.NewSimpleEnv()
	}

	infer := newInferer(env)
	if err := infer.consGen(ctx, expr); err != nil {
		return nil, err
	}

	if infer.t == nil {
		return nil, errors.Errorf("infer.t is nil")
	}

	return closeOver(infer.t)
}

type inferer struct {
	env hm.Env
	t   Type

	varCount int
}

func newInferer(env hm.Env) *inferer {
	return &inferer{
		env: env,
	}
}

// Fresh type variables use Greek letters so they cannot collide with
// source-level type variables, which the grammar restricts to lowercase
// Latin letters. This matters once scheme instantiation is wired into
// symbol/member lookup: instantiating `forall a. ...` with a fresh `a`
// would shadow any in-scope source `a`.
const greekLetters = "αβγδεζηθικλμνξοπρστυφχψω"

func (infer *inferer) Fresh() hm.TypeVariable {
	greek := []rune(greekLetters)
	if infer.varCount < len(greek) {
		retVal := greek[infer.varCount]
		infer.varCount++
		return hm.TypeVariable(retVal)
	}
	// Fall back to using numbers when we exhaust Greek letters
	numStart := infer.varCount - len(greek)
	char := rune('0' + (numStart % 10))
	infer.varCount++
	return hm.TypeVariable(char)
}

func (infer *inferer) consGen(ctx context.Context, expr hm.Expression) (err error) {
	// explicit types/inferers - can fail
	switch et := expr.(type) {
	case hm.Typer:
		infer.t = et.Type()
		return nil
	case hm.Inferer:
		infer.t, err = et.Infer(ctx, infer.env, infer)
		if err != nil {
			return err
		}
		return nil
	default:
		return errors.Errorf("expression of type %T is unhandled", expr)
	}
}

// InferenceErrors accumulates multiple errors during type inference
type InferenceErrors struct {
	Errors []error
}

func (ie *InferenceErrors) Add(err error) {
	if err != nil {
		ie.Errors = append(ie.Errors, ConvertInferError(err))
	}
}

func (ie *InferenceErrors) Unwrap() []error {
	return ie.Errors
}

func (ie *InferenceErrors) HasErrors() bool {
	return len(ie.Errors) > 0
}

func (ie *InferenceErrors) Error() string {
	if len(ie.Errors) == 0 {
		return "no errors"
	}
	if len(ie.Errors) == 1 {
		// Convert InferError to SourceError for pretty printing
		return ie.Errors[0].Error()
	}
	var msgs []string
	for i, err := range ie.Errors {
		// Convert each InferError to SourceError for pretty printing
		msgs = append(msgs, fmt.Sprintf("Error %d:\n%s", i+1, err.Error()))
	}
	return fmt.Sprintf("%d inference errors:\n\n%s", len(ie.Errors), strings.Join(msgs, "\n\n"))
}

func closeOver(t Type) (sch *hm.Scheme, err error) {
	sch = hm.Generalize(nil, t)
	err = sch.Normalize()
	return
}

// assignFallbackType assigns a fresh type variable to a declaration that failed inference
// This allows downstream code to continue type checking even if this declaration has errors
func assignFallbackType(decl Node, env hm.Env, fresh hm.Fresher) {
	// Get the declaration name(s)
	symbols := decl.DeclaredSymbols()
	if len(symbols) == 0 {
		return
	}

	for _, name := range symbols {
		// Create a fresh type variable as a fallback
		tv := fresh.Fresh()
		scheme := hm.NewScheme(nil, tv)

		// Add to environment so downstream references can resolve
		if dangEnv, ok := env.(Env); ok {
			dangEnv.Add(name, scheme)
			dangEnv.SetVisibility(name, PublicVisibility)
		}
	}
}
