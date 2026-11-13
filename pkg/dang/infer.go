package dang

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/vito/dang/v2/pkg/hm"
)

func Infer(ctx context.Context, env hm.Env, expr hm.Expression, hoist bool) (*hm.Scheme, error) {
	if expr == nil {
		return nil, errors.Errorf("Cannot infer a nil expression")
	}

	if env == nil {
		env = hm.NewSimpleEnv()
	}

	infer := newInferer(env)
	ctx, solver, ownSolver := withEffectSolver(ctx)
	if err := infer.consGen(ctx, expr); err != nil {
		return nil, err
	}
	if ownSolver {
		if errs := solver.solve(); len(errs) > 0 {
			all := &InferenceErrors{}
			for _, err := range errs {
				all.Add(err)
			}
			return nil, all
		}
	}

	if infer.t == nil {
		return nil, errors.Errorf("infer.t is nil")
	}

	return closeOver(infer.t)
}

// InferTopLevel type-checks forms as the top level of an interactive session
// (a REPL entry, a literate doc snippet, the playground) whose top-level
// bindings live in env, accumulating across calls. env is the top-level
// frame, as for the module entry points (RunFile, RunDir,
// InferDirectoryFiles): functions and methods may not write its bindings,
// while the forms themselves (script code) still may. Like
// InferFormsWithPhases, it runs the effect checks that need the whole input
// inferred first (escaping and `{{ }}` requirements on block parameters).
func InferTopLevel(ctx context.Context, forms []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// An entry may redefine what earlier ones declared (session.go).
	entry, err := beginSessionEntry(forms, env)
	if err != nil {
		return nil, err
	}
	t, err := InferFormsWithPhases(withTopLevelFrame(ctx, env), forms, env, fresh)
	if err == nil {
		err = entry.finish()
	}
	if err != nil {
		entry.rollback()
		return nil, err
	}
	entry.record(forms)
	return t, nil
}

type inferer struct {
	env hm.Env
	t   hm.Type

	varCount int
}

func newInferer(env hm.Env) *inferer {
	return &inferer{
		env: env,
	}
}

func (infer *inferer) Fresh() hm.TypeVariable {
	tv := hm.FreshVar(infer.varCount)
	infer.varCount++
	return tv
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

	// poisoned is set when inference failed only because it read a binding
	// whose declaration failed (see poisonType), which was reported where it
	// failed: the failure is kept, so what holds this has no type either,
	// but there is nothing new to report.
	poisoned bool
}

func (ie *InferenceErrors) Add(err error) {
	if err == nil {
		return
	}
	if isPoisoned(err) {
		ie.poisoned = true
		return
	}
	ie.Errors = append(ie.Errors, ConvertInferError(err))
}

func (ie *InferenceErrors) Unwrap() []error {
	return ie.Errors
}

func (ie *InferenceErrors) HasErrors() bool {
	return len(ie.Errors) > 0 || ie.poisoned
}

func (ie *InferenceErrors) Error() string {
	if len(ie.Errors) == 0 {
		if ie.poisoned {
			return "a declaration it depends on failed to type-check"
		}
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

func closeOver(t hm.Type) (sch *hm.Scheme, err error) {
	sch = hm.Generalize(nil, t)
	err = sch.Normalize()
	return
}

// assignFallbackType binds the names a declaration that failed inference
// declares, so that the code after it is still checked without reporting the
// failure again at each use. A name keeps a type the declaration itself
// gives it, so its uses are still checked against it: a function's declared
// signature, a field's annotation, or a type's constructor, bound when the
// type was hoisted. Any other is bound to poisonType.
func assignFallbackType(decl Node, env hm.Env) {
	typeScope, ok := env.(TypeScope)
	if !ok {
		return
	}
	for _, name := range decl.DeclaredSymbols() {
		var t hm.Type = poisonType{}
		switch d := decl.(type) {
		case *FieldDecl:
			if fn := d.funDecl(); fn != nil && fn.declared != nil {
				t = fn.declared
			} else if d.annotated != nil {
				t = d.annotated
			}
		case *ObjectDecl, *ScalarDecl, *InterfaceDecl, *UnionDecl, *EnumDecl:
			if hasKnownType(typeScope, name) {
				continue
			}
		}
		typeScope.Add(name, hm.NewScheme(nil, t))
		typeScope.SetVisibility(name, PublicVisibility)
	}
}

// hasKnownType reports whether scope binds name to a type that says
// something: not a poisonType, nor a bare type variable standing for a type
// still to be inferred.
func hasKnownType(scope TypeScope, name string) bool {
	scheme, ok := scope.LocalSchemeOf(name)
	if !ok {
		return false
	}
	t, _ := scheme.Type()
	switch unwrapNonNull(hm.ResolveWeak(t)).(type) {
	case nil, poisonType, hm.TypeVariable:
		return false
	}
	return true
}

// poisonType is the type of a binding whose declaration failed to
// type-check. Its error was reported there; a read of the binding fails
// with a poisonedError, which InferenceErrors keeps as a failure without
// reporting it, so whatever reads it fails quietly too (and a declaration
// doing so is poisoned in turn) while the code around it is still checked.
// Without it, a failed `let c = ...` was followed by `"c" not found` (or an
// error about a type it never had) at every use.
type poisonType struct{}

// poisonTypeName is how a poisoned binding's type shows, e.g. in hover and
// completion. Not `<error>`: editors may take angle brackets for markup.
const poisonTypeName = "unknown"

var _ hm.Type = poisonType{}

func (poisonType) Name() string                                    { return poisonTypeName }
func (t poisonType) Apply(hm.Subs) hm.Substitutable                { return t }
func (poisonType) FreeTypeVar() hm.TypeVarSet                      { return nil }
func (t poisonType) Normalize(_, _ hm.TypeVarSet) (hm.Type, error) { return t, nil }
func (poisonType) Types() hm.Types                                 { return nil }
func (poisonType) Supertypes() []hm.Type                           { return nil }
func (poisonType) String() string                                  { return poisonTypeName }
func (poisonType) Format(s fmt.State, _ rune)                      { _, _ = fmt.Fprint(s, poisonTypeName) }
func (poisonType) Eq(other hm.Type) bool {
	_, ok := other.(poisonType)
	return ok
}

// poisonedError is the failure to read a binding of poisonType.
type poisonedError struct {
	Name string
}

func (e *poisonedError) Error() string {
	return fmt.Sprintf("%q failed to type-check", e.Name)
}

// isPoisoned reports whether err is only the failure to read poisoned
// bindings: a poisonedError, however wrapped, or a group of errors that
// all are.
func isPoisoned(err error) bool {
	switch e := err.(type) {
	case nil:
		return false
	case *poisonedError:
		return true
	case *InferenceErrors:
		return len(e.Errors) == 0 && e.poisoned
	case interface{ Unwrap() []error }:
		errs := e.Unwrap()
		for _, err := range errs {
			if !isPoisoned(err) {
				return false
			}
		}
		return len(errs) > 0
	case interface{ Unwrap() error }:
		return isPoisoned(e.Unwrap())
	}
	return false
}
