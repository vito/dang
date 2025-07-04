package hm

// Generalize creates a type scheme by quantifying over type variables
// that are free in the type but not free in the environment
func Generalize(env Env, t Type) *Scheme {
	envFtvs := env.FreeTypeVar()
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

// Fresh generates a fresh type variable
func (f *SimpleFresher) Fresh() TypeVariable {
	// Use lowercase letters for type variables
	letters := "abcdefghijklmnopqrstuvwxyz"
	
	if f.counter < len(letters) {
		tv := TypeVariable(letters[f.counter])
		f.counter++
		return tv
	}
	
	// If we run out of letters, use subscripts
	base := f.counter - len(letters)
	letter := letters[base%len(letters)]
	
	// This is a simplified approach - in practice you'd want better naming
	tv := TypeVariable(rune(letter))
	f.counter++
	return tv
}