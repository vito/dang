package hm

// Env represents a type environment
type Env interface {
	SchemeOf(name string) (*Scheme, bool)
	Clone() Env
	Add(name string, scheme *Scheme) Env
	Remove(name string) Env
	FreeTypeVar() TypeVarSet
	Apply(subs Subs) Substitutable
	GetDynamicScopeType() Type
	SetDynamicScopeType(t Type)
}

// SimpleEnv is a simple implementation of Env
type SimpleEnv struct {
	schemes          map[string]*Scheme
	dynamicScopeType Type
}

// NewSimpleEnv creates a new SimpleEnv
func NewSimpleEnv() *SimpleEnv {
	return &SimpleEnv{
		schemes: make(map[string]*Scheme),
	}
}

// SchemeOf returns the scheme for a name
func (env *SimpleEnv) SchemeOf(name string) (*Scheme, bool) {
	scheme, exists := env.schemes[name]
	return scheme, exists
}

// Clone creates a copy of the environment
func (env *SimpleEnv) Clone() Env {
	newEnv := NewSimpleEnv()
	for name, scheme := range env.schemes {
		newEnv.schemes[name] = scheme.Clone()
	}
	return newEnv
}

// Add adds a binding to the environment
func (env *SimpleEnv) Add(name string, scheme *Scheme) Env {
	env.schemes[name] = scheme
	return env
}

// Remove removes a binding from the environment
func (env *SimpleEnv) Remove(name string) Env {
	newEnv := NewSimpleEnv()
	for n, scheme := range env.schemes {
		if n != name {
			newEnv.schemes[n] = scheme
		}
	}
	return newEnv
}

// FreeTypeVar returns the free type variables in the environment
func (env *SimpleEnv) FreeTypeVar() TypeVarSet {
	ftvs := make(TypeVarSet)
	for _, scheme := range env.schemes {
		schemeFtvs := scheme.FreeTypeVar()
		for tv := range schemeFtvs {
			ftvs[tv] = true
		}
	}
	return ftvs
}

// Apply applies a substitution to the environment
func (env *SimpleEnv) Apply(subs Subs) Substitutable {
	newEnv := NewSimpleEnv()
	for name, scheme := range env.schemes {
		newEnv.schemes[name] = scheme.Apply(subs).(*Scheme)
	}
	newEnv.dynamicScopeType = env.dynamicScopeType
	return newEnv
}

// GetDynamicScopeType returns the current dynamic scope type
func (env *SimpleEnv) GetDynamicScopeType() Type {
	return env.dynamicScopeType
}

// SetDynamicScopeType sets the current dynamic scope type
func (env *SimpleEnv) SetDynamicScopeType(t Type) {
	env.dynamicScopeType = t
}
