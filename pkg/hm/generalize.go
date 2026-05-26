package hm

import "fmt"

// Generalize creates a type scheme by quantifying over type variables
// that are free in the type but not free in the environment
func Generalize(env Env, t Type) *Scheme {
	var envFtvs TypeVarSet
	if env != nil {
		envFtvs = env.FreeTypeVar()
	} else {
		envFtvs = make(TypeVarSet)
	}

	typeFtvs := t.FreeTypeVar()

	// Find type variables that are free in the type but not in the environment
	var quantifiedVars []TypeVariable
	for tv := range typeFtvs {
		if !envFtvs.Contains(tv) {
			quantifiedVars = append(quantifiedVars, tv)
		}
	}

	return NewScheme(quantifiedVars, t)
}

// Instantiate creates a fresh instance of a type scheme
func Instantiate(fresher Fresher, scheme *Scheme) Type {
	if len(scheme.tvs) == 0 {
		return scheme.t
	}

	// Create fresh type variables for each quantified variable
	subs := NewSubs()
	for _, tv := range scheme.tvs {
		fresh := fresher.Fresh()
		subs.Add(tv, fresh)
	}

	return scheme.t.Apply(subs).(Type)
}

// Fresher interface for generating fresh type variables
type Fresher interface {
	Fresh() TypeVariable
}

// SimpleFresher is a simple implementation of Fresher
type SimpleFresher struct {
	counter int
}

// NewSimpleFresher creates a new SimpleFresher
func NewSimpleFresher() *SimpleFresher {
	return &SimpleFresher{counter: 0}
}

// Fresh generates a fresh type variable. Uses Greek letters so freshly
// generated variables cannot collide with source-level type variables,
// which use lowercase Latin letters.
func (f *SimpleFresher) Fresh() TypeVariable {
	greek := []rune("αβγδεζηθικλμνξοπρστυφχψω")

	if f.counter < len(greek) {
		tv := TypeVariable(greek[f.counter])
		f.counter++
		return tv
	}

	// Fall back to "τ0", "τ1", ... once the Greek letters are exhausted.
	// The earlier `'0' + n % 10` formulation collided after 34 calls.
	n := f.counter - len(greek)
	f.counter++
	return TypeVariable(fmt.Sprintf("τ%d", n))
}
