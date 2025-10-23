package dang

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/vito/dang/pkg/hm"
)

// InferenceErrors accumulates multiple errors during type inference
type InferenceErrors struct {
	Errors []error
}

func (ie *InferenceErrors) Add(err error) {
	if err != nil {
		ie.Errors = append(ie.Errors, err)
	}
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
		return ConvertInferError(ie.Errors[0]).Error()
	}
	var msgs []string
	for i, err := range ie.Errors {
		// Convert each InferError to SourceError for pretty printing
		converted := ConvertInferError(err)
		msgs = append(msgs, fmt.Sprintf("Error %d:\n%s", i+1, converted.Error()))
	}
	return fmt.Sprintf("%d inference errors:\n\n%s", len(ie.Errors), strings.Join(msgs, "\n\n"))
}

type inferer struct {
	env hm.Env
	cs  Constraints
	t   Type

	count int
}

func newInferer(env hm.Env) *inferer {
	return &inferer{
		env: env,
	}
}

const letters = `abcdefghijklmnopqrstuvwxyz`

func (infer *inferer) Fresh() hm.TypeVariable {
	if infer.count < len(letters) {
		retVal := letters[infer.count]
		infer.count++
		return hm.TypeVariable(retVal)
	} else {
		// Use Greek letters and other Unicode characters when we run out of Latin letters
		// Start with Greek lowercase letters (α, β, γ, etc.)
		greekStart := infer.count - len(letters)
		if greekStart < 24 { // 24 Greek letters
			greek := rune('α' + greekStart)
			infer.count++
			return hm.TypeVariable(greek)
		} else {
			// Fall back to using numbers as characters
			numStart := greekStart - 24
			char := rune('0' + (numStart % 10))
			infer.count++
			return hm.TypeVariable(char)
		}
	}
}

func (infer *inferer) lookup(name string) error {
	s, ok := infer.env.SchemeOf(name)
	if !ok {
		return errors.Errorf("Undefined %v", name)
	}
	infer.t = hm.Instantiate(infer, s)
	return nil
}

func (infer *inferer) consGen(ctx context.Context, expr hm.Expression) (err error) {
	// explicit types/inferers - can fail
	switch et := expr.(type) {
	case hm.Typer:
		if infer.t = et.Type(); infer.t != nil {
			return nil
		}
	case hm.Inferer:
		infer.t, err = et.Infer(ctx, infer.env, infer)
		if err != nil {
			return err
		}
		return nil
	}

	return errors.Errorf("Expression of %T is unhandled", expr)

	// fallbacks

	// switch et := expr.(type) {
	// case Literal:
	// 	return infer.lookup(et.Name())

	// case Var:
	// 	if err = infer.lookup(et.Name()); err != nil {
	// 		infer.env.Add(et.Name(), &Scheme{t: et.Type()})
	// 		err = nil
	// 	}

	// case Lambda:
	// 	tv := infer.Fresh()
	// 	env := infer.env // backup

	// 	infer.env = infer.env.Clone()
	// 	infer.env.Remove(et.Name())
	// 	sc := new(Scheme)
	// 	sc.t = tv
	// 	infer.env.Add(et.Name(), sc)

	// 	if err = infer.consGen(et.Body()); err != nil {
	// 		return errors.Wrapf(err, "Unable to infer body of %v. Body: %v", et, et.Body())
	// 	}

	// 	infer.t = NewFnType(tv, infer.t)
	// 	infer.env = env // restore backup

	// case Apply:
	// 	if err = infer.consGen(et.Fn()); err != nil {
	// 		return errors.Wrapf(err, "Unable to infer Fn of Apply: %v. Fn: %v", et, et.Fn())
	// 	}
	// 	fnType, fnCs := infer.t, infer.cs

	// 	if err = infer.consGen(et.Body()); err != nil {
	// 		return errors.Wrapf(err, "Unable to infer body of Apply: %v. Body: %v", et, et.Body())
	// 	}
	// 	bodyType, bodyCs := infer.t, infer.cs

	// 	tv := infer.Fresh()
	// 	cs := append(fnCs, bodyCs...)
	// 	cs = append(cs, Constraint{fnType, NewFnType(bodyType, tv)})

	// 	infer.t = tv
	// 	infer.cs = cs

	// case LetRec:
	// 	tv := infer.Fresh()
	// 	// env := infer.env // backup

	// 	infer.env = infer.env.Clone()
	// 	infer.env.Remove(et.Name())
	// 	infer.env.Add(et.Name(), &Scheme{tvs: TypeVarSet{tv}, t: tv})

	// 	if err = infer.consGen(et.Def()); err != nil {
	// 		return errors.Wrapf(err, "Unable to infer the definition of a letRec %v. Def: %v", et, et.Def())
	// 	}
	// 	defType, defCs := infer.t, infer.cs

	// 	s := newSolver()
	// 	s.solve(defCs)
	// 	if s.err != nil {
	// 		return errors.Wrapf(s.err, "Unable to solve constraints of def: %v", defCs)
	// 	}

	// 	sc := Generalize(infer.env.Apply(s.sub).(Env), defType.Apply(s.sub).(Type))

	// 	infer.env.Remove(et.Name())
	// 	infer.env.Add(et.Name(), sc)

	// 	if err = infer.consGen(et.Body()); err != nil {
	// 		return errors.Wrapf(err, "Unable to infer body of letRec %v. Body: %v", et, et.Body())
	// 	}

	// 	infer.t = infer.t.Apply(s.sub).(Type)
	// 	infer.cs = infer.cs.Apply(s.sub).(Constraints)
	// 	infer.cs = append(infer.cs, defCs...)

	// case Let:
	// 	env := infer.env

	// 	if err = infer.consGen(et.Def()); err != nil {
	// 		return errors.Wrapf(err, "Unable to infer the definition of a let %v. Def: %v", et, et.Def())
	// 	}
	// 	defType, defCs := infer.t, infer.cs

	// 	s := newSolver()
	// 	s.solve(defCs)
	// 	if s.err != nil {
	// 		return errors.Wrapf(s.err, "Unable to solve for the constraints of a def %v", defCs)
	// 	}

	// 	sc := Generalize(env.Apply(s.sub).(Env), defType.Apply(s.sub).(Type))
	// 	infer.env = infer.env.Clone()
	// 	infer.env.Remove(et.Name())
	// 	infer.env.Add(et.Name(), sc)

	// 	if err = infer.consGen(et.Body()); err != nil {
	// 		return errors.Wrapf(err, "Unable to infer body of let %v. Body: %v", et, et.Body())
	// 	}

	// 	infer.t = infer.t.Apply(s.sub).(Type)
	// 	infer.cs = infer.cs.Apply(s.sub).(Constraints)
	// 	infer.cs = append(infer.cs, defCs...)

	// default:
	// 	return errors.Errorf("Expression of %T is unhandled", expr)
	// }

	// return nil
}

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

	s := newSolver()
	s.solve(infer.cs)

	if s.err != nil {
		return nil, s.err
	}

	if infer.t == nil {
		return nil, errors.Errorf("infer.t is nil")
	}

	t := infer.t.Apply(s.sub).(Type)
	return closeOver(t)
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

