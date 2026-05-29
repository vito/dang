package dang

import (
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"sort"
	"strings"

	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
)

type BindingOriginKind int

const (
	BindingOriginUnknown BindingOriginKind = iota
	BindingOriginLocal
	BindingOriginImport
)

type BindingOrigin struct {
	Kind        BindingOriginKind
	ImportNames []string
	Qualified   bool
}

func LocalBindingOrigin() BindingOrigin {
	return BindingOrigin{Kind: BindingOriginLocal}
}

func ImportedBindingOrigin(importName string, qualified bool) BindingOrigin {
	return BindingOrigin{Kind: BindingOriginImport, ImportNames: []string{importName}, Qualified: qualified}
}

func (o BindingOrigin) IsUnqualifiedImport() bool {
	return o.Kind == BindingOriginImport && !o.Qualified
}

func (o BindingOrigin) AddImportProvider(importName string) BindingOrigin {
	if o.Kind != BindingOriginImport {
		return ImportedBindingOrigin(importName, false)
	}
	if slices.Contains(o.ImportNames, importName) {
		return o
	}
	o.ImportNames = append(o.ImportNames, importName)
	return o
}

func (o BindingOrigin) ConflictingImports() []string {
	if !o.IsUnqualifiedImport() || len(o.ImportNames) < 2 {
		return nil
	}
	return o.ImportNames
}

type Env interface {
	hm.Env
	hm.Type
	NamedType(string) (Env, bool)
	LocalNamedType(string) (Env, bool)
	AddClass(string, Env)
	SetTypeOrigin(string, BindingOrigin)
	LocalTypeOrigin(string) (BindingOrigin, bool)
	SetDocString(string, string)
	GetDocString(string) (string, bool)
	SetDirectives(string, []*DirectiveApplication)
	GetDirectives(string) []*DirectiveApplication
	SetModuleDocString(string)
	GetModuleDocString() string
	SetVisibility(string, Visibility)
	LocalSchemeOf(string) (*hm.Scheme, bool)
	SetValueOrigin(string, BindingOrigin)
	LocalValueOrigin(string) (BindingOrigin, bool)
	AddDirective(string, *DirectiveDecl)
	GetDirective(string) (*DirectiveDecl, bool)
	SetDirectiveOrigin(string, BindingOrigin)
	LocalDirectiveOrigin(string) (BindingOrigin, bool)
	Bindings(visibility Visibility) iter.Seq2[string, *hm.Scheme]
	CheckTypeConflict(symbolName string) []string
	CheckValueConflict(symbolName string) []string
	CheckDirectiveConflict(directiveName string) []string
	NamedTypes() iter.Seq2[string, Env]
}

// ModuleKind represents the kind of module
type ModuleKind int

const (
	ObjectKind ModuleKind = iota
	EnumKind
	ScalarKind
	InterfaceKind
	UnionKind
	InputKind
)

func (k ModuleKind) String() string {
	switch k {
	case ObjectKind:
		return "object"
	case EnumKind:
		return "enum"
	case ScalarKind:
		return "scalar"
	case InterfaceKind:
		return "interface"
	case UnionKind:
		return "union"
	case InputKind:
		return "input"
	default:
		return "unknown"
	}
}

func ModuleKindFromGraphQLKind(typeKind introspection.TypeKind) (ModuleKind, error) {
	switch typeKind {
	case introspection.TypeKindScalar:
		return ScalarKind, nil
	case introspection.TypeKindObject:
		return ObjectKind, nil
	case introspection.TypeKindInterface:
		return InterfaceKind, nil
	case introspection.TypeKindUnion:
		return UnionKind, nil
	case introspection.TypeKindEnum:
		return EnumKind, nil
	case introspection.TypeKindInputObject:
		return InputKind, nil
	default:
		return -1, fmt.Errorf("unsupported GraphQL type kind: %s", typeKind)
	}
}

// TODO: is this just ClassType? are Classes just named Envs?
type Module struct {
	Named string
	Kind  ModuleKind

	// Qualifier is the import/module alias used for display-only type names.
	Qualifier string

	Parent Env

	classes          map[string]Env
	vars             map[string]*hm.Scheme
	varOrder         []string
	visibility       map[string]Visibility
	directives       map[string]*DirectiveDecl
	typeOrigins      map[string]BindingOrigin
	valueOrigins     map[string]BindingOrigin
	directiveOrigins map[string]BindingOrigin
	slotDirectives   map[string][]*DirectiveApplication
	docStrings       map[string]string
	moduleDocString  string

	// Type-level dynamic scope type
	dynamicScopeType hm.Type

	// Interface tracking
	interfaces   []Env // Interfaces this type implements
	implementers []Env // Types that implement this interface (for interface modules)

	// Narrowed projections (inline fragment selections) create modules
	// with a subset of fields.  Canonical points back to the full type
	// so that runtime type matching can use identity instead of names.
	Canonical *Module

	// Union tracking
	members []Env // Member types of this union (for union modules)
	unions  []Env // Unions this type is a member of

}

