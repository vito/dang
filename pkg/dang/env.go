package dang

import (
	"fmt"
	"iter"
	"log/slog"
	"sort"
	"strings"

	"github.com/vito/dang/introspection"
	"github.com/vito/dang/pkg/hm"
)

type Env interface {
	hm.Env
	hm.Type
	NamedType(string) (Env, bool)
	AddClass(string, Env)
	SetDocString(string, string)
	GetDocString(string) (string, bool)
	SetModuleDocString(string)
	GetModuleDocString() string
	SetVisibility(string, Visibility)
	LocalSchemeOf(string) (*hm.Scheme, bool)
	AddDirective(string, *DirectiveDecl)
	GetDirective(string) (*DirectiveDecl, bool)
	Bindings(visibility Visibility) iter.Seq2[string, *hm.Scheme]
}

// TODO: is this just ClassType? are Classes just named Envs?
type Module struct {
	Named string

	Parent Env

	classes         map[string]Env
	vars            map[string]*hm.Scheme
	visibility      map[string]Visibility
	directives      map[string]*DirectiveDecl
	docStrings      map[string]string
	moduleDocString string
}

func NewModule(name string) *Module {
	env := &Module{
		Named:           name,
		classes:         make(map[string]Env),
		vars:            make(map[string]*hm.Scheme),
		visibility:      make(map[string]Visibility),
		directives:      make(map[string]*DirectiveDecl),
		docStrings:      make(map[string]string),
		moduleDocString: "",
	}
	return env
}

func gqlToTypeNode(mod Env, ref *introspection.TypeRef) (hm.Type, error) {
	switch ref.Kind {
	case introspection.TypeKindList:
		inner, err := gqlToTypeNode(mod, ref.OfType)
		if err != nil {
			return nil, fmt.Errorf("gqlToTypeNode List: %w", err)
		}
		// Lists of objects use GraphQLListType (not directly iterable)
		// Lists of scalars use regular ListType (iterable)
		if ref.OfType.IsObject() {
			return GraphQLListType{inner}, nil
		}
		return ListType{inner}, nil
	case introspection.TypeKindNonNull:
		inner, err := gqlToTypeNode(mod, ref.OfType)
		if err != nil {
			return nil, fmt.Errorf("gqlToTypeNode NonNull: %w", err)
		}
		return hm.NonNullType{Type: inner}, nil
	case introspection.TypeKindScalar:
		if strings.HasSuffix(ref.Name, "ID") && ref.Name != "ID" {
			return gqlToTypeNode(mod, &introspection.TypeRef{
				Name: strings.TrimSuffix(ref.Name, "ID"),
				Kind: introspection.TypeKindObject,
			})
		}
		fallthrough
	default:
		t, found := mod.NamedType(ref.Name)
		if !found {
			return nil, fmt.Errorf("gqlToTypeNode: %s %q not found", ref.Kind, ref.Name)
		}
		return t, nil
	}
}

var Prelude *Module

func init() {
	Prelude = NewModule("Prelude")

	// Install built-in types
	Prelude.AddClass("ID", IDType)
	Prelude.AddClass("String", StringType)
	Prelude.AddClass("Int", IntType)
	Prelude.AddClass("Float", FloatType)
	Prelude.AddClass("Boolean", BooleanType)

	// Register standard library builtins
	registerStdlib()

	// Register builtin function types from the registry
	registerBuiltinTypes()
}

