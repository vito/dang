package dash

import (
	"fmt"

	"github.com/chewxy/hm"
)

type Type = hm.Type

// SubtypingType extends hm.Type with subtyping relationships
type SubtypingType interface {
	hm.Type
	// IsSubtypeOf returns true if this type can be used where other is expected
	IsSubtypeOf(other hm.Type) bool
}

type TypeNode interface {
	hm.Inferer
}

// UnifyWithCompatibility unifies two types, considering subtyping relationships
// This is the main entry point for all type unification in Dash
func UnifyWithCompatibility(expected, provided hm.Type) (hm.Subs, error) {
	// Try direct structural unification first (preserves HM semantics)
	if subs, err := hm.Unify(expected, provided); err == nil {
		return subs, nil
	}

	// Check subtyping compatibility
	if isSubtypeCompatible(expected, provided) {
		return nil, nil // Empty substitution
	}

	// Neither worked, return original unification error for better error messages
	return hm.Unify(expected, provided)
}

// isSubtypeCompatible checks if provided type can be used where expected type is required
func isSubtypeCompatible(expected, provided hm.Type) bool {
	// Check if provided type implements subtyping and is subtype of expected
	if providedST, ok := provided.(SubtypingType); ok {
		if providedST.IsSubtypeOf(expected) {
			return true
		}
	}

	// Check if expected type implements subtyping and provided is its supertype
	// (This handles cases where expected is more specific than provided, which should fail)
	if expectedST, ok := expected.(SubtypingType); ok {
		if expectedST.IsSubtypeOf(provided) {
			return false // Don't allow supertype -> subtype
		}
	}

	return false
}

// TODO: support sub-selections?

type NamedTypeNode struct {
	Named string
}

var _ TypeNode = NamedTypeNode{}

type UnresolvedTypeError struct {
	Name string
}

func (e UnresolvedTypeError) Error() string {
	return fmt.Sprintf("unresolved type: %s", e.Name)
}

func (t NamedTypeNode) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	if t.Named == "" {
		return nil, fmt.Errorf("NamedType.Infer: empty name")
	}

	if lookup, ok := env.(Env); ok {
		s, found := lookup.NamedType(t.Named)
		if !found {
			return nil, UnresolvedTypeError{t.Named}
		}
		return s, nil
	}

	return nil, fmt.Errorf("NamedTypeNode.Infer: environment does not support NamedType lookup")
}

type ListTypeNode struct {
	Elem TypeNode
}

var _ TypeNode = ListTypeNode{}

func (t ListTypeNode) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	e, err := t.Elem.Infer(env, fresh)
	if err != nil {
		return nil, fmt.Errorf("ListType.Infer: %w", err)
	}
	return ListType{e}, nil
}

type ListType struct {
	Type
}

var _ hm.Type = ListType{}

func (t ListType) Name() string {
	return fmt.Sprintf("[%s]", t.Type.Name())
}

func (t ListType) Apply(subs hm.Subs) hm.Substitutable {
	return ListType{t.Type.Apply(subs).(hm.Type)}
}

func (t ListType) Normalize(k, v hm.TypeVarSet) (Type, error) {
	normalized, err := t.Type.Normalize(k, v)
	if err != nil {
		return nil, err
	}
	return ListType{normalized}, nil
}

func (t ListType) Types() hm.Types {
	ts := hm.BorrowTypes(1)
	ts[0] = t.Type
	return ts
}

// IsSubtypeOf implements SubtypingType
// ListType doesn't have direct subtyping, but its elements might
func (t ListType) IsSubtypeOf(other hm.Type) bool {
	// List types are only subtypes if they're identical
	// The subtyping happens at the NonNull wrapper level
	return t.Eq(other)
}

func (t ListType) String() string {
	return fmt.Sprintf("[%s]", t.Type)
}

func (t ListType) Format(s fmt.State, c rune) {
	fmt.Fprintf(s, "[%"+string(c)+"]", t.Type)
}