func NewModule(name string, kind ModuleKind) *Module {
	env := &Module{
		Named:            name,
		Kind:             kind,
		classes:          make(map[string]Env),
		vars:             make(map[string]*hm.Scheme),
		visibility:       make(map[string]Visibility),
		directives:       make(map[string]*DirectiveDecl),
		typeOrigins:      make(map[string]BindingOrigin),
		valueOrigins:     make(map[string]BindingOrigin),
		directiveOrigins: make(map[string]BindingOrigin),
		slotDirectives:   make(map[string][]*DirectiveApplication),
		docStrings:       make(map[string]string),
		moduleDocString:  "",
	}
	return env
}

func gqlFieldToTypeNode(mod Env, field *introspection.Field) (hm.Type, error) {
	return gqlOutputTypeRefToTypeNode(mod, field.TypeRef, field.Directives.ExpectedType())
}

func gqlOutputTypeRefToTypeNode(mod Env, ref *introspection.TypeRef, expectedType string) (hm.Type, error) {
	switch ref.Kind {
	case introspection.TypeKindList:
		inner, err := gqlOutputTypeRefToTypeNode(mod, ref.OfType, expectedType)
		if err != nil {
			return nil, fmt.Errorf("gqlOutputTypeRefToTypeNode List: %w", err)
		}
		// Lists of objects use GraphQLListType (not directly iterable)
		// Lists of scalars use regular ListType (iterable)
		if ref.OfType.IsObject() {
			return GraphQLListType{inner}, nil
		}
		return ListType{inner}, nil
	case introspection.TypeKindNonNull:
		inner, err := gqlOutputTypeRefToTypeNode(mod, ref.OfType, expectedType)
		if err != nil {
			return nil, fmt.Errorf("gqlOutputTypeRefToTypeNode NonNull: %w", err)
		}
		return hm.NonNullType{Type: inner}, nil
	case introspection.TypeKindScalar:
		if ref.Name == "ID" && expectedType != "" {
			t, found := mod.NamedType(expectedType)
			if !found {
				return nil, fmt.Errorf("gqlOutputTypeRefToTypeNode: expected type %q not found", expectedType)
			}
			return t, nil
		}
		if strings.HasSuffix(ref.Name, "ID") && ref.Name != "ID" {
			return gqlOutputTypeRefToTypeNode(mod, &introspection.TypeRef{
				Name: strings.TrimSuffix(ref.Name, "ID"),
				Kind: introspection.TypeKindObject,
			}, "")
		}
		fallthrough
	default:
		t, found := mod.NamedType(ref.Name)
		if !found {
			return nil, fmt.Errorf("gqlOutputTypeRefToTypeNode: %s %q not found", ref.Kind, ref.Name)
		}
		return t, nil
	}
}

func gqlInputToTypeNode(mod Env, input introspection.InputValue) (hm.Type, error) {
	return gqlInputTypeRefToTypeNode(mod, input.TypeRef, input.Directives.ExpectedType())
}

func gqlInputTypeRefToTypeNode(mod Env, ref *introspection.TypeRef, expectedType string) (hm.Type, error) {
	switch ref.Kind {
	case introspection.TypeKindList:
		inner, err := gqlInputTypeRefToTypeNode(mod, ref.OfType, expectedType)
		if err != nil {
			return nil, fmt.Errorf("gqlInputTypeRefToTypeNode List: %w", err)
		}
		return ListType{inner}, nil
	case introspection.TypeKindNonNull:
		inner, err := gqlInputTypeRefToTypeNode(mod, ref.OfType, expectedType)
		if err != nil {
			return nil, fmt.Errorf("gqlInputTypeRefToTypeNode NonNull: %w", err)
		}
		return hm.NonNullType{Type: inner}, nil
	case introspection.TypeKindScalar:
		isIDScalar := ref.Name == "ID" || (strings.HasSuffix(ref.Name, "ID") && ref.Name != "ID")
		if expectedType != "" && isIDScalar {
			expected, found := mod.NamedType(expectedType)
			if !found {
				return nil, fmt.Errorf("gqlInputTypeRefToTypeNode: expected type %q not found", expectedType)
			}
			scalar, found := mod.NamedType(ref.Name)
			if !found {
				return nil, fmt.Errorf("gqlInputTypeRefToTypeNode: scalar %q not found", ref.Name)
			}
			return hm.NewUnionType(expected, scalar), nil
		}
		if strings.HasSuffix(ref.Name, "ID") && ref.Name != "ID" {
			scalar, scalarFound := mod.NamedType(ref.Name)
			object, objectFound := mod.NamedType(strings.TrimSuffix(ref.Name, "ID"))
			if scalarFound && objectFound {
				return hm.NewUnionType(object, scalar), nil
			}
		}
		fallthrough
	default:
		t, found := mod.NamedType(ref.Name)
		if !found {
			return nil, fmt.Errorf("gqlInputTypeRefToTypeNode: %s %q not found", ref.Kind, ref.Name)
		}
		return t, nil
	}
}

