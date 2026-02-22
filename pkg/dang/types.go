package dang

import (
	"context"
	"fmt"
	"maps"

	"github.com/vito/dang/pkg/hm"
)

type Type = hm.Type

type TypeNode interface {
	hm.Inferer
	ReferencedSymbols() []string
}

type NamedTypeNode struct {
	Base *NamedTypeNode
	Name string
	Loc  *SourceLocation
}

var _ TypeNode = (*NamedTypeNode)(nil)

func (t *NamedTypeNode) ReferencedSymbols() []string {
	if t.Name == "" {
		return nil
	}
	return []string{t.Name}
}

type UnresolvedTypeError struct {
	Name string
}

func (e UnresolvedTypeError) Error() string {
	return fmt.Sprintf("unresolved type: %s", e.Name)
}

func (t *NamedTypeNode) GetSourceLocation() *SourceLocation {
	return t.Loc
}

func (t *NamedTypeNode) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(t, func() (hm.Type, error) {
		if t.Name == "" {
			return nil, fmt.Errorf("NamedType.Infer: empty name")
		}

		fromEnv := env.(Env)
		if t.Base != nil {
			base, err := t.Base.Infer(ctx, env, fresh)
			if err != nil {
				return nil, fmt.Errorf("NamedType.Infer: base type: %w", err)
			}
			fromEnv = base.(Env)
		}

		s, found := fromEnv.NamedType(t.Name)
		if !found {
			return nil, UnresolvedTypeError{t.Name}
		}
		return s, nil
	})
}

type ListTypeNode struct {
	Elem TypeNode
}

var _ TypeNode = ListTypeNode{}

func (t ListTypeNode) ReferencedSymbols() []string {
	return t.Elem.ReferencedSymbols()
}

func (t ListTypeNode) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	e, err := t.Elem.Infer(ctx, env, fresh)
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

func (t ListType) String() string {
	return fmt.Sprintf("[%s]", t.Type)
}

func (t ListType) Format(s fmt.State, c rune) {
	_, _ = fmt.Fprintf(s, "[%"+string(c)+"]", t.Type)
}

func (t ListType) Eq(other Type) bool {
	if ot, ok := other.(ListType); ok {
		return t.Type.Eq(ot.Type)
	}
	return false
}

func (t ListType) Supertypes() []Type {
	innerSupers := t.Type.Supertypes()
	for i, t := range innerSupers {
		// Generalize into list type for each supertype
		innerSupers[i] = ListType{t}
	}
	return innerSupers
}

// GraphQLListType represents a list returned from GraphQL that contains objects.
// Unlike ListType, it cannot be directly iterated - it must first be converted
// to a ListType via object selection (.{fields}) to avoid N+1 query problems.
type GraphQLListType struct {
	Type
}

var _ hm.Type = GraphQLListType{}

func (t GraphQLListType) Name() string {
	return fmt.Sprintf("GraphQL[%s]", t.Type.Name())
}

func (t GraphQLListType) Apply(subs hm.Subs) hm.Substitutable {
	return GraphQLListType{t.Type.Apply(subs).(hm.Type)}
}

func (t GraphQLListType) Normalize(k, v hm.TypeVarSet) (Type, error) {
	normalized, err := t.Type.Normalize(k, v)
	if err != nil {
		return nil, err
	}
	return GraphQLListType{normalized}, nil
}

func (t GraphQLListType) Types() hm.Types {
	ts := hm.BorrowTypes(1)
	ts[0] = t.Type
	return ts
}

func (t GraphQLListType) String() string {
	return fmt.Sprintf("GraphQL[%s]", t.Type)
}

func (t GraphQLListType) Format(s fmt.State, c rune) {
	_, _ = fmt.Fprintf(s, "GraphQL[%"+string(c)+"]", t.Type)
}

func (t GraphQLListType) Eq(other Type) bool {
	if ot, ok := other.(GraphQLListType); ok {
		return t.Type.Eq(ot.Type)
	}
	return false
}

