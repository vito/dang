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

// Compose composes substitutions while checking repeated bindings for
// compatibility. When both substitutions constrain the same type variable, the
// variable is bound to the least common assignable type (preserving nullable
// taint) instead of whichever binding happened to be seen first.
func (s Subs) Compose(other Subs) (Subs, error) {
	result := make(Subs)

	// Apply other to all types in s, matching normal substitution composition.
	for tv, t := range s {
		result[tv] = t.Apply(other).(Type)
	}

	for tv, t := range other {
		if existing, exists := result[tv]; exists {
			merged, extraSubs, err := MergeTypes(existing, t)
			if err != nil {
				return nil, err
			}
			if len(extraSubs) > 0 {
				var err error
				result, err = result.mergeIn(extraSubs)
				if err != nil {
					return nil, err
				}
				merged = merged.Apply(extraSubs).(Type)
			}
			result[tv] = merged
			continue
		}
		result[tv] = t
	}

	return result, nil
}

func (s Subs) mergeIn(extra Subs) (Subs, error) {
	result := make(Subs, len(s)+len(extra))
	for tv, t := range s {
		result[tv] = t.Apply(extra).(Type)
	}
	for tv, t := range extra {
		if existing, exists := result[tv]; exists {
			merged, moreSubs, err := MergeTypes(existing, t)
			if err != nil {
				return nil, err
			}
			result[tv] = merged
			if len(moreSubs) > 0 {
				return result.mergeIn(moreSubs)
			}
			continue
		}
		result[tv] = t
	}
	return result, nil
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