var Prelude *Module

func init() {
	Prelude = NewModule("Prelude", ObjectKind)

	// Install built-in types
	Prelude.AddClass("ID", IDType)
	Prelude.AddClass("String", StringType)
	Prelude.AddClass("Int", IntType)
	Prelude.AddClass("Float", FloatType)
	Prelude.AddClass("Boolean", BooleanType)
	Prelude.AddClass("List", ListTypeModule)

	// Install built-in modules (as both classes and values)
	Prelude.AddClass("Random", RandomModule)
	Prelude.AddClass("UUID", UUIDModule)
	Prelude.Add("Random", hm.NewScheme(nil, hm.NonNullType{Type: RandomModule}))
	Prelude.Add("UUID", hm.NewScheme(nil, hm.NonNullType{Type: UUIDModule}))

	// Install Error interface with message field
	Prelude.AddClass("Error", ErrorType)
	ErrorType.Add("message", hm.NewScheme(nil, hm.NonNullType{Type: StringType}))
	ErrorType.SetVisibility("message", PublicVisibility)

	// Install BasicError — the concrete type behind raise "msg"
	Prelude.AddClass("BasicError", BasicErrorType)
	BasicErrorType.Add("message", hm.NewScheme(nil, hm.NonNullType{Type: StringType}))
	BasicErrorType.SetVisibility("message", PublicVisibility)
	BasicErrorType.AddInterface(ErrorType)
	ErrorType.AddImplementer(BasicErrorType)

	// Register standard library builtins
	registerStdlib()

	// Register builtin function types from the registry
	registerBuiltinTypes()
}

func NewPreludeEnv(name string) *CompositeModule {
	mod := NewModule(name, ObjectKind)
	return &CompositeModule{mod, Prelude}
}