func (t ListType) Eq(other Type) bool {
	if ot, ok := other.(ListType); ok {
		return t.Type.Eq(ot.Type)
	}
	return false
}

type RecordType struct {
	Named  string
	Fields []Keyed[*hm.Scheme] // TODO this should be a map
}

var _ hm.Type = (*RecordType)(nil)

// NewRecordType creates a new Record Type
func NewRecordType(name string, fields ...Keyed[*hm.Scheme]) *RecordType {
	return &RecordType{
		Named:  name,
		Fields: fields,
	}
}

var _ hm.Env = (*RecordType)(nil)

func (t *RecordType) SchemeOf(key string) (*hm.Scheme, bool) {
	for _, f := range t.Fields {
		if f.Key == key {
			return f.Value, true
		}
	}
	return nil, false
}

func (t *RecordType) Clone() hm.Env {
	retVal := new(RecordType)
	ts := make([]Keyed[*hm.Scheme], len(t.Fields))
	for i, tt := range t.Fields {
		ts[i] = tt
		ts[i].Value = ts[i].Value.Clone()
	}
	retVal.Fields = ts
	return retVal
}

func (t *RecordType) Add(key string, type_ *hm.Scheme) hm.Env {
	t.Fields = append(t.Fields, Keyed[*hm.Scheme]{Key: key, Value: type_})
	return t
}

func (t *RecordType) Remove(key string) hm.Env {
	for i, f := range t.Fields {
		if f.Key == key {
			t.Fields = append(t.Fields[:i], t.Fields[i+1:]...)
		}
	}
	return t
}

func (t *RecordType) Apply(subs hm.Subs) hm.Substitutable {
	fields := make([]Keyed[*hm.Scheme], len(t.Fields))
	for i, v := range t.Fields {
		fields[i] = v
		fields[i].Value = v.Value.Apply(subs).(*hm.Scheme)
	}
	return NewRecordType(t.Named, fields...)
}

func (t *RecordType) FreeTypeVar() hm.TypeVarSet {
	var tvs hm.TypeVarSet
	for _, v := range t.Fields {
		tvs = v.Value.FreeTypeVar().Union(tvs)
	}
	return tvs
}

func (t *RecordType) Name() string {
	if t.Named != "" {
		return t.Named
	}
	return t.String()
}

func (t *RecordType) Normalize(k, v hm.TypeVarSet) (Type, error) {
	cp := t.Clone().(*RecordType)
	for _, f := range cp.Fields {
		if err := f.Value.Normalize(); err != nil {
			return nil, fmt.Errorf("RecordType.Normalize: %w", err)
		}
	}
	return cp, nil
}

func (t *RecordType) Types() hm.Types {
	ts := hm.BorrowTypes(len(t.Fields))
	for _, f := range t.Fields {
		typ, mono := f.Value.Type()
		if !mono {
			// TODO maybe omit? For now, skip non-monomorphic types
			continue
		}
		ts = append(ts, typ)
	}
	return ts
}

func (t *RecordType) Eq(other Type) bool {
	if ot, ok := other.(*RecordType); ok {
		if len(ot.Fields) != len(t.Fields) {
			return false
		}
		if t.Named != "" && ot.Named != "" && t.Named != ot.Named {
			// if either does not specify a name, allow a match
			//
			// either the client is wanting to duck type instead, or the API is
			// wanting to be generic
			//
			// TDOO: not sure if Eq is the right place for this
			return false
		}
		for i, f := range t.Fields {
			of := ot.Fields[i]
			if f.Key != of.Key {
				return false
			}
			// TODO
			ft, _ := f.Value.Type()
			oft, _ := of.Value.Type()
			if !ft.Eq(oft) {
				return false
			}
		}
		return true
	}
	return false
}

func (t *RecordType) Format(f fmt.State, c rune) {
	if t.Named != "" {
		fmt.Fprint(f, t.Named)
	}
	f.Write([]byte("{"))
	for i, v := range t.Fields {
		fmt.Fprintf(f, "%s: %v", v.Key, v.Value)
		if i < len(t.Fields)-1 {
			fmt.Fprintf(f, ", ")
		}
	}
	f.Write([]byte("}"))
}