func (t GraphQLListType) Supertypes() []Type {
	return nil
}

type RecordType struct {
	Named      string
	Fields     []Keyed[*hm.Scheme]
	Directives []Keyed[[]*DirectiveApplication]
	DocStrings map[string]string // Maps field names to their doc strings
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
	retVal.Directives = t.Directives
	// Clone doc strings map
	if t.DocStrings != nil {
		retVal.DocStrings = make(map[string]string, len(t.DocStrings))
		maps.Copy(retVal.DocStrings, t.DocStrings)
	}
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
	dup := NewRecordType(t.Named, fields...)
	dup.Directives = t.Directives
	dup.DocStrings = t.DocStrings
	return dup
}

func (t *RecordType) FreeTypeVar() hm.TypeVarSet {
	var tvs hm.TypeVarSet
	for _, v := range t.Fields {
		tvs = v.Value.FreeTypeVar().Union(tvs)
	}
	return tvs
}

func (t *RecordType) GetDynamicScopeType() hm.Type {
	return nil // RecordTypes don't have dynamic scope
}

func (t *RecordType) SetDynamicScopeType(hm.Type) {
	// RecordTypes don't have dynamic scope - noop
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
	// Count monomorphic types first
	count := 0
	for _, f := range t.Fields {
		_, mono := f.Value.Type()
		if mono {
			count++
		}
	}

	ts := hm.BorrowTypes(count)
	index := 0
	for _, f := range t.Fields {
		typ, mono := f.Value.Type()
		if !mono {
			// TODO maybe omit? For now, skip non-monomorphic types
			continue
		}
		ts[index] = typ
		index++
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

func (t *RecordType) Supertypes() []Type {
	return nil
}

func (t *RecordType) Format(f fmt.State, c rune) {
	if t.Named != "" {
		_, _ = fmt.Fprint(f, t.Named)
	}
	_, _ = f.Write([]byte("{"))
	for i, v := range t.Fields {
		_, _ = fmt.Fprintf(f, "%s: %v", v.Key, v.Value)
		if i < len(t.Fields)-1 {
			_, _ = fmt.Fprintf(f, ", ")
		}
	}
	_, _ = f.Write([]byte("}"))
}

func (t *RecordType) String() string { return fmt.Sprintf("%v", t) }

type NonNullTypeNode struct {
	Elem TypeNode
}

var _ TypeNode = NonNullTypeNode{}

func (t NonNullTypeNode) ReferencedSymbols() []string {
	return t.Elem.ReferencedSymbols()
}

func (t NonNullTypeNode) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	e, err := t.Elem.Infer(ctx, env, fresh)
	if err != nil {
		return nil, fmt.Errorf("NonNullType.Infer: %w", err)
	}
	return hm.NonNullType{Type: e}, nil
}

type VariableTypeNode struct {
	Name byte
}

var _ TypeNode = VariableTypeNode{}

func (t VariableTypeNode) ReferencedSymbols() []string {
	return nil // Type variables don't reference other symbols
}

func (t VariableTypeNode) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return hm.TypeVariable(t.Name), nil
}

type ObjectTypeField struct {
	Key  string
	Type TypeNode
}

type ObjectTypeNode struct {
	Fields []ObjectTypeField
}

var _ TypeNode = ObjectTypeNode{}

func (t ObjectTypeNode) ReferencedSymbols() []string {
	var symbols []string
	for _, field := range t.Fields {
		symbols = append(symbols, field.Type.ReferencedSymbols()...)
	}
	return symbols
}

func (t ObjectTypeNode) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod := NewModule("", ObjectKind)
	for _, field := range t.Fields {
		fieldType, err := field.Type.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("ObjectTypeNode.Infer: field %q: %w", field.Key, err)
		}
		scheme := hm.NewScheme(nil, fieldType)
		mod.Add(field.Key, scheme)
	}
	return mod, nil
}