func NewEnv(name string, schema *introspection.Schema) Env {
	env := NewPreludeEnv(name)

	isBuiltinScalar := func(t *introspection.Type) bool {
		if t.Kind != introspection.TypeKindScalar {
			return false
		}
		switch t.Name {
		case "ID", "String", "Int", "Float", "Boolean":
			return true
		default:
			return false
		}
	}

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
		env.AddDirective(t.Name, directive)
	}

	// Track schema-owned types separately from env.NamedType. env has Prelude as
	// a lexical fallback, which is useful for resolving built-in scalars but must
	// not participate in schema declaration ownership. A schema type named Error,
	// Random, etc. is a new GraphQL type that shadows the Dang Prelude type.
	schemaTypes := map[string]Env{}

	for _, t := range schema.Types {
		var sub Env
		if isBuiltinScalar(t) {
			var found bool
			sub, found = env.lexical.NamedType(t.Name)
			if !found {
				slog.Warn("built-in scalar not found", "type", t.Name)
				continue
			}
		} else {
			var found bool
			sub, found = schemaTypes[t.Name]
			if !found {
				kind, err := ModuleKindFromGraphQLKind(t.Kind)
				if err != nil {
					slog.Warn("skipping unsupported type", "type", t.Name, "kind", t.Kind, "error", err)
					continue
				}
				sub = NewModule(t.Name, kind)
				if subMod, ok := sub.(*Module); ok {
					subMod.Qualifier = name
				}
				// Store type description as module documentation
				if t.Description != "" {
					sub.SetModuleDocString(t.Description)
				}
				schemaTypes[t.Name] = sub
				env.AddClass(t.Name, sub)
			}
		}
		if t.Name == schema.QueryType.Name {
			// TODO: "lexical" is maybe not the right word anymore
			env.lexical = &CompositeModule{sub, env.lexical}
		}
		// Expose the Mutation type as a named value (not lexical) so fields
		// are accessed via Mutation.fieldName rather than bare names.
		if schema.MutationType != nil && t.Name == schema.MutationType.Name {
			env.Add(t.Name, hm.NewScheme(nil, NonNull(sub)))
			env.SetVisibility(t.Name, PublicVisibility)
		}
	}

	// Make enum types available as values in the module
	for _, t := range schema.Types {
		if t.Kind == introspection.TypeKindEnum {
			sub, found := schemaTypes[t.Name]
			if found {
				// Add the enum type as a scheme that represents the module itself
				env.Add(t.Name, hm.NewScheme(nil, NonNull(sub)))
				env.SetVisibility(t.Name, PublicVisibility)
			}
		}
	}

	// Make custom scalar types available as values in the module
	for _, t := range schema.Types {
		if t.Kind == introspection.TypeKindScalar {
			// Skip built-in scalars (String, Int, Float, Boolean, ID)
			if isBuiltinScalar(t) {
				continue
			}
			sub, found := schemaTypes[t.Name]
			if !found {
				continue
			}
			// Add the scalar type as a scheme
			env.Add(t.Name, hm.NewScheme(nil, sub))
			env.SetVisibility(t.Name, PublicVisibility)
		}
	}

	// Make interface types available as values in the module
	for _, t := range schema.Types {
		if t.Kind == introspection.TypeKindInterface {
			sub, found := schemaTypes[t.Name]
			if found {
				// Add the interface type as a scheme that represents the module itself
				env.Add(t.Name, hm.NewScheme(nil, sub))
				env.SetVisibility(t.Name, PublicVisibility)
			}
		}
	}

	// Make union types available as values in the module
	for _, t := range schema.Types {
		if t.Kind == introspection.TypeKindUnion {
			sub, found := schemaTypes[t.Name]
			if found {
				// Add the union type as a scheme that represents the module itself
				env.Add(t.Name, hm.NewScheme(nil, sub))
				env.SetVisibility(t.Name, PublicVisibility)
			}
		}
	}

	// Make input object types available as constructors:
	// UserSort(field: ..., direction: ...) creates a UserSort value
	for _, t := range schema.Types {
		if t.Kind == introspection.TypeKindInputObject {
			sub, found := schemaTypes[t.Name]
			if found {
				args := NewRecordType("")
				for _, f := range t.InputFields {
					fieldType, err := gqlInputToTypeNode(env, f)
					if err != nil {
						continue
					}
					if f.DefaultValue != nil {
						if nn, ok := fieldType.(hm.NonNullType); ok {
							fieldType = nn.Type
						}
					}
					args.Add(f.Name, hm.NewScheme(nil, fieldType))
				}
				constructorType := hm.NewFnType(args, NonNull(sub))
				env.Add(t.Name, hm.NewScheme(nil, constructorType))
				env.SetVisibility(t.Name, PublicVisibility)
			}
		}
	}

	for _, t := range schema.Types {
		install, found := schemaTypes[t.Name]
		if !found {
			// Built-in GraphQL scalars live in the shared Prelude and have no schema
			// fields to install. All schema-owned types were recorded above.
			continue
		}

		// Input objects: register their fields so they can be used as constructors
		if t.Kind == introspection.TypeKindInputObject {
			for _, f := range t.InputFields {
				fieldType, err := gqlInputToTypeNode(env, f)
				if err != nil {
					panic(err)
				}
				if f.DefaultValue != nil {
					// Optional field: make nullable in the type
					if nn, ok := fieldType.(hm.NonNullType); ok {
						fieldType = nn.Type
					}
				}
				install.Add(f.Name, hm.NewScheme(nil, fieldType))
				install.SetVisibility(f.Name, PublicVisibility)
				if f.Description != "" {
					install.SetDocString(f.Name, f.Description)
				}
			}
		}

		// Assign enum values as string fields for enum types
		if t.Kind == introspection.TypeKindEnum {
			for _, enumVal := range t.EnumValues {
				// Enum values are represented with the enum type itself
				install.Add(enumVal.Name, hm.NewScheme(nil, NonNull(install)))
				// Enum values are public by default
				install.SetVisibility(enumVal.Name, PublicVisibility)
				// Store enum value description as documentation
				if enumVal.Description != "" {
					install.SetDocString(enumVal.Name, enumVal.Description)
				}
			}
			// Add the values() method that returns all enum values as a list
			valuesType := hm.NewScheme(nil, NonNull(ListType{NonNull(install)}))
			install.Add("values", valuesType)
			install.SetVisibility("values", PublicVisibility)
		}

		for _, f := range t.Fields {
			ret, err := gqlFieldToTypeNode(env, f)
			if err != nil {
				panic(err)
			}

			args := NewRecordType("")
			for _, arg := range f.Args {
				argType, err := gqlInputToTypeNode(env, arg)
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
				if arg.Description != "" {
					if args.DocStrings == nil {
						args.DocStrings = make(map[string]string)
					}
					args.DocStrings[arg.Name] = arg.Description
				}
			}
			install.Add(f.Name, hm.NewScheme(nil, hm.NewFnType(args, ret)))
			// GraphQL schema fields are public by default
			install.SetVisibility(f.Name, PublicVisibility)
			// Store field description as documentation
			if f.Description != "" {
				install.SetDocString(f.Name, f.Description)
			}
		}
	}

	// Link interface implementations
	for _, t := range schema.Types {
		// Skip types that don't implement any interfaces
		if len(t.Interfaces) == 0 {
			continue
		}

		implType, found := schemaTypes[t.Name]
		if !found {
			continue
		}

		// For each interface this type implements
		for _, iface := range t.Interfaces {
			ifaceModule, found := schemaTypes[iface.Name]
			if !found {
				slog.Warn("interface not found", "interface", iface.Name, "implementer", t.Name)
				continue
			}

			// Link them together
			if implMod, ok := implType.(*Module); ok {
				implMod.AddInterface(ifaceModule)
			}
			if ifaceMod, ok := ifaceModule.(*Module); ok {
				ifaceMod.AddImplementer(implType)
			}
		}
	}

	// Link union members
	for _, t := range schema.Types {
		if t.Kind != introspection.TypeKindUnion {
			continue
		}

		unionType, found := schemaTypes[t.Name]
		if !found {
			continue
		}

		unionMod, ok := unionType.(*Module)
		if !ok {
			continue
		}

		for _, member := range t.PossibleTypes {
			memberType, found := schemaTypes[member.Name]
			if !found {
				slog.Warn("union member not found", "union", t.Name, "member", member.Name)
				continue
			}

			unionMod.LinkMember(memberType)
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
			Name: name,
		}
	}
}