func (t *RecordType) String() string { return fmt.Sprintf("%v", t) }

type NonNullTypeNode struct {
	Elem TypeNode
}

var _ TypeNode = NonNullTypeNode{}

func (t NonNullTypeNode) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	e, err := t.Elem.Infer(env, fresh)
	if err != nil {
		return nil, fmt.Errorf("NonNullType.Infer: %w", err)
	}
	return NonNullType{e}, nil
}

type VariableTypeNode struct {
	Name byte
}

var _ TypeNode = VariableTypeNode{}

func (t VariableTypeNode) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// TODO unsure if this works
	return hm.TypeVariable(t.Name), nil
}

type NonNullType struct {
	Type
}

var _ hm.Type = NonNullType{}

func (t NonNullType) Name() string {
	return fmt.Sprintf("%s!", t.Type.Name())
}

func (t NonNullType) Apply(subs hm.Subs) hm.Substitutable {
	return NonNullType{t.Type.Apply(subs).(hm.Type)}
}

func (t NonNullType) Normalize(k, v hm.TypeVarSet) (Type, error) {
	normalized, err := t.Type.Normalize(k, v)
	if err != nil {
		return nil, err
	}
	return NonNullType{normalized}, nil
}

func (t NonNullType) Types() hm.Types {
	ts := hm.BorrowTypes(1)
	ts[0] = t.Type
	return ts
}

// IsSubtypeOf implements SubtypingType
// NonNull T is a subtype of nullable T
func (t NonNullType) IsSubtypeOf(other hm.Type) bool {
	// NonNull T <: T (non-null is subtype of nullable)
	return t.Type.Eq(other)
}

func (t NonNullType) String() string {
	return fmt.Sprintf("%s!", t.Type)
}

func (t NonNullType) Format(s fmt.State, c rune) {
	fmt.Fprintf(s, "%"+string(c)+"!", t.Type)
}

func (t NonNullType) Eq(other Type) bool {
	if ot, ok := other.(NonNullType); ok {
		return t.Type.Eq(ot.Type)
	}
	return false
}

type FunTypeNode struct {
	Args []SlotDecl
	Ret  TypeNode
}

var _ TypeNode = FunTypeNode{}

func (t FunTypeNode) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	args := make([]Keyed[*hm.Scheme], len(t.Args))
	for i, a := range t.Args {
		// TODO: more scheme/type awkwardness, double check this
		// scheme, err := Infer(env, a.Value)
		// if err != nil {
		// 	return nil, fmt.Errorf("FunType.Infer: %w", err)
		// }
		dt, err := a.Type_.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("FunTypeNode.Infer: %w", err)
		}
		// TODO: should we infer from value?
		args[i] = Keyed[*hm.Scheme]{Key: a.Named, Value: hm.NewScheme(nil, dt)}
	}
	ret, err := t.Ret.Infer(env, fresh)
	if err != nil {
		return nil, fmt.Errorf("FunTypeNode.Infer: %w", err)
	}
	return hm.NewFnType(NewRecordType("", args...), ret), nil
}

// not needed yet
//
// type RecordTypeNode struct {
// 	Named  string
// 	Fields []SlotDecl
// }

// var _ TypeNode = RecordTypeNode{}

// func (t RecordTypeNode) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
// 	fields := make([]Keyed[*hm.Scheme], len(t.Fields))
// 	for i, f := range t.Fields {
// 		dt, err := f.Type_.Infer(env, fresh)
// 		if err != nil {
// 			return nil, fmt.Errorf("RecordType.Infer: %w", err)
// 		}
// 		// TODO: more scheme/type awkwardness, double check this
// 		// TODO: should we infer from value?
// 		fields[i] = Keyed[*hm.Scheme]{Key: f.Named, Value: hm.NewScheme(nil, dt)}
// 	}
// 	return NewRecordType(t.Named, fields...), nil
// }
