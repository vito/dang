package dang

import (
	"fmt"
	"iter"
	"strings"

	"github.com/vito/dang/pkg/hm"
)

// AppliedType represents a generic class type applied to its type arguments,
// e.g. Box[Int!] or Either[Int!, String!]. It implements both hm.Type and
// dang.Env: when asked for a field's scheme it delegates to the underlying
// Base module but substitutes the class-level type parameters with the
// supplied Args.
type AppliedType struct {
	Base *Module
	Args []hm.Type
}

// NewAppliedType constructs an AppliedType after verifying arity matches.
func NewAppliedType(base *Module, args []hm.Type) *AppliedType {
	return &AppliedType{Base: base, Args: args}
}

// substitutions builds a Subs mapping the base's type parameters to this
// application's arguments.
func (t *AppliedType) substitutions() hm.Subs {
	subs := hm.NewSubs()
	for i, tv := range t.Base.TypeParams {
		if i >= len(t.Args) {
			break
		}
		subs.Add(tv, t.Args[i])
	}
	return subs
}

// substituteScheme returns a scheme rewritten so that this application's
// args replace the class-level type parameters. Any quantified variables
// remain quantified.
func (t *AppliedType) substituteScheme(s *hm.Scheme) *hm.Scheme {
	if s == nil {
		return nil
	}
	subs := t.substitutions()
	if len(subs) == 0 {
		return s
	}
	return s.Apply(subs).(*hm.Scheme)
}

// --- hm.Type ---

var _ hm.Type = (*AppliedType)(nil)

func (t *AppliedType) Name() string {
	return t.String()
}

func (t *AppliedType) Apply(subs hm.Subs) hm.Substitutable {
	if len(subs) == 0 {
		return t
	}
	args := make([]hm.Type, len(t.Args))
	for i, a := range t.Args {
		args[i] = a.Apply(subs).(hm.Type)
	}
	return &AppliedType{Base: t.Base, Args: args}
}

func (t *AppliedType) FreeTypeVar() hm.TypeVarSet {
	var tvs hm.TypeVarSet
	for _, a := range t.Args {
		tvs = a.FreeTypeVar().Union(tvs)
	}
	return tvs
}

func (t *AppliedType) Normalize(k, v hm.TypeVarSet) (Type, error) {
	args := make([]hm.Type, len(t.Args))
	for i, a := range t.Args {
		n, err := a.Normalize(k, v)
		if err != nil {
			return nil, err
		}
		args[i] = n
	}
	return &AppliedType{Base: t.Base, Args: args}, nil
}

func (t *AppliedType) Types() hm.Types {
	out := make(hm.Types, len(t.Args))
	copy(out, t.Args)
	return out
}

func (t *AppliedType) Eq(other Type) bool {
	ot, ok := other.(*AppliedType)
	if !ok {
		return false
	}
	if t.Base != ot.Base {
		return false
	}
	if len(t.Args) != len(ot.Args) {
		return false
	}
	for i, a := range t.Args {
		if !a.Eq(ot.Args[i]) {
			return false
		}
	}
	return true
}

func (t *AppliedType) Supertypes() []Type {
	// Walk the base's interfaces and unions, applying this instance's
	// argument substitution so a generic interface reference like
	// `Container[a]` becomes `Container[Int!]` for a Box[Int!] receiver.
	bases := t.Base.Supertypes()
	if len(bases) == 0 {
		return nil
	}
	subs := t.substitutions()
	out := make([]Type, len(bases))
	for i, s := range bases {
		out[i] = s.Apply(subs).(Type)
	}
	return out
}

// SameTypeConstructor reports whether two AppliedTypes share the same base.
// Used by unification to require constructor-aware matching of generic types
// instead of falling through to reflect.TypeOf-based composite unification.
func (t *AppliedType) SameTypeConstructor(other hm.Type) bool {
	ot, ok := other.(*AppliedType)
	if !ok {
		return false
	}
	return t.Base == ot.Base
}

func (t *AppliedType) String() string {
	var b strings.Builder
	b.WriteString(t.Base.String())
	b.WriteString("[")
	for i, a := range t.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(a.String())
	}
	b.WriteString("]")
	return b.String()
}

func (t *AppliedType) Format(s fmt.State, c rune) {
	_, _ = fmt.Fprint(s, t.String())
}

// --- dang.Env ---

var _ Env = (*AppliedType)(nil)

func (t *AppliedType) SchemeOf(name string) (*hm.Scheme, bool) {
	s, ok := t.Base.SchemeOf(name)
	if !ok {
		return nil, false
	}
	return t.substituteScheme(s), true
}

