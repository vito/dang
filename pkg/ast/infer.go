package ast

import (
	"fmt"

	"github.com/chewxy/hm"
	"github.com/pkg/errors"
)

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
	retVal := letters[infer.count]
	infer.count++
	return hm.TypeVariable(retVal)
}

func (infer *inferer) lookup(name string) error {
	s, ok := infer.env.SchemeOf(name)
	if !ok {
		return errors.Errorf("Undefined %v", name)
	}
	infer.t = hm.Instantiate(infer, s)
	return nil
}

func (infer *inferer) consGen(expr hm.Expression) (err error) {
	// explicit types/inferers - can fail
	switch et := expr.(type) {
	case hm.Typer:
		if infer.t = et.Type(); infer.t != nil {
			return nil
		}
	case hm.Inferer:
		infer.t, err = et.Infer(infer.env, infer)
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

// Infer takes an env, and an expression, and returns a scheme.
//
// The Infer function is the core of the HM type inference system. This is a reference implementation and is completely servicable, but not quite performant.
// You should use this as a reference and write your own infer function.
//
// Very briefly, these rules are implemented:
//
// # Var
//
// If x is of type T, in a collection of statements Γ, then we can infer that x has type T when we come to a new instance of x
//
//	 x: T ∈ Γ
//	-----------
//	 Γ ⊢ x: T
//
// # Apply
//
// If f is a function that takes T1 and returns T2; and if x is of type T1;
// then we can infer that the result of applying f on x will yield a result has type T2
//
//	 Γ ⊢ f: T1→T2  Γ ⊢ x: T1
//	-------------------------
//	     Γ ⊢ f(x): T2
//
// # Lambda Abstraction
//
// If we assume x has type T1, and because of that we were able to infer e has type T2
// then we can infer that the lambda abstraction of e with respect to the variable x,  λx.e,
// will be a function with type T1→T2
//
//	  Γ, x: T1 ⊢ e: T2
//	-------------------
//	  Γ ⊢ λx.e: T1→T2
//
// # Let
//
// If we can infer that e1 has type T1 and if we take x to have type T1 such that we could infer that e2 has type T2,
// then we can infer that the result of letting x = e1 and substituting it into e2 has type T2
//
//	  Γ, e1: T1  Γ, x: T1 ⊢ e2: T2
//	--------------------------------
//	     Γ ⊢ let x = e1 in e2: T2
func Infer(env hm.Env, expr hm.Expression) (*hm.Scheme, error) {
	if expr == nil {
		return nil, errors.Errorf("Cannot infer a nil expression")
	}

	if env == nil {
		env = make(hm.SimpleEnv)
	}

	infer := newInferer(env)
	if err := infer.consGen(expr); err != nil {
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
