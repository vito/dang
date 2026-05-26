package hm

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

	// Fall back to using numbers when we exhaust Greek letters
	base := f.counter - len(greek)
	char := rune('0' + (base % 10))
	f.counter++
	return TypeVariable(char)
}
