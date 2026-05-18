package hm

import "maps"

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

	// Add mappings from other that aren't in s. If both substitutions mention
	// the same variable, preserve nullable taint from either side instead of
	// letting a later non-null binding silently erase it.
	for tv, t := range other {
		if existing, exists := result[tv]; exists {
			result[tv] = mergeSubstitution(existing, t)
			continue
		}
		result[tv] = t
	}

	return result
}

func mergeSubstitution(existing, incoming Type) Type {
	if _, ok := existing.(NullableTypeVariable); ok {
		return makeNullable(incoming)
	}
	if _, ok := incoming.(NullableTypeVariable); ok {
		return makeNullable(existing)
	}
	if existingNonNull, ok := existing.(NonNullType); ok && existingNonNull.Type.Eq(incoming) {
		return incoming
	}
	if incomingNonNull, ok := incoming.(NonNullType); ok && incomingNonNull.Type.Eq(existing) {
		return existing
	}
	return existing
}

// Clone creates a copy of the substitution
func (s Subs) Clone() Subs {
	result := make(Subs)
	maps.Copy(result, s)
	return result
}

// Add adds a substitution mapping and returns the updated substitution
func (s Subs) Add(tv TypeVariable, t Type) Subs {
	s[tv] = t
	return s
}

// Get gets a type for a type variable
func (s Subs) Get(tv TypeVariable) (Type, bool) {
	t, exists := s[tv]
	return t, exists
}

// Substitution represents a single type variable to type mapping
type Substitution struct {
	Tv TypeVariable
	T  Type
}

// Iter iterates over the substitutions
func (s Subs) Iter() []Substitution {
	var result []Substitution
	for tv, t := range s {
		result = append(result, Substitution{Tv: tv, T: t})
	}
	return result
}

// ReturnSubs is a no-op function for compatibility with object pooling
func ReturnSubs(s Subs) {
	// No-op - not implementing object pooling
}