func (t *AppliedType) LocalSchemeOf(name string) (*hm.Scheme, bool) {
	s, ok := t.Base.LocalSchemeOf(name)
	if !ok {
		return nil, false
	}
	return t.substituteScheme(s), true
}

func (t *AppliedType) Bindings(visibility Visibility) iter.Seq2[string, *hm.Scheme] {
	return func(yield func(string, *hm.Scheme) bool) {
		for name, s := range t.Base.Bindings(visibility) {
			if !yield(name, t.substituteScheme(s)) {
				return
			}
		}
	}
}

// Add and Remove are not supported on an applied generic type.
// Modifications must go through the Base module.
func (t *AppliedType) Add(name string, s *hm.Scheme) hm.Env {
	panic(fmt.Sprintf("AppliedType.Add(%q) — mutate the base Module, not an applied instance", name))
}

func (t *AppliedType) Remove(name string) hm.Env {
	panic(fmt.Sprintf("AppliedType.Remove(%q) — mutate the base Module, not an applied instance", name))
}

func (t *AppliedType) Clone() hm.Env {
	args := make([]hm.Type, len(t.Args))
	copy(args, t.Args)
	return &AppliedType{Base: t.Base, Args: args}
}

func (t *AppliedType) NamedType(name string) (Env, bool) {
	return t.Base.NamedType(name)
}

func (t *AppliedType) LocalNamedType(name string) (Env, bool) {
	return t.Base.LocalNamedType(name)
}

func (t *AppliedType) NamedTypes() iter.Seq2[string, Env] {
	return t.Base.NamedTypes()
}

func (t *AppliedType) AddClass(name string, c Env) {
	panic(fmt.Sprintf("AppliedType.AddClass(%q)", name))
}

func (t *AppliedType) SetTypeOrigin(name string, origin BindingOrigin) {
	panic(fmt.Sprintf("AppliedType.SetTypeOrigin(%q)", name))
}

func (t *AppliedType) LocalTypeOrigin(name string) (BindingOrigin, bool) {
	return t.Base.LocalTypeOrigin(name)
}

func (t *AppliedType) SetDocString(name, s string) {
	panic(fmt.Sprintf("AppliedType.SetDocString(%q)", name))
}
func (t *AppliedType) GetDocString(name string) (string, bool) {
	return t.Base.GetDocString(name)
}
func (t *AppliedType) SetDirectives(name string, ds []*DirectiveApplication) {
	panic(fmt.Sprintf("AppliedType.SetDirectives(%q)", name))
}
func (t *AppliedType) GetDirectives(name string) []*DirectiveApplication {
	return t.Base.GetDirectives(name)
}
func (t *AppliedType) SetModuleDocString(s string) {
	panic("AppliedType.SetModuleDocString")
}
func (t *AppliedType) GetModuleDocString() string { return t.Base.GetModuleDocString() }

func (t *AppliedType) SetVisibility(name string, v Visibility) {
	panic(fmt.Sprintf("AppliedType.SetVisibility(%q)", name))
}

func (t *AppliedType) SetValueOrigin(name string, origin BindingOrigin) {
	panic(fmt.Sprintf("AppliedType.SetValueOrigin(%q)", name))
}
func (t *AppliedType) LocalValueOrigin(name string) (BindingOrigin, bool) {
	return t.Base.LocalValueOrigin(name)
}
func (t *AppliedType) AddDirective(name string, d *DirectiveDecl) {
	panic(fmt.Sprintf("AppliedType.AddDirective(%q)", name))
}
func (t *AppliedType) GetDirective(name string) (*DirectiveDecl, bool) {
	return t.Base.GetDirective(name)
}
func (t *AppliedType) SetDirectiveOrigin(name string, origin BindingOrigin) {
	panic(fmt.Sprintf("AppliedType.SetDirectiveOrigin(%q)", name))
}
func (t *AppliedType) LocalDirectiveOrigin(name string) (BindingOrigin, bool) {
	return t.Base.LocalDirectiveOrigin(name)
}
func (t *AppliedType) CheckTypeConflict(name string) []string {
	return t.Base.CheckTypeConflict(name)
}
func (t *AppliedType) CheckValueConflict(name string) []string {
	return t.Base.CheckValueConflict(name)
}
func (t *AppliedType) CheckDirectiveConflict(name string) []string {
	return t.Base.CheckDirectiveConflict(name)
}

func (t *AppliedType) GetDynamicScopeType() hm.Type {
	return t.Base.GetDynamicScopeType()
}
func (t *AppliedType) SetDynamicScopeType(dt hm.Type) {
	panic("AppliedType.SetDynamicScopeType")
}
