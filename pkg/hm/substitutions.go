package hm

// Subs represents a substitution mapping from type variables to types
type Subs map[TypeVariable]Type

// NewSubs creates a new substitution
func NewSubs() Subs {
	return make(Subs)
}

// Apply applies a substitution to a type
func (s Subs) Apply(t Type) Type {
	return t.Apply(s).(Type)
}

// Compose composes two substitutions
func (s Subs) Compose(other Subs) Subs {
	result := make(Subs)
	
	// Apply other to all types in s
	for tv, t := range s {
		result[tv] = t.Apply(other).(Type)
	}
	
	// Add mappings from other that aren't in s
	for tv, t := range other {
		if _, exists := result[tv]; !exists {
			result[tv] = t
		}
	}
	
	return result
}

// Clone creates a copy of the substitution
func (s Subs) Clone() Subs {
	result := make(Subs)
	for tv, t := range s {
		result[tv] = t
	}
	return result
}

// Add adds a substitution mapping
func (s Subs) Add(tv TypeVariable, t Type) {
	s[tv] = t
}

// Get gets a type for a type variable
func (s Subs) Get(tv TypeVariable) (Type, bool) {
	t, exists := s[tv]
	return t, exists
}