type FunTypeNode struct {
	Args []*SlotDecl
	Ret  TypeNode
}

var _ TypeNode = FunTypeNode{}

func (t FunTypeNode) ReferencedSymbols() []string {
	var symbols []string
	for _, arg := range t.Args {
		if arg.Type_ != nil {
			symbols = append(symbols, arg.Type_.ReferencedSymbols()...)
		}
		if arg.Value != nil {
			symbols = append(symbols, arg.Value.ReferencedSymbols()...)
		}
	}
	if t.Ret != nil {
		symbols = append(symbols, t.Ret.ReferencedSymbols()...)
	}
	return symbols
}

func (t FunTypeNode) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var regularArgs []Keyed[*hm.Scheme]
	var directives []Keyed[[]*DirectiveApplication]
	var docStrings = make(map[string]string)
	var blockType *hm.FunctionType

	for _, a := range t.Args {
		if a.IsBlockParam {
			// This is a block parameter - infer it as a function type
			blockFnType, err := a.Type_.Infer(ctx, env, fresh)
			if err != nil {
				return nil, fmt.Errorf("FunTypeNode.Infer: block parameter: %w", err)
			}
			bt, ok := blockFnType.(*hm.FunctionType)
			if !ok {
				return nil, fmt.Errorf("FunTypeNode.Infer: block parameter must be a function type, got %T", blockFnType)
			}
			blockType = bt
		} else {
			// Regular argument
			dt, err := a.Type_.Infer(ctx, env, fresh)
			if err != nil {
				return nil, fmt.Errorf("FunTypeNode.Infer: %w", err)
			}

			// Apply the same nullable transformation as in inferFunctionArguments
			// For arguments with defaults, make them nullable in the function signature
			signatureType := dt
			if a.Value != nil {
				// Argument has a default value - make it nullable in the function signature
				if nonNullType, isNonNull := dt.(hm.NonNullType); isNonNull {
					signatureType = nonNullType.Type
				}
			}

			regularArgs = append(regularArgs, Keyed[*hm.Scheme]{
				Key:   a.Name.Name,
				Value: hm.NewScheme(nil, signatureType),
			})
			if len(a.Directives) > 0 {
				directives = append(directives, Keyed[[]*DirectiveApplication]{
					Key:   a.Name.Name,
					Value: a.Directives,
				})
			}
			// Capture doc string if present
			if a.DocString != "" {
				docStrings[a.Name.Name] = a.DocString
			}
		}
	}

	ret, err := t.Ret.Infer(ctx, env, fresh)
	if err != nil {
		return nil, fmt.Errorf("FunTypeNode.Infer: %w", err)
	}

	// Include directives and doc strings in args type
	argsRec := NewRecordType("", regularArgs...)
	argsRec.Directives = directives
	argsRec.DocStrings = docStrings

	// Create function type with optional block parameter
	fnType := hm.NewFnType(argsRec, ret)
	if blockType != nil {
		fnType.SetBlock(blockType)
	}
	return fnType, nil
}

// not needed yet
//
// type RecordTypeNode struct {
// 	Named  string
// 	Fields []SlotDecl
// }

// var _ TypeNode = RecordTypeNode{}

// func (t RecordTypeNode) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
// 	fields := make([]Keyed[*hm.Scheme], len(t.Fields))
// 	for i, f := range t.Fields {
// 		dt, err := f.Type_.Infer(ctx, env, fresh)
// 		if err != nil {
// 			return nil, fmt.Errorf("RecordType.Infer: %w", err)
// 		}
// 		// TODO: more scheme/type awkwardness, double check this
// 		// TODO: should we infer from value?
// 		fields[i] = Keyed[*hm.Scheme]{Key: f.Named, Value: hm.NewScheme(nil, dt)}
// 	}
// 	return NewRecordType(t.Named, fields...), nil
// }