type solver struct {
	sub hm.Subs
	err error
}

func newSolver() *solver {
	return new(solver)
}

type Constraints []Constraint

func (cs Constraints) Apply(sub hm.Subs) hm.Substitutable {
	// an optimization
	if sub == nil {
		return cs
	}

	if len(cs) == 0 {
		return cs
	}

	// logf("Constraints: %d", len(cs))
	// logf("Applying %v to %v", sub, cs)
	for i, c := range cs {
		cs[i] = c.Apply(sub).(Constraint)
	}
	// logf("Constraints %v", cs)
	return cs
}

func (cs Constraints) FreeTypeVar() hm.TypeVarSet {
	var retVal hm.TypeVarSet
	for _, v := range cs {
		retVal = v.FreeTypeVar().Union(retVal)
	}
	return retVal
}

func (cs Constraints) Format(state fmt.State, c rune) {
	state.Write([]byte("Constraints["))
	for i, c := range cs {
		if i < len(cs)-1 {
			fmt.Fprintf(state, "%v, ", c)
		} else {
			fmt.Fprintf(state, "%v", c)
		}
	}
	state.Write([]byte{']'})
}

func (s *solver) solve(cs Constraints) {
	if s.err != nil {
		return
	}

	switch len(cs) {
	case 0:
		return
	default:
		var sub hm.Subs
		c := cs[0]
		sub, s.err = hm.Unify(c.a, c.b)
		defer hm.ReturnSubs(s.sub)

		s.sub = compose(sub, s.sub)
		cs = cs[1:].Apply(s.sub).(Constraints)
		s.solve(cs)
	}
}

func compose(a, b hm.Subs) (retVal hm.Subs) {
	if b == nil {
		return a
	}

	retVal = b.Clone()

	if a == nil {
		return
	}

	for _, v := range a.Iter() {
		retVal = retVal.Add(v.Tv, v.T)
	}

	for _, v := range retVal.Iter() {
		retVal = retVal.Add(v.Tv, v.T.Apply(a).(Type))
	}
	return retVal
}

type Constraint struct {
	a, b Type
}

func (c Constraint) Apply(sub hm.Subs) hm.Substitutable {
	c.a = c.a.Apply(sub).(Type)
	c.b = c.b.Apply(sub).(Type)
	return c
}

func (c Constraint) FreeTypeVar() hm.TypeVarSet {
	var retVal hm.TypeVarSet
	retVal = c.a.FreeTypeVar().Union(retVal)
	retVal = c.b.FreeTypeVar().Union(retVal)
	return retVal
}

func (c Constraint) Format(state fmt.State, r rune) {
	fmt.Fprintf(state, "{%v = %v}", c.a, c.b)
}