func NewEnv(schema *introspection.Schema) Env {
	mod := NewModule("")
	env := &CompositeModule{mod, Prelude}

	for _, t := range schema.Directives {
		var args []*SlotDecl
		for _, arg := range t.Args {
			args = append(args, &SlotDecl{
				Name: &Symbol{
					Name: arg.Name,
				},
				Type_:     introspectionTypeRefToTypeNode(arg.TypeRef),
				DocString: arg.Description,
			})
		}
		var locations []DirectiveLocation
		for _, loc := range t.Locations {
			locations = append(locations, DirectiveLocation{Name: loc})
		}
		directive := &DirectiveDecl{
			Name:      t.Name,
			Args:      args,
			Locations: locations,
			DocString: t.Description,
		}
		mod.AddDirective(t.Name, directive)
	}

	for _, t := range schema.Types {
		sub, found := env.NamedType(t.Name)
		if !found {
			sub = NewModule(t.Name)
			// Store type description as module documentation
			if t.Description != "" {
				sub.SetModuleDocString(t.Description)
			}
			env.AddClass(t.Name, sub)
		}
		if t.Name == schema.QueryType.Name {
			env.lexical = &CompositeModule{sub, env.lexical}
		}
	}

	// Make enum types available as values in the module
	for _, t := range schema.Types {
		if t.Kind == introspection.TypeKindEnum {
			sub, found := env.NamedType(t.Name)
			if found {
				// Add the enum type as a scheme that represents the module itself
				mod.Add(t.Name, hm.NewScheme(nil, sub))
				mod.SetVisibility(t.Name, PublicVisibility)
			}
		}
	}

	for _, t := range schema.Types {
		install, found := env.NamedType(t.Name)
		if !found {
			// we just set it above...
			// This should never happen, but handle gracefully
			continue
		}

		// TODO assign input fields, maybe input classes are "just" records?
		//t.InputFields

		// Assign enum values as string fields for enum types
		if t.Kind == introspection.TypeKindEnum {
			for _, enumVal := range t.EnumValues {
				slog.Debug("adding enum value", "type", t.Name, "value", enumVal.Name)
				// Enum values are represented with the enum type itself
				install.Add(enumVal.Name, hm.NewScheme(nil, install))
				// Enum values are public by default
				install.SetVisibility(enumVal.Name, PublicVisibility)
				// Store enum value description as documentation
				if enumVal.Description != "" {
					install.SetDocString(enumVal.Name, enumVal.Description)
				}
			}
		}

		for _, f := range t.Fields {
			ret, err := gqlToTypeNode(env, f.TypeRef)
			if err != nil {
				panic(err)
			}

			args := NewRecordType("")
			for _, arg := range f.Args {
				argType, err := gqlToTypeNode(env, arg.TypeRef)
				if err != nil {
					panic(err)
				}
				if arg.DefaultValue != nil {
					// If an argument has a default, make sure it's nullable in the
					// function signature.
					t, ok := argType.(hm.NonNullType)
					if ok {
						argType = t.Type
					}
				}
				args.Add(arg.Name, hm.NewScheme(nil, argType))
			}
			slog.Debug("adding function binding", "type", t.Name, "function", f.Name)
			install.Add(f.Name, hm.NewScheme(nil, hm.NewFnType(args, ret)))
			// GraphQL schema fields are public by default
			install.SetVisibility(f.Name, PublicVisibility)
			// Store field description as documentation
			if f.Description != "" {
				install.SetDocString(f.Name, f.Description)
			}
		}
	}

	return env
}

func introspectionTypeRefToTypeNode(ref *introspection.TypeRef) TypeNode {
	switch ref.Kind {
	case introspection.TypeKindList:
		return ListTypeNode{
			Elem: introspectionTypeRefToTypeNode(ref.OfType),
		}
	case introspection.TypeKindNonNull:
		return NonNullTypeNode{
			Elem: introspectionTypeRefToTypeNode(ref.OfType),
		}
	default:
		name := ref.Name
		if ref.Name == "" {
			name = "-INVALID_NAME_MISSING-"
		}
		return &NamedTypeNode{
			Named: name,
		}
	}
}

func (e *Module) Bindings(visibility Visibility) iter.Seq2[string, *hm.Scheme] {
	return func(yield func(string, *hm.Scheme) bool) {
		for name, v := range e.vars {
			if e.visibility[name] >= visibility {
				if !yield(name, v) {
					break
				}
			}
		}
	}
}

var _ hm.Substitutable = (*Module)(nil)

func (e *Module) Apply(subs hm.Subs) hm.Substitutable {
	if len(subs) == 0 || len(e.FreeTypeVar()) == 0 {
		return e
	}
	retVal := e.Clone().(*Module)
	for _, v := range retVal.vars {
		v.Apply(subs)
	}
	return retVal
}

func (e *Module) FreeTypeVar() hm.TypeVarSet {
	var retVal hm.TypeVarSet
	// for _, v := range e.vars {
	// 	retVal = v.FreeTypeVar().Union(retVal)
	// }
	return retVal
}

func (e *Module) Add(name string, s *hm.Scheme) hm.Env {
	e.vars[name] = s
	if _, ok := e.visibility[name]; !ok {
		e.visibility[name] = PrivateVisibility
	}
	return e
}

func (e *Module) SetVisibility(name string, visibility Visibility) {
	e.visibility[name] = visibility
}

func (e *Module) SchemeOf(name string) (*hm.Scheme, bool) {
	s, ok := e.vars[name]
	if ok {
		return s, ok
	}
	if e.Parent != nil {
		return e.Parent.SchemeOf(name)
	}
	return nil, false
}

