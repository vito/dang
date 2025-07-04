package hm

// TypeVarSet represents a set of type variables
type TypeVarSet map[TypeVariable]bool

// NewTypeVarSet creates a new TypeVarSet
func NewTypeVarSet(tvs ...TypeVariable) TypeVarSet {
	set := make(TypeVarSet)
	for _, tv := range tvs {
		set[tv] = true
	}
	return set
}

// Union returns the union of two TypeVarSets
func (tvs TypeVarSet) Union(other TypeVarSet) TypeVarSet {
	result := make(TypeVarSet)
	for tv := range tvs {
		result[tv] = true
	}
	for tv := range other {
		result[tv] = true
	}
	return result
}

// Contains checks if a type variable is in the set
func (tvs TypeVarSet) Contains(tv TypeVariable) bool {
	return tvs[tv]
}

// Add adds a type variable to the set
func (tvs TypeVarSet) Add(tv TypeVariable) {
	tvs[tv] = true
}

// Remove removes a type variable from the set
func (tvs TypeVarSet) Remove(tv TypeVariable) {
	delete(tvs, tv)
}

// ToSlice converts the set to a slice
func (tvs TypeVarSet) ToSlice() []TypeVariable {
	result := make([]TypeVariable, 0, len(tvs))
	for tv := range tvs {
		result = append(result, tv)
	}
	return result
}