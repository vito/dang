package dang

import (
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// Interfaces declared in Dang are structural: a value of any object or
// interface type that has an interface's fields is a value of the interface,
// whether or not its type says `implements`, and whichever schema it comes
// from. That is what makes them cross-schema interfaces: `interface Error {
// message: String! }`, the prelude's, takes a Dagger `Error` as readily as
// a Dang type declaring it.
//
// Interfaces a GraphQL schema declares stay nominal, as GraphQL's are: the
// server decides which types implement them, and resolves fragments on them
// by that.
//
// `implements` still means what it did: it is checked where the type is
// declared, which pins the types of the fields it conforms with, and the
// interface becomes a supertype of the type for good. Structural conformance
// is only ever a question, asked where a value meets the interface, and pins
// nothing.

var _ hm.StructuralType = (*Type)(nil)

// isStructural reports whether t is an interface that other types satisfy
// by their shape: one declared in Dang (see above).
func (t *Type) isStructural() bool {
	return t.Kind == InterfaceKind && t.SourceSchema == nil && t.metaOf == nil && t.Canonical == nil
}

// SatisfiedBy implements hm.StructuralType: have, an object or interface
// type, satisfies t, a Dang interface, when it has every field of t,
// public, with a type fitting t's by the rules `implements` checks (see
// validateFieldImplementation), short of pinning anything.
func (t *Type) SatisfiedBy(have hm.Type, fits func(have, want hm.Type) (hm.Subs, error)) bool {
	if !t.isStructural() {
		return false
	}
	mod, ok := have.(*Type)
	if !ok || mod.metaOf != nil || isRecordModule(mod) {
		return false
	}
	if mod.Kind == UnionKind {
		// A union's values are its members', so it has the shape when
		// each of them does. Each is asked about directly: through its
		// supertypes, a member would reach this union again, which the
		// check assumes has the shape.
		//
		// Not a schema's union, though: a handle on one is selected
		// through the union, which has no fields to read until the server
		// says what it holds.
		if mod.SourceSchema != nil {
			return false
		}
		members := mod.GetMembers()
		for _, m := range members {
			member, ok := m.(*Type)
			if !ok || member.Kind != ObjectKind && member.Kind != InterfaceKind {
				return false
			}
			if !member.ImplementsInterface(t) && !conformsStructurally(member, t, fits) {
				return false
			}
		}
		return len(members) > 0
	}
	if mod.Kind != ObjectKind && mod.Kind != InterfaceKind {
		return false
	}
	return conformsStructurally(mod, t, fits)
}

// satisfies reports whether a value of type have is a value of iface:
// have is iface, implements it, or satisfies it structurally.
func satisfies(have, iface *Type) bool {
	return hm.IsSubtypeOf(have, iface)
}

// conformsStructurally reports whether every field of iface is a public
// field of mod whose type fits.
func conformsStructurally(mod, iface *Type, fits func(have, want hm.Type) (hm.Subs, error)) bool {
	return structuralMismatch(mod, iface, fits) == ""
}

// structuralMismatch says why mod doesn't have the shape of iface: the
// first field of iface it lacks, hides, or has a type of that doesn't fit.
// It is empty when mod has the shape.
func structuralMismatch(mod, iface *Type, fits func(have, want hm.Type) (hm.Subs, error)) string {
	for field, ifaceScheme := range iface.Bindings(PrivateVisibility) {
		// Only the type's own members: a name its declaring scope binds is
		// no field of its values. Every field counts, `id` included: Dagger
		// supplies an `id` only for a type that implements a schema's
		// interface, which is never structural.
		modScheme, found := mod.LocalSchemeOf(field)
		if !found {
			return fmt.Sprintf("missing field %q", field)
		}
		if mod.Visibility(field) != PublicVisibility {
			return fmt.Sprintf("field %q is private (`let`), so a value of %s doesn't have it where %s is used", field, mod, iface)
		}
		ifaceType, _ := ifaceScheme.Type()
		modType, _ := modScheme.Type()
		if !fieldFitsStructurally(ifaceType, modType, fits) {
			return fmt.Sprintf("field %q: type %s doesn't fit %s", field, FormatType(modType), FormatType(ifaceType))
		}
	}
	return ""
}

// fieldFitsStructurally reports whether a field of type have stands in for
// an interface field of type want, as validateFieldImplementation decides
// it for a declared implementation: a stored field for a stored field or an
// argumentless method, and a method for a method taking the same arguments,
// as well as optional ones of its own. The interface's type variables are
// rigid, since its callers pick them; the field's own are fresh, and may be
// bound, but nothing else may: the check pins no type.
func fieldFitsStructurally(want, have hm.Type, fits func(have, want hm.Type) (hm.Subs, error)) bool {
	have, own := freshVars(have)
	skolem, _ := skolemSubs(want)
	want = want.Apply(skolem).(hm.Type)

	wantFn, wantIsFn := want.(*hm.FunctionType)
	haveFn, haveIsFn := have.(*hm.FunctionType)
	switch {
	case !wantIsFn && !haveIsFn:
		return fitsOwn(have, want, own, fits)
	case !wantIsFn:
		return false
	case !haveIsFn:
		args, ok := wantFn.Arg().(*RecordType)
		if !ok || len(args.Fields) > 0 || wantFn.Block() != nil {
			return false
		}
		return fitsOwn(have, wantFn.Ret(false), own, fits)
	}

	// Callers through the interface pass its arguments, by name, so the
	// method must take each of them, and nothing else it requires.
	wantArgs, ok := wantFn.Arg().(*RecordType)
	if !ok {
		return false
	}
	haveArgs, ok := haveFn.Arg().(*RecordType)
	if !ok {
		return false
	}
	asCalled := NewRecordType("")
	for _, arg := range wantArgs.Fields {
		scheme, found := haveArgs.SchemeOf(arg.Key)
		if !found {
			return false
		}
		asCalled.Fields = append(asCalled.Fields, Keyed[*hm.Scheme]{Key: arg.Key, Value: scheme})
	}
	for _, arg := range haveArgs.Fields {
		if _, found := wantArgs.SchemeOf(arg.Key); found {
			continue
		}
		argType, _ := arg.Value.Type()
		if _, required := argType.(hm.NonNullType); required {
			return false
		}
	}
	called := hm.NewFnType(asCalled, haveFn.Ret(false))
	wantBlock := wantFn.Block()
	if haveFn.Block() != nil {
		called.SetBlock(haveFn.Block())
	} else {
		// The method ignores any block the interface's callers pass.
		wantBlock = nil
	}
	callers := hm.NewFnType(wantArgs, wantFn.Ret(false))
	if wantBlock != nil {
		callers.SetBlock(wantBlock)
	}
	return fitsOwn(called, callers, own, fits)
}

// fitsOwn asks fits whether have fits want, and accepts the answer only
// if it binds no type variable besides own, the ones fieldFitsStructurally
// freshened: binding any other would make a type of the program's what this
// check needs it to be, and this check pins nothing.
func fitsOwn(have, want hm.Type, own hm.TypeVarSet, fits func(have, want hm.Type) (hm.Subs, error)) bool {
	subs, err := fits(have, want)
	if err != nil {
		return false
	}
	for tv := range subs {
		if !own.Contains(tv) {
			return false
		}
	}
	return true
}

// freshVars gives t's flexible type variables new identities, as
// freshenTypeVars does, and returns them.
func freshVars(t hm.Type) (hm.Type, hm.TypeVarSet) {
	subs := hm.NewSubs()
	own := hm.NewTypeVarSet()
	for tv := range t.FreeTypeVar() {
		if tv.IsRigid() || tv.IsWeak() {
			continue
		}
		fresh := hm.Freshen(tv)
		subs.Add(tv, fresh)
		own.Add(fresh)
	}
	if len(subs) == 0 {
		return t, own
	}
	return t.Apply(subs).(hm.Type), own
}
