package dang

import (
	"fmt"
	"iter"
	"log/slog"
	"sort"
	"strings"

	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
)

type Env interface {
	hm.Env
	hm.Type
	NamedType(string) (Env, bool)
	AddClass(string, Env)
	SetDocString(string, string)
	GetDocString(string) (string, bool)
	SetDirectives(string, []*DirectiveApplication)
	GetDirectives(string) []*DirectiveApplication
	SetModuleDocString(string)
	GetModuleDocString() string
	SetVisibility(string, Visibility)
	LocalSchemeOf(string) (*hm.Scheme, bool)
	AddDirective(string, *DirectiveDecl)
	GetDirective(string) (*DirectiveDecl, bool)
	Bindings(visibility Visibility) iter.Seq2[string, *hm.Scheme]
}

// ModuleKind represents the kind of module
type ModuleKind int

const (
	ObjectKind ModuleKind = iota
	EnumKind
	ScalarKind
	InterfaceKind
)

func ModuleKindFromGraphQLKind(typeKind introspection.TypeKind) (ModuleKind, error) {
	switch typeKind {
	case introspection.TypeKindScalar:
		return ScalarKind, nil
	case introspection.TypeKindObject:
		return ObjectKind, nil
	case introspection.TypeKindInterface:
		return InterfaceKind, nil
	// case introspection.TypeKindUnion:
	// 	slog.Warn("unsupported union type; skipping", "type", t.Name)
	// 	// return UnionKind
	case introspection.TypeKindEnum:
		return EnumKind, nil
	case introspection.TypeKindInputObject:
		// TODO: adjust once we support these
		return ObjectKind, nil
	default:
		return -1, fmt.Errorf("unsupported GraphQL type kind: %s", typeKind)
	}
}

// TODO: is this just ClassType? are Classes just named Envs?
type Module struct {
	Named string
	Kind  ModuleKind

	Parent Env

	classes         map[string]Env
	vars            map[string]*hm.Scheme
	visibility      map[string]Visibility
	directives      map[string]*DirectiveDecl
	slotDirectives  map[string][]*DirectiveApplication
	docStrings      map[string]string
	moduleDocString string

	// Type-level dynamic scope type
	dynamicScopeType hm.Type

	// Interface tracking
	interfaces   []Env // Interfaces this type implements
	implementers []Env // Types that implement this interface (for interface modules)
}