func (e *Module) Bindings(visibility Visibility) iter.Seq2[string, *hm.Scheme] {
	return func(yield func(string, *hm.Scheme) bool) {
		seen := map[string]bool{}
		for _, name := range e.varOrder {
			v, ok := e.vars[name]
			if !ok {
				continue
			}
			seen[name] = true
			if e.visibility[name] >= visibility {
				if !yield(name, v) {
					return
				}
			}
		}

		// Be robust to modules constructed before varOrder existed or by tests
		// that mutate vars directly in-package: yield any stragglers
		// deterministically after the ordered entries.
		var unordered []string
		for name := range e.vars {
			if !seen[name] {
				unordered = append(unordered, name)
			}
		}
		sort.Strings(unordered)
		for _, name := range unordered {
			if e.visibility[name] >= visibility {
				if !yield(name, e.vars[name]) {
					return
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
	if _, exists := e.vars[name]; !exists {
		e.varOrder = append(e.varOrder, name)
	}
	e.vars[name] = s
	e.valueOrigins[name] = LocalBindingOrigin()
	if _, ok := e.visibility[name]; !ok {
		e.visibility[name] = PrivateVisibility
	}
	return e
}

func (e *Module) SetValueOrigin(name string, origin BindingOrigin) {
	e.valueOrigins[name] = origin
}

func (e *Module) LocalValueOrigin(name string) (BindingOrigin, bool) {
	origin, ok := e.valueOrigins[name]
	return origin, ok
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
	mod := NewModule(e.Named, e.Kind)
	mod.Qualifier = e.Qualifier
	mod.Parent = e
	mod.dynamicScopeType = e.dynamicScopeType
	return mod
}

func (e *Module) GetDynamicScopeType() hm.Type {
	if e.dynamicScopeType != nil {
		return e.dynamicScopeType
	}
	if e.Parent != nil {
		return e.Parent.GetDynamicScopeType()
	}
	return nil
}

func (e *Module) SetDynamicScopeType(t hm.Type) {
	e.dynamicScopeType = t
}

func (e *Module) NamedTypes() iter.Seq2[string, Env] {
	return func(yield func(string, Env) bool) {
		for name, env := range e.classes {
			if !yield(name, env) {
				break
			}
		}
	}
}

func (e *Module) AddClass(name string, c Env) {
	e.classes[name] = c
	e.typeOrigins[name] = LocalBindingOrigin()
}

func (e *Module) SetTypeOrigin(name string, origin BindingOrigin) {
	e.typeOrigins[name] = origin
}

func (e *Module) LocalTypeOrigin(name string) (BindingOrigin, bool) {
	origin, ok := e.typeOrigins[name]
	return origin, ok
}

func (e *Module) AddDirective(name string, directive *DirectiveDecl) {
	e.directives[name] = directive
	e.directiveOrigins[name] = LocalBindingOrigin()
}

func (e *Module) SetDirectiveOrigin(name string, origin BindingOrigin) {
	e.directiveOrigins[name] = origin
}

func (e *Module) LocalDirectiveOrigin(name string) (BindingOrigin, bool) {
	origin, ok := e.directiveOrigins[name]
	return origin, ok
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
	t, ok := e.LocalNamedType(name)
	if ok {
		return t, ok
	}
	if e.Parent != nil {
		return e.Parent.NamedType(name)
	}
	return nil, false
}

func (e *Module) LocalNamedType(name string) (Env, bool) {
	t, ok := e.classes[name]
	return t, ok
}

func (e *Module) Remove(name string) hm.Env {
	// TODO: lol, tombstone???? idk if i ever use this method. maybe i don't need
	// to conform to hm.Env?
	delete(e.vars, name)
	for i, orderedName := range e.varOrder {
		if orderedName == name {
			e.varOrder = append(e.varOrder[:i], e.varOrder[i+1:]...)
			break
		}
	}
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

// SetDirectives sets the documentation string for a symbol
func (e *Module) SetDirectives(name string, directives []*DirectiveApplication) {
	e.slotDirectives[name] = directives
}

// GetDirectives gets the documentation string for a symbol
func (e *Module) GetDirectives(name string) []*DirectiveApplication {
	if slotDirectives, ok := e.slotDirectives[name]; ok {
		return slotDirectives
	}
	if e.Parent != nil {
		if parent, ok := e.Parent.(*Module); ok {
			return parent.GetDirectives(name)
		}
	}
	return nil
}

// registerBuiltinTypes registers types for all builtins in the Prelude
func registerBuiltinTypes() {
	// Register all builtin function types
	ForEachFunction(func(def BuiltinDef) {
		fnType := createFunctionTypeFromDef(def)
		Prelude.Add(def.Name, hm.NewScheme(nil, fnType))
	})

	// Register all builtin method types
	for _, receiverType := range MethodReceivers() {
		ForEachMethod(receiverType, func(def BuiltinDef) {
			fnType := createFunctionTypeFromDef(def)
			receiverType.Add(def.Name, hm.NewScheme(nil, fnType))
			receiverType.SetVisibility(def.Name, PublicVisibility)
			if def.Doc != "" {
				receiverType.SetDocString(def.Name, def.Doc)
			}
		})
	}

	// Register all static method types on their host modules
	for _, hostModule := range StaticModules() {
		ForEachStaticMethod(hostModule, func(def BuiltinDef) {
			fnType := createFunctionTypeFromDef(def)
			hostModule.Add(def.Name, hm.NewScheme(nil, fnType))
			hostModule.SetVisibility(def.Name, PublicVisibility)
		})
	}
}

// createFunctionTypeFromDef creates a FunctionType from a BuiltinDef
func createFunctionTypeFromDef(def BuiltinDef) *hm.FunctionType {
	args := NewRecordType("")
	for _, param := range def.ParamTypes {
		paramType := param.Type
		// Parameters with defaults are optional — strip NonNull so the
		// type checker treats them as nullable (not required).
		if param.DefaultValue != nil {
			if nn, ok := paramType.(hm.NonNullType); ok {
				paramType = nn.Type
			}
		}
		args.Add(param.Name, hm.NewScheme(nil, paramType))
	}
	fnType := hm.NewFnType(args, def.ReturnType)

	// Set block type if present
	if def.BlockType != nil {
		fnType.SetBlock(def.BlockType)
	}

	return fnType
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
		if t.Qualifier != "" {
			return t.Qualifier + "." + t.Named
		}
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
		// Named modules are only equal if they're the exact same instance (pointer equality)
		return t == otherMod
	}
	// Unnamed modules (anonymous record-like modules) use structural equality
	return t.AsRecord().Eq(otherMod.AsRecord())
}

func (t *Module) Supertypes() []Type {
	// Only object types have supertypes (their interfaces and unions)
	if t.Kind != ObjectKind {
		return nil
	}
	if len(t.interfaces) == 0 && len(t.unions) == 0 {
		return nil
	}
	// Convert []Env to []Type - include both interfaces and unions
	result := make([]Type, 0, len(t.interfaces)+len(t.unions))
	for _, iface := range t.interfaces {
		result = append(result, iface.(Type))
	}
	for _, union := range t.unions {
		result = append(result, union.(Type))
	}
	return result
}

// AcceptsCoercionFrom implements hm.Coercible for explicit scalar/enum casts.
// Enums and string-backed scalars (including ID and custom scalars) accept
// coercion from String so runtime materialization can validate/construct the
// target value. Primitive scalars do not accept value-level coercion.
func (t *Module) AcceptsCoercionFrom(other hm.Type) bool {
	if t.Kind == EnumKind {
		return other == StringType
	}

	if t.Kind != ScalarKind {
		return false
	}

	// Primitive scalars are not coerced. ID is intentionally excluded here: Dang
	// treats it as a distinct string-backed scalar that can be explicitly cast
	// from String.
	switch t {
	case StringType, IntType, FloatType, BooleanType:
		return false
	}

	return other == StringType
}

// AddInterface adds an interface that this type implements
func (m *Module) AddInterface(iface Env) {
	if slices.Contains(m.interfaces, iface) {
		return
	}
	m.interfaces = append(m.interfaces, iface)
}

// GetInterfaces returns the interfaces this type implements
func (m *Module) GetInterfaces() []Env {
	return m.interfaces
}

// AddImplementer adds a type that implements this interface (for interface modules)
func (m *Module) AddImplementer(impl Env) {
	if slices.Contains(m.implementers, impl) {
		return
	}
	m.implementers = append(m.implementers, impl)
}

// GetImplementers returns the types that implement this interface (for interface modules)
func (m *Module) GetImplementers() []Env {
	return m.implementers
}

// ImplementsInterface checks if this type implements the given interface
func (m *Module) ImplementsInterface(iface Env) bool {
	return slices.Contains(m.interfaces, iface)
}

// AddMember adds a member type to this union (for union modules).
func (m *Module) AddMember(member Env) {
	if slices.Contains(m.members, member) {
		return
	}
	m.members = append(m.members, member)
}

// LinkMember adds a union member and records the reverse relationship on the
// member. Only call this when the member module is owned by the same local
// environment; Prelude modules are shared process-wide and must not record
// per-module union declarations.
func (m *Module) LinkMember(member Env) {
	m.AddMember(member)
	if memberMod, ok := member.(*Module); ok {
		if slices.Contains(memberMod.unions, Env(m)) {
			return
		}
		memberMod.unions = append(memberMod.unions, m)
	}
}

// GetMembers returns the member types of this union (for union modules)
func (m *Module) GetMembers() []Env {
	return m.members
}

// HasMember checks if this union contains the given type as a member
func (m *Module) HasMember(t Env) bool {
	return slices.Contains(m.members, t)
}

// GetUnions returns the unions this type is a member of
func (m *Module) GetUnions() []Env {
	return m.unions
}

// CheckTypeConflict checks if a type-level symbol has import conflicts.
// Returns the list of imports that provide it (empty if no conflict or not tracked).
func (m *Module) CheckTypeConflict(symbolName string) []string {
	if origin, found := m.LocalTypeOrigin(symbolName); found {
		return origin.ConflictingImports()
	}
	if m.Parent != nil {
		return m.Parent.CheckTypeConflict(symbolName)
	}
	return nil
}

// CheckValueConflict checks if a value-level symbol has import conflicts.
// Returns the list of imports that provide it (empty if no conflict or not tracked).
func (m *Module) CheckValueConflict(symbolName string) []string {
	if origin, found := m.LocalValueOrigin(symbolName); found {
		return origin.ConflictingImports()
	}
	if m.Parent != nil {
		return m.Parent.CheckValueConflict(symbolName)
	}
	return nil
}

// CheckDirectiveConflict checks if a directive has import conflicts.
// Returns the list of imports that provide it (empty if no conflict or not tracked).
func (m *Module) CheckDirectiveConflict(directiveName string) []string {
	if origin, found := m.LocalDirectiveOrigin(directiveName); found {
		return origin.ConflictingImports()
	}
	if m.Parent != nil {
		return m.Parent.CheckDirectiveConflict(directiveName)
	}
	return nil
}

// validateFieldImplementation validates that a class field correctly implements an interface field
// according to GraphQL interface implementation rules:
// - Return types must be covariant (implementation can be more specific)
// - Argument types must be contravariant (implementation can be more general)
// - All interface arguments must be present
// - Additional arguments must be optional
func validateFieldImplementation(fieldName string, ifaceFieldType, classFieldType hm.Type, ifaceName, className string) error {
	// This is schema-level compatibility, not a value handoff, so use pure
	// subtyping (no value-level scalar coercions such as String -> ID).
	// Both must be function types (fields in GraphQL are represented as functions)
	ifaceFn, ifaceIsFn := ifaceFieldType.(*hm.FunctionType)
	classFn, classIsFn := classFieldType.(*hm.FunctionType)

	// If interface field is not a function, class field must match exactly
	if !ifaceIsFn {
		if !classIsFn {
			// Both are non-function types - check covariance
			if !hm.IsSubtypeOf(classFieldType, ifaceFieldType) {
				return fmt.Errorf("field %q: type %s is not compatible with interface type %s",
					fieldName, classFieldType, ifaceFieldType)
			}
			return nil
		}
		return fmt.Errorf("field %q: class has function type but interface does not", fieldName)
	}

	// Interface field is a function
	// Check if it's a zero-argument function (common for GraphQL fields and properties)
	isZeroArgFn := false
	if ifaceFn != nil {
		if rt, ok := ifaceFn.Arg().(*RecordType); ok {
			isZeroArgFn = len(rt.Fields) == 0
		}
	}

	// If interface has a zero-arg function and class has a simple field, unwrap and compare
	if isZeroArgFn && !classIsFn {
		// Unwrap the function to get the return type
		ifaceRetType := ifaceFn.Ret(false)
		// Compare the return type with the class field type
		if !hm.IsSubtypeOf(classFieldType, ifaceRetType) {
			return fmt.Errorf("field %q: type %s is not compatible with interface type %s",
				fieldName, classFieldType, ifaceRetType)
		}
		return nil
	}

	// Interface field is a function - class field must also be a function
	if !classIsFn {
		return fmt.Errorf("field %q: interface has function type but class does not", fieldName)
	}

	// Validate return type (covariant - class can return more specific type)
	classRetType := classFn.Ret(false)
	ifaceRetType := ifaceFn.Ret(false)

	if !hm.IsSubtypeOf(classRetType, ifaceRetType) {
		return fmt.Errorf("field %q: return type %s is not compatible with interface return type %s (covariance required)",
			fieldName, classRetType, ifaceRetType)
	}

	// Validate arguments (contravariant - class can accept more general types)
	ifaceArgs, ifaceArgsOk := ifaceFn.Arg().(*RecordType)
	classArgs, classArgsOk := classFn.Arg().(*RecordType)

	if !ifaceArgsOk || !classArgsOk {
		// Arguments must be records
		return fmt.Errorf("field %q: arguments must be record types", fieldName)
	}

	// Check that all interface arguments are present in class
	for _, ifaceArg := range ifaceArgs.Fields {
		classArgScheme, found := classArgs.SchemeOf(ifaceArg.Key)
		if !found {
			return fmt.Errorf("field %q: missing argument %q required by interface", fieldName, ifaceArg.Key)
		}

		// Validate argument type compatibility (contravariant)
		classArgType, _ := classArgScheme.Type()
		ifaceArgType, _ := ifaceArg.Value.Type()

		// For contravariance: class arg type must be a supertype of interface arg type
		// This means: if interface requires String!, class can accept String or String!
		// But if interface requires String, class must accept String (can't require String!)
		if !hm.IsSupertypeOf(classArgType, ifaceArgType) {
			return fmt.Errorf("field %q, argument %q: type %s is not compatible with interface type %s (contravariance required)",
				fieldName, ifaceArg.Key, classArgType, ifaceArgType)
		}
	}

	// Check that any additional arguments in class are optional
	for _, classArg := range classArgs.Fields {
		// Check if this argument exists in the interface
		_, found := ifaceArgs.SchemeOf(classArg.Key)
		if !found {
			// Additional argument - must be optional (nullable or has default)
			classArgType, _ := classArg.Value.Type()
			if _, isNonNull := classArgType.(hm.NonNullType); isNonNull {
				return fmt.Errorf("field %q, argument %q: additional arguments not in interface must be optional (nullable or have default)",
					fieldName, classArg.Key)
			}
		}
	}

	return nil
}
