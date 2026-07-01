package dang

import (
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"sort"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"
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

// TypeScope is both a typing environment (hm.Env: name->scheme value bindings)
// and a type in its own right (hm.Type), so a declared type can appear in type
// positions while also carrying a namespace of members. On top of hm.Env it
// tracks three kinds of named symbol -- types, values, and directives -- each
// with parallel origin and conflict bookkeeping, plus docs and visibility.
//
// Throughout, the Local* variants consult only this scope; their non-Local
// counterparts fall back to Parent.
type TypeScope interface {
	hm.Env
	hm.Type

	// Named types declared or imported into this scope.
	NamedType(string) (TypeScope, bool)
	LocalNamedType(string) (TypeScope, bool)
	AddObject(string, TypeScope)
	NamedTypes() iter.Seq2[string, TypeScope]

	// Value/field bindings. hm.Env supplies SchemeOf/Add; these extend it.
	LocalSchemeOf(string) (*hm.Scheme, bool)
	Bindings(visibility Visibility) iter.Seq2[string, *hm.Scheme]
	SetVisibility(string, Visibility)

	// Directives: declarations (Add/GetDirective) and the applications
	// attached to a named member (Set/GetDirectives).
	AddDirective(string, *DirectiveDecl)
	GetDirective(string) (*DirectiveDecl, bool)
	SetDirectives(string, []*DirectiveApplication)
	GetDirectives(string) []*DirectiveApplication

	// Documentation, for individual members and for the scope itself.
	SetDocString(string, string)
	GetDocString(string) (string, bool)
	SetTypeDocString(string)
	GetTypeDocString() string

	// Binding origins: where each symbol came from (local vs which import),
	// tracked separately for types, values, and directives.
	SetTypeOrigin(string, BindingOrigin)
	LocalTypeOrigin(string) (BindingOrigin, bool)
	SetValueOrigin(string, BindingOrigin)
	LocalValueOrigin(string) (BindingOrigin, bool)
	SetDirectiveOrigin(string, BindingOrigin)
	LocalDirectiveOrigin(string) (BindingOrigin, bool)

	// Import-conflict detection: returns the imports that provide a name when
	// more than one does (nil if there is no conflict).
	CheckTypeConflict(symbolName string) []string
	CheckValueConflict(symbolName string) []string
	CheckDirectiveConflict(directiveName string) []string
}

// Kind represents the kind of a type.
type Kind int

const (
	ObjectKind Kind = iota
	EnumKind
	ScalarKind
	InterfaceKind
	UnionKind
	InputKind
)

func (k Kind) String() string {
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

func KindFromGraphQLKind(typeKind introspection.TypeKind) (Kind, error) {
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

// Type is a declared named type -- object, interface, scalar, enum, union,
// or input, distinguished by Kind. It is deliberately one Kind-tagged struct
// rather than a struct per kind, because every named type is uniformly a scope
// (it implements TypeScope): it can carry value bindings and nested named
// types. That uniformity is the point. A scalar like String having methods, an
// object like Container having fields, and Regexp exposing a nested Regexp.Match
// type are all the same machinery -- members attach to any named type, the way
// Ruby attaches methods to any object.
//
// The Kind tag also mirrors GraphQL introspection's __Type, which is itself one
// type with a kind enum; that keeps schema import a near 1:1 mapping (see
// KindFromGraphQLKind). The cost is that kind-specific behavior is gated
// by Kind checks rather than enforced by the type system.
type Type struct {
	Named string
	Kind  Kind

	// Qualifier is the import/module alias used for display-only type names.
	Qualifier string

	Parent TypeScope

	objects          map[string]TypeScope
	vars             map[string]*hm.Scheme
	varOrder         []string
	visibility       map[string]Visibility
	directives       map[string]*DirectiveDecl
	typeOrigins      map[string]BindingOrigin
	valueOrigins     map[string]BindingOrigin
	directiveOrigins map[string]BindingOrigin
	fieldDirectives  map[string][]*DirectiveApplication
	docStrings       map[string]string
	typeDocString    string

	// Type-level dynamic scope type
	dynamicScopeType hm.Type

	// Interface tracking
	interfaces   []TypeScope // Interfaces this type implements
	implementers []TypeScope // Types that implement this interface (for interface modules)

	// Narrowed projections (inline fragment selections) create modules
	// with a subset of fields.  Canonical points back to the full type
	// so that runtime type matching can use identity instead of names.
	Canonical *Type

	// Union tracking
	members []TypeScope // Member types of this union (for union modules)
	unions  []TypeScope // Unions this type is a member of

}

func NewType(name string, kind Kind) *Type {
	env := &Type{
		Named:            name,
		Kind:             kind,
		objects:          make(map[string]TypeScope),
		vars:             make(map[string]*hm.Scheme),
		visibility:       make(map[string]Visibility),
		directives:       make(map[string]*DirectiveDecl),
		typeOrigins:      make(map[string]BindingOrigin),
		valueOrigins:     make(map[string]BindingOrigin),
		directiveOrigins: make(map[string]BindingOrigin),
		fieldDirectives:  make(map[string][]*DirectiveApplication),
		docStrings:       make(map[string]string),
		typeDocString:    "",
	}
	return env
}

func gqlFieldToTypeNode(mod TypeScope, field *introspection.Field) (hm.Type, error) {
	return gqlOutputTypeRefToTypeNode(mod, field.TypeRef, field.Directives.ExpectedType())
}

func gqlOutputTypeRefToTypeNode(mod TypeScope, ref *introspection.TypeRef, expectedType string) (hm.Type, error) {
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

func gqlInputToTypeNode(mod TypeScope, input introspection.InputValue) (hm.Type, error) {
	return gqlInputTypeRefToTypeNode(mod, input.TypeRef, input.Directives.ExpectedType())
}

func gqlInputTypeRefToTypeNode(mod TypeScope, ref *introspection.TypeRef, expectedType string) (hm.Type, error) {
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
		if expectedType != "" && ref.Name == "ID" {
			t, found := mod.NamedType(expectedType)
			if !found {
				return nil, fmt.Errorf("gqlInputTypeRefToTypeNode: expected type %q not found", expectedType)
			}
			return t, nil
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

var Prelude *Type

func init() {
	Prelude = NewType("Prelude", ObjectKind)

	// Install built-in types
	Prelude.AddObject("ID", IDType)
	Prelude.AddObject("String", StringType)
	Prelude.AddObject("Int", IntType)
	Prelude.AddObject("Float", FloatType)
	Prelude.AddObject("Boolean", BooleanType)
	Prelude.AddObject("List", ListTypeModule)

	// Install built-in modules (as both objects and values)
	Prelude.AddObject("Random", RandomModule)
	Prelude.AddObject("UUID", UUIDModule)
	Prelude.Add("Random", hm.NewScheme(nil, hm.NonNullType{Type: RandomModule}))
	Prelude.Add("UUID", hm.NewScheme(nil, hm.NonNullType{Type: UUIDModule}))

	// Install regex types so user code can refer to them by name.
	Prelude.AddObject("Regexp", RegexpType)
	RegexpType.AddObject("Match", MatchType)

	// Install Error interface with message field
	Prelude.AddObject("Error", ErrorType)
	ErrorType.Add("message", hm.NewScheme(nil, hm.NewFnType(NewRecordType(""), hm.NonNullType{Type: StringType})))
	ErrorType.SetVisibility("message", PublicVisibility)

	// Install BasicError — the concrete type behind raise "msg"
	Prelude.AddObject("BasicError", BasicErrorType)
	BasicErrorType.Add("message", hm.NewScheme(nil, hm.NonNullType{Type: StringType}))
	BasicErrorType.SetVisibility("message", PublicVisibility)
	BasicErrorType.AddInterface(ErrorType)
	ErrorType.AddImplementer(BasicErrorType)

	// Register standard library builtins
	registerStdlib()

	// Install the builtin scalars (JSON/YAML/TOML) in both namespaces, like the
	// modules above. They are the ScalarKind static modules registerStdlib just
	// registered. Each is AddObject'd so `:: JSON` resolves in type position even
	// without a schema scalar, and Add'd non-null so `JSON.encode`/`.decode`
	// resolve as a value. A user- or schema-declared scalar of the same name
	// shadows these and is grafted the identical members (merge, not collide),
	// so all behave uniformly. See builtins.go: BuiltinScalarModule.
	for _, mod := range StaticModules() {
		if mod.Kind != ScalarKind {
			continue
		}
		Prelude.AddObject(mod.Named, mod)
		Prelude.Add(mod.Named, hm.NewScheme(nil, hm.NonNullType{Type: mod}))
	}

	// Register builtin function types from the registry
	registerBuiltinTypes()
}

func NewPreludeTypeScope(name string) *OverlayTypeScope {
	mod := NewType(name, ObjectKind)
	return &OverlayTypeScope{mod, Prelude}
}

func TypeScopeFromSchema(name string, schema *introspection.Schema) TypeScope {
	env := NewPreludeTypeScope(name)

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
		var args []*FieldDecl
		for _, arg := range t.Args {
			args = append(args, &FieldDecl{
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
	schemaTypes := map[string]TypeScope{}

	for _, t := range schema.Types {
		var sub TypeScope
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
				kind, err := KindFromGraphQLKind(t.Kind)
				if err != nil {
					slog.Warn("skipping unsupported type", "type", t.Name, "kind", t.Kind, "error", err)
					continue
				}
				sub = NewType(t.Name, kind)
				if subMod, ok := sub.(*Type); ok {
					subMod.Qualifier = name
				}
				// Store type description as module documentation
				if t.Description != "" {
					sub.SetTypeDocString(t.Description)
				}
				schemaTypes[t.Name] = sub
				env.AddObject(t.Name, sub)
			}
		}
		if t.Name == schema.QueryType.Name {
			// TODO: "lexical" is maybe not the right word anymore
			env.lexical = &OverlayTypeScope{sub, env.lexical}
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
			// Add the scalar type as a scheme. A builtin scalar (e.g. an imported
			// JSON scalar) doubles as its namespace and is always present, so it
			// binds non-null — otherwise JSON.encode would inherit the binding's
			// nullability and weaken String! to String.
			scalarScheme := hm.NewScheme(nil, sub)
			if _, isBuiltin := BuiltinScalarModule(t.Name); isBuiltin {
				scalarScheme = hm.NewScheme(nil, hm.NonNullType{Type: sub})
			}
			env.Add(t.Name, scalarScheme)
			env.SetVisibility(t.Name, PublicVisibility)
			// Staple Dang's members onto the scalar so it doubles as the namespace.
			if scalarMod, ok := sub.(*Type); ok {
				attachBuiltinSchemes(scalarMod)
			}
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
			if implMod, ok := implType.(*Type); ok {
				implMod.AddInterface(ifaceModule)
			}
			if ifaceMod, ok := ifaceModule.(*Type); ok {
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

		unionMod, ok := unionType.(*Type)
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

func (e *Type) Bindings(visibility Visibility) iter.Seq2[string, *hm.Scheme] {
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

var _ hm.Substitutable = (*Type)(nil)

func (e *Type) Apply(subs hm.Subs) hm.Substitutable {
	if len(subs) == 0 || len(e.FreeTypeVar()) == 0 {
		return e
	}
	retVal := e.Clone().(*Type)
	for _, v := range retVal.vars {
		v.Apply(subs)
	}
	return retVal
}

func (e *Type) FreeTypeVar() hm.TypeVarSet {
	var retVal hm.TypeVarSet
	// for _, v := range e.vars {
	// 	retVal = v.FreeTypeVar().Union(retVal)
	// }
	return retVal
}

func (e *Type) Add(name string, s *hm.Scheme) hm.Env {
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

func (e *Type) SetValueOrigin(name string, origin BindingOrigin) {
	e.valueOrigins[name] = origin
}

func (e *Type) LocalValueOrigin(name string) (BindingOrigin, bool) {
	origin, ok := e.valueOrigins[name]
	return origin, ok
}

func (e *Type) SetVisibility(name string, visibility Visibility) {
	e.visibility[name] = visibility
}

func (e *Type) SchemeOf(name string) (*hm.Scheme, bool) {
	s, ok := e.vars[name]
	if ok {
		return s, ok
	}
	if e.Parent != nil {
		return e.Parent.SchemeOf(name)
	}
	return nil, false
}

func (e *Type) LocalSchemeOf(name string) (*hm.Scheme, bool) {
	s, ok := e.vars[name]
	return s, ok
}

func (e *Type) Clone() hm.Env {
	mod := NewType(e.Named, e.Kind)
	mod.Qualifier = e.Qualifier
	mod.Parent = e
	mod.dynamicScopeType = e.dynamicScopeType
	return mod
}

func (e *Type) GetDynamicScopeType() hm.Type {
	if e.dynamicScopeType != nil {
		return e.dynamicScopeType
	}
	if e.Parent != nil {
		return e.Parent.GetDynamicScopeType()
	}
	return nil
}

func (e *Type) SetDynamicScopeType(t hm.Type) {
	e.dynamicScopeType = t
}

func (e *Type) NamedTypes() iter.Seq2[string, TypeScope] {
	return func(yield func(string, TypeScope) bool) {
		for name, env := range e.objects {
			if !yield(name, env) {
				break
			}
		}
	}
}

func (e *Type) AddObject(name string, c TypeScope) {
	e.objects[name] = c
	e.typeOrigins[name] = LocalBindingOrigin()
}

func (e *Type) SetTypeOrigin(name string, origin BindingOrigin) {
	e.typeOrigins[name] = origin
}

func (e *Type) LocalTypeOrigin(name string) (BindingOrigin, bool) {
	origin, ok := e.typeOrigins[name]
	return origin, ok
}

func (e *Type) AddDirective(name string, directive *DirectiveDecl) {
	e.directives[name] = directive
	e.directiveOrigins[name] = LocalBindingOrigin()
}

func (e *Type) SetDirectiveOrigin(name string, origin BindingOrigin) {
	e.directiveOrigins[name] = origin
}

func (e *Type) LocalDirectiveOrigin(name string) (BindingOrigin, bool) {
	origin, ok := e.directiveOrigins[name]
	return origin, ok
}

func (e *Type) GetDirective(name string) (*DirectiveDecl, bool) {
	directive, ok := e.directives[name]
	if ok {
		return directive, ok
	}
	if e.Parent != nil {
		return e.Parent.GetDirective(name)
	}
	return nil, false
}

func (e *Type) NamedType(name string) (TypeScope, bool) {
	t, ok := e.LocalNamedType(name)
	if ok {
		return t, ok
	}
	if e.Parent != nil {
		return e.Parent.NamedType(name)
	}
	return nil, false
}

func (e *Type) LocalNamedType(name string) (TypeScope, bool) {
	t, ok := e.objects[name]
	return t, ok
}

func (e *Type) Remove(name string) hm.Env {
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
func (e *Type) SetDocString(name string, docString string) {
	e.docStrings[name] = docString
}

// GetDocString gets the documentation string for a symbol
func (e *Type) GetDocString(name string) (string, bool) {
	if docString, ok := e.docStrings[name]; ok {
		return docString, true
	}
	if e.Parent != nil {
		if parent, ok := e.Parent.(*Type); ok {
			return parent.GetDocString(name)
		}
	}
	return "", false
}

// SetDirectives sets the documentation string for a symbol
func (e *Type) SetDirectives(name string, directives []*DirectiveApplication) {
	e.fieldDirectives[name] = directives
}

// GetDirectives gets the documentation string for a symbol
func (e *Type) GetDirectives(name string) []*DirectiveApplication {
	if fieldDirectives, ok := e.fieldDirectives[name]; ok {
		return fieldDirectives
	}
	if e.Parent != nil {
		if parent, ok := e.Parent.(*Type); ok {
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

// SetTypeDocString sets the documentation string for the type itself
// (as opposed to its members, which use SetDocString).
func (e *Type) SetTypeDocString(docString string) {
	e.typeDocString = docString
}

// GetTypeDocString gets the documentation string for the type itself
func (e *Type) GetTypeDocString() string {
	return e.typeDocString
}

func (e *Type) AsRecord() *RecordType {
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

var _ hm.Type = (*Type)(nil)

func (t *Type) Name() string                                  { return t.Named }
func (t *Type) Normalize(k, v hm.TypeVarSet) (hm.Type, error) { return t, nil }
func (t *Type) Types() hm.Types                               { return nil }

func (t *Type) String() string {
	if t.Named != "" {
		if t.Qualifier != "" {
			return t.Qualifier + "." + t.Named
		}
		return t.Named
	}
	return t.AsRecord().String()
}

//	func (t *Type) Format(s fmt.State, c rune) {
//		switch c {
//		case 'v':
//			fmt.Fprintf(s, "%+v", t.)
//		case 's':
//			fmt.Fprintf(s, "%s", t.String())
//		default:
//			fmt.Fprintf(s, "%#v", t)
//		}
//	}
func (t *Type) Eq(other hm.Type) bool {
	otherMod, ok := other.(*Type)
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

func (t *Type) Supertypes() []hm.Type {
	// Object types have their implemented interfaces and unions as
	// supertypes. Interfaces have their parent interfaces (from
	// `interface Foo implements Bar`) as supertypes too.
	if t.Kind != ObjectKind && t.Kind != InterfaceKind {
		return nil
	}
	if len(t.interfaces) == 0 && len(t.unions) == 0 {
		return nil
	}
	result := make([]hm.Type, 0, len(t.interfaces)+len(t.unions))
	for _, iface := range t.interfaces {
		result = append(result, iface.(hm.Type))
	}
	for _, union := range t.unions {
		result = append(result, union.(hm.Type))
	}
	return result
}

// AcceptsCoercionFrom implements hm.Coercible for explicit scalar/enum casts.
// Enums and string-backed scalars (including ID and custom scalars) accept
// coercion from String so runtime materialization can validate/construct the
// target value. Primitive scalars do not accept value-level coercion.
func (t *Type) AcceptsCoercionFrom(other hm.Type) bool {
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
func (m *Type) AddInterface(iface TypeScope) {
	if slices.Contains(m.interfaces, iface) {
		return
	}
	m.interfaces = append(m.interfaces, iface)
}

// GetInterfaces returns the interfaces this type implements
func (m *Type) GetInterfaces() []TypeScope {
	return m.interfaces
}

// AddImplementer adds a type that implements this interface (for interface modules)
func (m *Type) AddImplementer(impl TypeScope) {
	if slices.Contains(m.implementers, impl) {
		return
	}
	m.implementers = append(m.implementers, impl)
}

// GetImplementers returns the types that implement this interface (for interface modules)
func (m *Type) GetImplementers() []TypeScope {
	return m.implementers
}

// ImplementsInterface checks if this type implements the given interface
func (m *Type) ImplementsInterface(iface TypeScope) bool {
	return slices.Contains(m.interfaces, iface)
}

// AddMember adds a member type to this union (for union modules).
func (m *Type) AddMember(member TypeScope) {
	if slices.Contains(m.members, member) {
		return
	}
	m.members = append(m.members, member)
}

// LinkMember adds a union member and records the reverse relationship on the
// member. Only call this when the member module is owned by the same local
// environment; Prelude modules are shared process-wide and must not record
// per-module union declarations.
func (m *Type) LinkMember(member TypeScope) {
	m.AddMember(member)
	if memberMod, ok := member.(*Type); ok {
		if slices.Contains(memberMod.unions, TypeScope(m)) {
			return
		}
		memberMod.unions = append(memberMod.unions, m)
	}
}

// GetMembers returns the member types of this union (for union modules)
func (m *Type) GetMembers() []TypeScope {
	return m.members
}

// HasMember checks if this union contains the given type as a member
func (m *Type) HasMember(t TypeScope) bool {
	return slices.Contains(m.members, t)
}

// GetUnions returns the unions this type is a member of
func (m *Type) GetUnions() []TypeScope {
	return m.unions
}

// CheckTypeConflict checks if a type-level symbol has import conflicts.
// Returns the list of imports that provide it (empty if no conflict or not tracked).
func (m *Type) CheckTypeConflict(symbolName string) []string {
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
func (m *Type) CheckValueConflict(symbolName string) []string {
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
func (m *Type) CheckDirectiveConflict(directiveName string) []string {
	if origin, found := m.LocalDirectiveOrigin(directiveName); found {
		return origin.ConflictingImports()
	}
	if m.Parent != nil {
		return m.Parent.CheckDirectiveConflict(directiveName)
	}
	return nil
}

// validateFieldImplementation validates that a object field correctly implements an interface field
// according to GraphQL interface implementation rules:
// - Return types must be covariant (implementation can be more specific)
// - Argument types must be contravariant (implementation can be more general)
// - All interface arguments must be present
// - Additional arguments must be optional
func validateFieldImplementation(fieldName string, ifaceFieldType, objectFieldType hm.Type, ifaceName, objectName string) error {
	// This is schema-level compatibility, not a value handoff, so use pure
	// subtyping (no value-level scalar coercions such as String -> ID).
	// Both must be function types (fields in GraphQL are represented as functions)
	ifaceFn, ifaceIsFn := ifaceFieldType.(*hm.FunctionType)
	objectFn, objectIsFn := objectFieldType.(*hm.FunctionType)

	// If interface field is not a function, object field must match exactly
	if !ifaceIsFn {
		if !objectIsFn {
			// Both are non-function types - check covariance
			if !hm.IsSubtypeOf(objectFieldType, ifaceFieldType) {
				return fmt.Errorf("field %q: type %s is not compatible with interface type %s",
					fieldName, objectFieldType, ifaceFieldType)
			}
			return nil
		}
		return fmt.Errorf("field %q: object has function type but interface does not", fieldName)
	}

	// Interface field is a function
	// Check if it's a zero-argument function (common for GraphQL fields and properties)
	isZeroArgFn := false
	if ifaceFn != nil {
		if rt, ok := ifaceFn.Arg().(*RecordType); ok {
			isZeroArgFn = len(rt.Fields) == 0
		}
	}

	// If interface has a zero-arg function and object has a simple field, unwrap and compare
	if isZeroArgFn && !objectIsFn {
		// Unwrap the function to get the return type
		ifaceRetType := ifaceFn.Ret(false)
		// Compare the return type with the object field type
		if !hm.IsSubtypeOf(objectFieldType, ifaceRetType) {
			return fmt.Errorf("field %q: type %s is not compatible with interface type %s",
				fieldName, objectFieldType, ifaceRetType)
		}
		return nil
	}

	// Interface field is a function - object field must also be a function
	if !objectIsFn {
		return fmt.Errorf("field %q: interface has function type but object does not", fieldName)
	}

	// Validate return type (covariant - object can return more specific type)
	objectRetType := objectFn.Ret(false)
	ifaceRetType := ifaceFn.Ret(false)

	if !hm.IsSubtypeOf(objectRetType, ifaceRetType) {
		return fmt.Errorf("field %q: return type %s is not compatible with interface return type %s (covariance required)",
			fieldName, objectRetType, ifaceRetType)
	}

	// Validate arguments (contravariant - object can accept more general types)
	ifaceArgs, ifaceArgsOk := ifaceFn.Arg().(*RecordType)
	objectArgs, objectArgsOk := objectFn.Arg().(*RecordType)

	if !ifaceArgsOk || !objectArgsOk {
		// Arguments must be records
		return fmt.Errorf("field %q: arguments must be record types", fieldName)
	}

	// Check that all interface arguments are present in object
	for _, ifaceArg := range ifaceArgs.Fields {
		objectArgScheme, found := objectArgs.SchemeOf(ifaceArg.Key)
		if !found {
			return fmt.Errorf("field %q: missing argument %q required by interface", fieldName, ifaceArg.Key)
		}

		// Validate argument type compatibility (contravariant)
		objectArgType, _ := objectArgScheme.Type()
		ifaceArgType, _ := ifaceArg.Value.Type()

		// For contravariance: object arg type must be a supertype of interface arg type
		// This means: if interface requires String!, object can accept String or String!
		// But if interface requires String, object must accept String (can't require String!)
		if !hm.IsSupertypeOf(objectArgType, ifaceArgType) {
			return fmt.Errorf("field %q, argument %q: type %s is not compatible with interface type %s (contravariance required)",
				fieldName, ifaceArg.Key, objectArgType, ifaceArgType)
		}
	}

	// Check that any additional arguments in object are optional
	for _, objectArg := range objectArgs.Fields {
		// Check if this argument exists in the interface
		_, found := ifaceArgs.SchemeOf(objectArg.Key)
		if !found {
			// Additional argument - must be optional (nullable or has default)
			objectArgType, _ := objectArg.Value.Type()
			if _, isNonNull := objectArgType.(hm.NonNullType); isNonNull {
				return fmt.Errorf("field %q, argument %q: additional arguments not in interface must be optional (nullable or have default)",
					fieldName, objectArg.Key)
			}
		}
	}

	return nil
}