func NewModule(name string, kind ModuleKind) *Module {
	env := &Module{
		Named:           name,
		Kind:            kind,
		classes:         make(map[string]Env),
		vars:            make(map[string]*hm.Scheme),
		visibility:      make(map[string]Visibility),
		directives:      make(map[string]*DirectiveDecl),
		slotDirectives:  make(map[string][]*DirectiveApplication),
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
	Prelude = NewModule("Prelude", ObjectKind)

	// Install built-in types
	Prelude.AddClass("ID", IDType)
	Prelude.AddClass("String", StringType)
	Prelude.AddClass("Int", IntType)
	Prelude.AddClass("Float", FloatType)
	Prelude.AddClass("Boolean", BooleanType)
	Prelude.AddClass("List", ListTypeModule)

	// Register standard library builtins
	registerStdlib()

	// Register builtin function types from the registry
	registerBuiltinTypes()
}

func NewEnv(schema *introspection.Schema) Env {
	mod := NewModule("", ObjectKind)
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
			kind, err := ModuleKindFromGraphQLKind(t.Kind)
			if err != nil {
				slog.Warn("skipping unsupported type", "type", t.Name, "kind", t.Kind, "error", err)
				continue
			}
			sub = NewModule(t.Name, kind)
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
				mod.Add(t.Name, hm.NewScheme(nil, NonNull(sub)))
				mod.SetVisibility(t.Name, PublicVisibility)
			}
		}
	}

	// Make custom scalar types available as values in the module
	for _, t := range schema.Types {
		if t.Kind == introspection.TypeKindScalar {
			// Skip built-in scalars (String, Int, Float, Boolean, ID)
			if t.Name == "String" || t.Name == "Int" || t.Name == "Float" || t.Name == "Boolean" || t.Name == "ID" {
				continue
			}
			sub, found := env.NamedType(t.Name)
			if !found {
				// Create a scalar type module
				sub = NewModule(t.Name, ScalarKind)
				if t.Description != "" {
					sub.SetModuleDocString(t.Description)
				}
				env.AddClass(t.Name, sub)
			}
			// Add the scalar type as a scheme
			mod.Add(t.Name, hm.NewScheme(nil, sub))
			mod.SetVisibility(t.Name, PublicVisibility)
		}
	}

	// Make interface types available as values in the module
	for _, t := range schema.Types {
		if t.Kind == introspection.TypeKindInterface {
			sub, found := env.NamedType(t.Name)
			if found {
				// Add the interface type as a scheme that represents the module itself
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
				install.Add(enumVal.Name, hm.NewScheme(nil, NonNull(install)))
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

	// Link interface implementations
	for _, t := range schema.Types {
		// Skip types that don't implement any interfaces
		if len(t.Interfaces) == 0 {
			continue
		}

		// Get the implementing type module
		implType, found := env.NamedType(t.Name)
		if !found {
			continue
		}

		// For each interface this type implements
		for _, iface := range t.Interfaces {
			// Look up the interface module
			ifaceModule, found := env.NamedType(iface.Name)
			if !found {
				slog.Warn("interface not found", "interface", iface.Name, "implementer", t.Name)
				continue
			}

			// Link them together
			if implMod, ok := implType.(*Module); ok {
				implMod.AddInterface(ifaceModule)
				slog.Debug("linked interface implementation", "type", t.Name, "interface", iface.Name)
			}
			if ifaceMod, ok := ifaceModule.(*Module); ok {
				ifaceMod.AddImplementer(implType)
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
	mod := NewModule(e.Named, e.Kind)
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
		slog.Debug("adding builtin function", "function", def.Name)
		Prelude.Add(def.Name, hm.NewScheme(nil, fnType))
	})

	// Register all builtin method types
	for _, receiverType := range []*Module{StringType, IntType, FloatType, BooleanType, ListTypeModule} {
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
	// Only object types have supertypes (their interfaces)
	if t.Kind != ObjectKind {
		return nil
	}
	if len(t.interfaces) == 0 {
		return nil
	}
	// Convert []Env to []Type
	result := make([]Type, len(t.interfaces))
	for i, iface := range t.interfaces {
		result[i] = iface.(Type)
	}
	return result
}

// AddInterface adds an interface that this type implements
func (m *Module) AddInterface(iface Env) {
	m.interfaces = append(m.interfaces, iface)
}

// GetInterfaces returns the interfaces this type implements
func (m *Module) GetInterfaces() []Env {
	return m.interfaces
}

// AddImplementer adds a type that implements this interface (for interface modules)
func (m *Module) AddImplementer(impl Env) {
	m.implementers = append(m.implementers, impl)
}

// GetImplementers returns the types that implement this interface (for interface modules)
func (m *Module) GetImplementers() []Env {
	return m.implementers
}

// ImplementsInterface checks if this type implements the given interface
func (m *Module) ImplementsInterface(iface Env) bool {
	for _, impl := range m.interfaces {
		if impl == iface {
			return true
		}
	}
	return false
}

// validateFieldImplementation validates that a class field correctly implements an interface field
// according to GraphQL interface implementation rules:
// - Return types must be covariant (implementation can be more specific)
// - Argument types must be contravariant (implementation can be more general)
// - All interface arguments must be present
// - Additional arguments must be optional
func validateFieldImplementation(fieldName string, ifaceFieldType, classFieldType hm.Type, ifaceName, className string) error {
	// Both must be function types (fields in GraphQL are represented as functions)
	ifaceFn, ifaceIsFn := ifaceFieldType.(*hm.FunctionType)
	classFn, classIsFn := classFieldType.(*hm.FunctionType)

	// If interface field is not a function, class field must match exactly
	if !ifaceIsFn {
		if !classIsFn {
			// Both are non-function types - check covariance
			if !isSubtypeOf(classFieldType, ifaceFieldType) {
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
		if !isSubtypeOf(classFieldType, ifaceRetType) {
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

	if !isSubtypeOf(classRetType, ifaceRetType) {
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
		if !isSupertypeOf(classArgType, ifaceArgType) {
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

// isSubtypeOf checks if sub is a subtype of super (covariance check)
// For return types: String! is a subtype of String, User is a subtype of Node
func isSubtypeOf(sub, super hm.Type) bool {
	_, err := hm.Assignable(sub, super)
	return err == nil
}

// isSupertypeOf checks if super is a supertype of sub (contravariance check)
// For argument types: String is a supertype of String!, Node is a supertype of User
func isSupertypeOf(super, sub hm.Type) bool {
	// Contravariance is just flipped subtyping
	return isSubtypeOf(sub, super)
}

// findCommonSupertype finds the least common supertype of two types.
// This is used for inferring list types with heterogeneous elements.
// Returns nil if no common supertype exists (other than Any).
func findCommonSupertype(t1, t2 hm.Type) hm.Type {
	// If types are equal, return either one
	if t1.Eq(t2) {
		return t1
	}

	// If one is a subtype of the other, return the supertype
	if isSubtypeOf(t1, t2) {
		return t2
	}
	if isSubtypeOf(t2, t1) {
		return t1
	}

	// Build supertype sets for both types
	supers1 := buildSupertypeSet(t1)
	supers2 := buildSupertypeSet(t2)

	// Find common supertypes
	var common []hm.Type
	for super := range supers1 {
		if supers2[super] {
			common = append(common, super)
		}
	}

	if len(common) == 0 {
		return nil
	}

	// Find the most specific common supertype (LUB - Least Upper Bound)
	// This is the one that is a subtype of all others
	for _, candidate := range common {
		isLeast := true
		for _, other := range common {
			if candidate.Eq(other) {
				continue
			}
			// If candidate is a supertype of other, then other is more specific
			if isSubtypeOf(other, candidate) {
				isLeast = false
				break
			}
		}
		if isLeast {
			return candidate
		}
	}

	// Fallback: return first common supertype (shouldn't reach here if LUB exists)
	return common[0]
}

// buildSupertypeSet builds the transitive closure of all supertypes
func buildSupertypeSet(t hm.Type) map[hm.Type]bool {
	result := make(map[hm.Type]bool)
	result[t] = true

	for _, super := range t.Supertypes() {
		for s := range buildSupertypeSet(super) {
			result[s] = true
		}
	}

	return result
}
