package hm

import "slices"

import "strings"

import "fmt"

// Scheme represents a type scheme for polymorphic types
type Scheme struct {
	tvs []TypeVariable
	t   Type
}

// NewScheme creates a new type scheme
func NewScheme(tvs []TypeVariable, t Type) *Scheme {
	return &Scheme{tvs: tvs, t: t}
}

// Type returns the underlying type and whether it's monomorphic
func (s *Scheme) Type() (Type, bool) {
	// A scheme is monomorphic if it has no bound type variables
	return s.t, len(s.tvs) == 0
}

// TypeVars returns the bound type variables
func (s *Scheme) TypeVars() []TypeVariable {
	return s.tvs
}

// Apply applies a substitution to a scheme
func (s *Scheme) Apply(subs Subs) Substitutable {
	// Remove substitutions for bound variables
	filteredSubs := make(Subs)
	for tv, t := range subs {
		bound := slices.Contains(s.tvs, tv)
		if !bound {
			filteredSubs[tv] = t
		}
	}

	return &Scheme{
		tvs: s.tvs,
		t:   s.t.Apply(filteredSubs).(Type),
	}
}

// FreeTypeVar returns the free type variables in the scheme
func (s *Scheme) FreeTypeVar() TypeVarSet {
	ftvs := s.t.FreeTypeVar()

	// Remove bound variables
	for _, tv := range s.tvs {
		delete(ftvs, tv)
	}

	return ftvs
}

// Clone creates a copy of the scheme
func (s *Scheme) Clone() *Scheme {
	tvs := make([]TypeVariable, len(s.tvs))
	copy(tvs, s.tvs)
	return &Scheme{tvs: tvs, t: s.t}
}

// Normalize normalizes the type variable names in the scheme
func (s *Scheme) Normalize() error {
	// For now, we don't implement normalization
	return nil
}

// String returns a string representation
func (s *Scheme) String() string {
	if len(s.tvs) == 0 {
		return s.t.String()
	}

	var tvStrs []string
	for _, tv := range s.tvs {
		tvStrs = append(tvStrs, tv.String())
	}

	return fmt.Sprintf("forall %s. %s", joinStrings(tvStrs, " "), s.t.String())
}

// Helper function to join strings
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	if len(strs) == 1 {
		return strs[0]
	}

	var result strings.Builder
	result.WriteString(strs[0])
	for i := 1; i < len(strs); i++ {
		result.WriteString(sep + strs[i])
	}
	return result.String()
}