func (e *Module) LocalSchemeOf(name string) (*hm.Scheme, bool) {
	s, ok := e.vars[name]
	return s, ok
}

func (e *Module) Clone() hm.Env {
	mod := NewModule(e.Named)
	mod.Parent = e
	return mod
}

func (e *Module) AddClass(name string, c Env) {
	e.classes[name] = c
}

func (e *Module) AddDirective(name string, directive *DirectiveDecl) {
	e.directives[name] = directive
}

func (e *Module) GetDirective(name string) (*DirectiveDecl, bool) {
	directive, ok := e.directives[name]
	if ok {
		return directive, ok
	}
	if e.Parent != nil {
		return e.Parent.GetDirective(name)
	}
	return nil, false
}

func (e *Module) NamedType(name string) (Env, bool) {
	t, ok := e.classes[name]
	if ok {
		return t, ok
	}
	if e.Parent != nil {
		return e.Parent.NamedType(name)
	}
	return nil, false
}

func (e *Module) Remove(name string) hm.Env {
	// TODO: lol, tombstone???? idk if i ever use this method. maybe i don't need
	// to conform to hm.Env?
	delete(e.vars, name)
	return e
}

// SetDocString sets the documentation string for a symbol
func (e *Module) SetDocString(name string, docString string) {
	e.docStrings[name] = docString
}

// GetDocString gets the documentation string for a symbol
func (e *Module) GetDocString(name string) (string, bool) {
	if docString, ok := e.docStrings[name]; ok {
		return docString, true
	}
	if e.Parent != nil {
		if parent, ok := e.Parent.(*Module); ok {
			return parent.GetDocString(name)
		}
	}
	return "", false
}

// registerBuiltinTypes registers types for all builtins in the Prelude
func registerBuiltinTypes() {
	// Register all builtin function types
	ForEachFunction(func(def BuiltinDef) {
		fnType := createFunctionTypeFromDef(def)
		slog.Debug("adding builtin function", "function", def.Name)
		Prelude.Add(def.Name, hm.NewScheme(nil, fnType))
	})

	// Register all builtin method types
	for _, receiverType := range []*Module{StringType, IntType, FloatType, BooleanType} {
		ForEachMethod(receiverType, func(def BuiltinDef) {
			fnType := createFunctionTypeFromDef(def)
			slog.Debug("adding builtin method", "type", receiverType.Named, "method", def.Name)
			receiverType.Add(def.Name, hm.NewScheme(nil, fnType))
		})
	}
}

// createFunctionTypeFromDef creates a FunctionType from a BuiltinDef
func createFunctionTypeFromDef(def BuiltinDef) *hm.FunctionType {
	args := NewRecordType("")
	for _, param := range def.ParamTypes {
		args.Add(param.Name, hm.NewScheme(nil, param.Type))
	}
	return hm.NewFnType(args, def.ReturnType)
}

// SetModuleDocString sets the documentation string for the module itself
func (e *Module) SetModuleDocString(docString string) {
	e.moduleDocString = docString
}

// GetModuleDocString gets the documentation string for the module itself
func (e *Module) GetModuleDocString() string {
	return e.moduleDocString
}

func (e *Module) AsRecord() *RecordType {
	var rec RecordType
	for name, scheme := range e.vars {
		rec.Fields = append(rec.Fields, Keyed[*hm.Scheme]{
			Key:   name,
			Value: scheme,
		})
	}
	sort.Slice(rec.Fields, func(i, j int) bool {
		return rec.Fields[i].Key < rec.Fields[j].Key
	})
	return &rec
}

var _ hm.Type = (*Module)(nil)

func (t *Module) Name() string                               { return t.Named }
func (t *Module) Normalize(k, v hm.TypeVarSet) (Type, error) { return t, nil }
func (t *Module) Types() hm.Types                            { return nil }

func (t *Module) String() string {
	if t.Named != "" {
		return t.Named
	}
	return t.AsRecord().String()
}

//	func (t *Module) Format(s fmt.State, c rune) {
//		switch c {
//		case 'v':
//			fmt.Fprintf(s, "%+v", t.)
//		case 's':
//			fmt.Fprintf(s, "%s", t.String())
//		default:
//			fmt.Fprintf(s, "%#v", t)
//		}
//	}
func (t *Module) Eq(other Type) bool {
	otherMod, ok := other.(*Module)
	if !ok {
		return false
	}
	if t.Named != "" {
		return t == otherMod
	}
	return t.AsRecord().Eq(otherMod.AsRecord())
}
