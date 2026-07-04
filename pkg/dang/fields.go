package dang

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

type FieldDecl struct {
	InferredTypeHolder
	Name         *Symbol
	Type_        TypeNode
	Value        Node
	Visibility   Visibility
	Directives   []*DirectiveApplication
	DocString    string
	IsBlockParam bool // True if this is a block parameter (prefixed with &)
	Loc          *SourceLocation

	// A type inferred from context, i.e. a lambda passed as a function argument.
	ContextInferredType hm.Type
}

var _ Node = (*FieldDecl)(nil)
var _ Evaluator = (*FieldDecl)(nil)
var _ Hoister = (*FieldDecl)(nil)

func (s *FieldDecl) DeclaredSymbols() []string {
	return []string{s.Name.Name} // Field declarations declare their name
}

func (s *FieldDecl) ReferencedSymbols() []string {
	var symbols []string
	if s.Value != nil {
		symbols = append(symbols, s.Value.ReferencedSymbols()...)
	}
	if s.Type_ != nil {
		symbols = append(symbols, s.Type_.ReferencedSymbols()...)
	}
	for _, directive := range s.Directives {
		symbols = append(symbols, directive.ReferencedSymbols()...)
	}
	return symbols
}

func (s *FieldDecl) Body() hm.Expression {
	// TODO(vito): return Value? unclear how Body is used
	return s
}

func (s *FieldDecl) GetSourceLocation() *SourceLocation { return s.Loc }

func (s *FieldDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	// If the field value is a hoister (e.g. wraps a FunDecl), delegate while
	// preserving the field's name and metadata. This is the signature boundary:
	// function bodies are not inferred, but callers can see the function type.
	if funDecl, ok := s.Value.(*FunDecl); ok {
		if funDecl.Named == "" {
			funDecl.Named = s.Name.Name
		}
		if err := funDecl.Hoist(ctx, env, fresh, pass); err != nil {
			return err
		}
		if pass == 0 {
			s.SetInferredType(funDecl.Inferred)
			if e, ok := env.(TypeScope); ok {
				e.SetVisibility(s.Name.Name, s.Visibility)
				if s.DocString != "" {
					e.SetDocString(s.Name.Name, s.DocString)
				}
				directives := s.Directives
				if len(directives) == 0 {
					directives = funDecl.Directives
				}
				if len(directives) > 0 {
					e.SetDirectives(s.Name.Name, directives)
				}
			}
		}
		return nil
	}
	if hoister, ok := s.Value.(Hoister); ok {
		return hoister.Hoist(ctx, env, fresh, pass)
	}

	// For non-function fields, register the type during pass 0 so that
	// sibling declarations (e.g. method default-value expressions) can
	// reference it before full inference runs. This mirrors the declaration
	// pass for function signatures.
	//
	// The type is determined from the explicit annotation if present,
	// otherwise from the value if it implements Constant (literals whose
	// type is known without consulting the environment). Computed values are
	// intentionally not inferred at the hoist boundary.
	if pass == 0 {
		fieldType, err := s.signatureType(ctx, env, fresh, false)
		if err != nil {
			return err
		}
		if fieldType != nil {
			env.Add(s.Name.Name, hm.NewScheme(nil, fieldType))
			s.SetInferredType(fieldType)
			if e, ok := env.(TypeScope); ok {
				e.SetVisibility(s.Name.Name, s.Visibility)
				if s.DocString != "" {
					e.SetDocString(s.Name.Name, s.DocString)
				}
				if len(s.Directives) > 0 {
					e.SetDirectives(s.Name.Name, s.Directives)
				}
			}
		}
	}

	return nil
}

func (s *FieldDecl) signatureType(ctx context.Context, env hm.Env, fresh hm.Fresher, allowComputed bool) (hm.Type, error) {
	if s.Type_ != nil {
		fieldType, err := s.Type_.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}
		return fieldType, nil
	}
	if constant, ok := s.Value.(Constant); ok {
		return constant.ConstantType(), nil
	}
	if s.Value == nil {
		if allowComputed {
			return fresh.Fresh(), nil
		}
		return nil, nil
	}
	if allowComputed {
		return s.Value.Infer(ctx, env, fresh)
	}
	return nil, nil
}

func (s *FieldDecl) DeclareKnownSignature(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	fieldType, err := s.signatureType(ctx, env, fresh, false)
	if err != nil {
		return nil, err
	}
	if fieldType == nil {
		return nil, nil
	}
	env.Add(s.Name.Name, hm.NewScheme(nil, fieldType))
	s.SetInferredType(fieldType)
	if e, ok := env.(TypeScope); ok {
		e.SetVisibility(s.Name.Name, s.Visibility)
		if s.DocString != "" {
			e.SetDocString(s.Name.Name, s.DocString)
		}
		if len(s.Directives) > 0 {
			e.SetDirectives(s.Name.Name, s.Directives)
		}
	}
	return fieldType, nil
}

func (s *FieldDecl) DeclareSignature(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	fieldType, err := s.signatureType(ctx, env, fresh, true)
	if err != nil {
		return nil, err
	}
	if fieldType == nil {
		fieldType = fresh.Fresh()
	}
	env.Add(s.Name.Name, hm.NewScheme(nil, fieldType))
	s.SetInferredType(fieldType)
	if e, ok := env.(TypeScope); ok {
		e.SetVisibility(s.Name.Name, s.Visibility)
		if s.DocString != "" {
			e.SetDocString(s.Name.Name, s.DocString)
		}
		if len(s.Directives) > 0 {
			e.SetDirectives(s.Name.Name, s.Directives)
		}
	}
	return fieldType, nil
}

func (s *FieldDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var err error

	var definedType hm.Type
	if s.Type_ != nil {
		definedType, err = s.Type_.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}
	}

	if s.ContextInferredType != nil {
		if definedType != nil {
			if _, err := hm.Assignable(definedType, s.ContextInferredType); err != nil {
				return nil, NewInferError(err, s)
			}
		} else {
			definedType = s.ContextInferredType
		}
	}

	var inferredType hm.Type
	if s.Value != nil {
		inferredType, err = s.Value.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		if definedType != nil {
			if _, err := assignableForValue(inferredType, definedType, s.Value); err != nil {
				return nil, NewInferError(err, s.Value)
			}
			s.Value = wrapCoerce(s.Value, definedType, s.Name.Name)
		} else {
			definedType = inferredType
		}
	}

	if definedType == nil {
		definedType = fresh.Fresh()
	}

	if e, ok := env.(TypeScope); ok {
		cur, defined := e.LocalSchemeOf(s.Name.Name)
		if defined {
			curT, curMono := cur.Type()
			if !curMono {
				return nil, fmt.Errorf("FieldDecl.Infer: TODO: type is not monomorphic")
			}

			if !definedType.Eq(curT) {
				return nil, WrapInferError(
					fmt.Errorf("FieldDecl.Infer: %q already defined as %s, trying to redefine as %s", s.Name.Name, curT, definedType),
					s,
				)
			}
		}

		e.SetVisibility(s.Name.Name, s.Visibility)

		// Store doc string if present
		if s.DocString != "" {
			e.SetDocString(s.Name.Name, s.DocString)
		}

		if len(s.Directives) > 0 {
			e.SetDirectives(s.Name.Name, s.Directives)
		}
	}

	// Validate directive applications
	for _, directive := range s.Directives {
		_, err := directive.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("FieldDecl.Infer: directive validation: %w", err)
		}
	}

	env.Add(s.Name.Name, hm.NewScheme(nil, definedType))
	s.SetInferredType(definedType)
	return definedType, nil
}

// EvalValue computes the field's value without binding it to the scope.
// Callers must invoke Publish to make the value visible to siblings. Object
// literals split these so a layer of independent fields can be evaluated
// concurrently and then published in deterministic source order.
func (s *FieldDecl) EvalValue(ctx context.Context, scope ValueScope) (Value, error) {
	if s.Value == nil {
		// Check if this is a required (non-null) type without a value
		// This is a runtime error - required types must have values
		if inferredType := s.GetInferredType(); inferredType != nil {
			if _, isNonNull := inferredType.(hm.NonNullType); isNonNull {
				return nil, fmt.Errorf("required field %q (type %s) has no value", s.Name.Name, inferredType)
			}
		}

		// If no value is provided, this is just a type declaration. The caller
		// will publish NullValue as a placeholder.
		return NullValue{}, nil
	}

	// Evaluate the value expression with proper error context. The Value
	// node is wrapped in a Coerce by FieldDecl.Infer when the field has an
	// explicit type, so materialization happens during EvalNode.
	return EvalNode(ctx, scope, s.Value)
}

// Publish binds the field's computed value into scope, making it visible to
// sibling declarations and the resulting object.
func (s *FieldDecl) Publish(scope ValueScope, val Value) {
	scope.Bind(s.Name.Name, val, s.Visibility)
}

func (s *FieldDecl) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, s, func() (Value, error) {
		if val, defined := scope.LookupLocal(s.Name.Name); defined {
			// Already defined (e.g. through constructor). The value reached us
			// through a Coerce-wrapped argument so it is already materialized;
			// don't re-evaluate or re-bind it.
			return val, nil
		}

		val, err := s.EvalValue(ctx, scope)
		if err != nil {
			return nil, err
		}
		s.Publish(scope, val)
		return val, nil
	})
}

func (s *FieldDecl) Walk(fn func(Node) bool) {
	if !fn(s) {
		return
	}
	for _, d := range s.Directives {
		d.Walk(fn)
	}
	// TypeNode doesn't have Walk method - skip
	if s.Value != nil {
		s.Value.Walk(fn)
	}
}

type ObjectDecl struct {
	InferredTypeHolder
	Name       *Symbol
	Value      *Block
	Implements []*Symbol
	Visibility Visibility
	Directives []*DirectiveApplication
	DocString  string
	Loc        *SourceLocation

	Inferred          *Type
	ConstructorFnType *hm.FunctionType
}

// NewConstructorDecl represents an explicit `new(...) { ... }` constructor
type NewConstructorDecl struct {
	InferredTypeHolder
	Args       []*FieldDecl
	BlockParam *FieldDecl
	BodyBlock  *Block
	DocString  string
	Loc        *SourceLocation

	Inferred *hm.FunctionType
}

var _ Node = &NewConstructorDecl{}
var _ Evaluator = &NewConstructorDecl{}

func (n *NewConstructorDecl) DeclaredSymbols() []string {
	return nil // new doesn't declare a symbol, it's handled specially by ObjectDecl
}

func (n *NewConstructorDecl) ReferencedSymbols() []string {
	var symbols []string
	for _, arg := range n.Args {
		symbols = append(symbols, arg.ReferencedSymbols()...)
	}
	if n.BlockParam != nil {
		symbols = append(symbols, n.BlockParam.ReferencedSymbols()...)
	}
	symbols = append(symbols, n.BodyBlock.ReferencedSymbols()...)
	return symbols
}

func (n *NewConstructorDecl) Body() hm.Expression { return n.BodyBlock }

func (n *NewConstructorDecl) GetSourceLocation() *SourceLocation { return n.Loc }

func (n *NewConstructorDecl) Walk(fn func(Node) bool) {
	if !fn(n) {
		return
	}
	for _, arg := range n.Args {
		arg.Walk(fn)
	}
	if n.BlockParam != nil {
		n.BlockParam.Walk(fn)
	}
	n.BodyBlock.Walk(fn)
}

// Infer returns an error since new() is only valid inside a object body.
// When used inside a object, it is inferred by ObjectDecl.inferNewConstructor instead.
func (n *NewConstructorDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return nil, NewInferError(fmt.Errorf("new() constructor can only be defined inside a type body"), n)
}

// Eval is a no-op since NewConstructorDecl is evaluated as part of ConstructorFunction.Call
func (n *NewConstructorDecl) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	// The new() constructor body is evaluated by ConstructorFunction.Call
	return NullValue{}, nil
}

var _ Node = &ObjectDecl{}
var _ Evaluator = &ObjectDecl{}

func (c *ObjectDecl) DeclaredSymbols() []string {
	return []string{c.Name.Name} // Object declarations declare their name
}

func (c *ObjectDecl) ReferencedSymbols() []string {
	var symbols []string
	// Object declarations reference symbols from their body (the Block)
	symbols = append(symbols, c.Value.ReferencedSymbols()...)
	// And from directive applications
	for _, directive := range c.Directives {
		symbols = append(symbols, directive.ReferencedSymbols()...)
	}
	return symbols
}

func (c *ObjectDecl) Body() hm.Expression { return c.Value }

func (c *ObjectDecl) GetSourceLocation() *SourceLocation { return c.Loc }

var _ Hoister = &ObjectDecl{}

// Imported schema types are installed into the local type map so they can be
// referenced unqualified. A real type declaration with the same name must
// replace an unqualified imported alias, not mutate the imported schema type.
// Qualified import aliases like Dagger are protected so Dagger.Container keeps
// resolving through the imported module.
func localTypeShadowsImport(env TypeScope, name string) bool {
	origin, found := env.LocalTypeOrigin(name)
	return found && origin.IsUnqualifiedImport()
}

func localTypeIsQualifiedImport(env TypeScope, name string) bool {
	origin, found := env.LocalTypeOrigin(name)
	return found && origin.Kind == BindingOriginImport && origin.Qualified
}

func declareLocalType(env TypeScope, name string, kind Kind) (*Type, error) {
	if existing, found := env.LocalNamedType(name); found {
		if localTypeShadowsImport(env, name) {
			// Local declarations intentionally shadow unqualified imports.
		} else if localTypeIsQualifiedImport(env, name) {
			return nil, fmt.Errorf("type %q conflicts with import alias", name)
		} else if mod, ok := existing.(*Type); ok {
			return mod, nil
		} else {
			return nil, fmt.Errorf("type %q conflicts with existing type", name)
		}
	}

	mod := NewType(name, kind)
	env.AddObject(name, mod)
	// A scalar whose name matches a builtin scalar (e.g. JSON) doubles as that
	// scalar's namespace: staple Dang's members onto its type.
	attachBuiltinSchemes(mod)
	return mod, nil
}

// findNewConstructorIn returns the NewConstructorDecl among a body block's
// forms, if any. Shared by object and scalar declarations.
func findNewConstructorIn(block *Block) *NewConstructorDecl {
	if block == nil {
		return nil
	}
	for _, form := range block.Forms {
		if newDecl, ok := form.(*NewConstructorDecl); ok {
			return newDecl
		}
	}
	return nil
}

// formsWithoutNew returns a body block's forms excluding any
// NewConstructorDecl. Shared by object and scalar declarations.
func formsWithoutNew(block *Block) []Node {
	if block == nil {
		return nil
	}
	var forms []Node
	for _, form := range block.Forms {
		if _, ok := form.(*NewConstructorDecl); !ok {
			forms = append(forms, form)
		}
	}
	return forms
}

// findNewConstructor returns the NewConstructorDecl from the object body, if any
func (c *ObjectDecl) findNewConstructor() *NewConstructorDecl {
	return findNewConstructorIn(c.Value)
}

// bodyFormsWithoutNew returns the object body forms excluding the NewConstructorDecl
func (c *ObjectDecl) bodyFormsWithoutNew() []Node {
	var forms []Node
	for _, form := range c.Value.Forms {
		if _, ok := form.(*NewConstructorDecl); !ok {
			forms = append(forms, form)
		}
	}
	return forms
}

func (c *ObjectDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	mod, ok := env.(TypeScope)
	if !ok {
		return fmt.Errorf("ObjectDecl.Hoist: environment does not support module operations")
	}

	object, declareErr := declareLocalType(mod, c.Name.Name, ObjectKind)
	if declareErr != nil {
		return WrapInferError(declareErr, c.Name)
	}
	c.Inferred = object
	c.SetInferredType(object)
	if c.DocString != "" {
		mod.SetDocString(c.Name.Name, c.DocString)
		object.SetTypeDocString(c.DocString)
	}

	// Pass 0 must only register the type name. Other top-level types may refer
	// to this object before its file is reached, so anything that resolves field
	// annotations, constructor params, or implemented interfaces has to wait
	// until every type has had this registration pass.
	if pass == 0 {
		return nil
	}

	inferTypeScope := &OverlayTypeScope{
		primary: object,
		lexical: env.(TypeScope),
	}

	// Build constructor type: use explicit new() if present, otherwise derive from fields
	newDecl := c.findNewConstructor()
	var constructorParams []*FieldDecl
	var constructorBlockParam *FieldDecl
	if newDecl != nil {
		constructorParams = newDecl.Args
		constructorBlockParam = newDecl.BlockParam
	} else {
		constructorParams = c.extractConstructorParameters()
	}
	constructorType, err := c.buildConstructorType(ctx, inferTypeScope, constructorParams, constructorBlockParam, object, fresh)
	if err != nil {
		return err
	}
	c.ConstructorFnType = constructorType

	// Add the constructor function type to the environment
	constructorScheme := hm.NewScheme(nil, constructorType)
	env.Add(c.Name.Name, constructorScheme)

	// Link the implementation after all interface type names have been
	// registered by pass 0.
	if len(c.Implements) > 0 {
		objectMod := object
		for _, ifaceSym := range c.Implements {
			ifaceType, found := mod.NamedType(ifaceSym.Name)
			if !found {
				return WrapInferError(
					fmt.Errorf("interface %s not found", ifaceSym.Name),
					ifaceSym,
				)
			}

			ifaceMod, ok := ifaceType.(*Type)
			if !ok || ifaceMod.Kind != InterfaceKind {
				return WrapInferError(
					fmt.Errorf("%s is not an interface", ifaceSym.Name),
					ifaceSym,
				)
			}

			// Add "blindly" initially, we'll validate later. The reverse
			// implementer index is only maintained for locally owned interfaces;
			// Prelude interfaces are shared process-wide and must not be mutated
			// by per-module declarations.
			objectMod.AddInterface(ifaceType)
			if localIface, found := mod.LocalNamedType(ifaceSym.Name); found && localIface == ifaceType {
				ifaceMod.AddImplementer(objectMod)
			}
		}
	}

	// Set dynamic scope type to the object type
	selfType := hm.NonNullType{Type: object}
	object.SetDynamicScopeType(selfType)

	// Hoist body forms directly (not via Block.Hoist which clones the env)
	// to register method signatures on the object module. This enables
	// forward references between types defined in any order. We hoist at
	// pass 0 so that FunDecl.Hoist registers signatures and
	// FieldDecl.Hoist registers typed field declarations.
	bodyForms := c.bodyFormsWithoutNew()
	for _, form := range bodyForms {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, inferTypeScope, fresh, 0); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *ObjectDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod, ok := env.(TypeScope)
	if !ok {
		return nil, fmt.Errorf("ObjectDecl.Infer: environment does not support module operations")
	}

	object, declareErr := declareLocalType(mod, c.Name.Name, ObjectKind)
	if declareErr != nil {
		return nil, WrapInferError(declareErr, c.Name)
	}

	// Store doc string for the object name in the environment
	if c.DocString != "" {
		mod.SetDocString(c.Name.Name, c.DocString)
		object.SetTypeDocString(c.DocString)
	}

	// Set this early so we can at least partially infer.
	c.Inferred = object

	// Set dynamic scope type to the object type
	selfType := hm.NonNullType{Type: object}
	object.SetDynamicScopeType(selfType)

	// Validate directive applications
	for _, directive := range c.Directives {
		_, err := directive.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("ObjectDecl.Infer: directive validation: %w", err)
		}
	}

	inferTypeScope := &OverlayTypeScope{
		primary: object,
		lexical: env.(TypeScope),
	}

	// Check for fields named "new" — the user likely intended a constructor
	for _, form := range c.Value.Forms {
		if field, ok := form.(*FieldDecl); ok && field.Name.Name == "new" {
			vis := "pub"
			if field.Visibility == PrivateVisibility {
				vis = "let"
			}
			return nil, NewInferError(
				fmt.Errorf("'new' is a constructor, not a method; use `new(...) { ... }` without `%s` or a return type", vis),
				field,
			)
		}
	}

	// Infer body forms (excluding new() which is handled separately)
	bodyForms := c.bodyFormsWithoutNew()
	if _, err := InferFormsWithPhases(ctx, bodyForms, inferTypeScope, fresh); err != nil {
		return nil, err
	}

	// Validate codec field directives (@JSON.field, @YAML.ignore, ...) now that
	// every field's type and directives are known.
	if err := validateCodecFieldDirectives(object); err != nil {
		return nil, err
	}

	// If there's an explicit new(), infer its body with its args in scope.
	// Errors here (e.g. wrong return type) are collected but don't prevent
	// the object from being usable, avoiding cascading type errors.
	newDecl := c.findNewConstructor()
	var newBodyErr error
	if newDecl != nil {
		newBodyErr = c.inferNewConstructor(ctx, newDecl, inferTypeScope, fresh)
	}

	// Validate interface implementations after fields have been inferred
	if len(c.Implements) > 0 {
		objectMod := c.Inferred
		for _, ifaceSym := range c.Implements {
			if err := c.validateInterfaceImplementations(objectMod, mod, ifaceSym); err != nil {
				return nil, err
			}
		}
	}

	return c.ConstructorFnType, newBodyErr
}

// isReservedInterfaceField reports whether an interface field is provided by
// the Dagger runtime rather than the implementer. Dagger synthesizes `id: ID!`
// on every object and interface in a module's schema, so an imported interface
// carries an `id` field that no user-defined type declares (and shouldn't have
// to: Dagger supplies it for implementers too). Locally-declared interfaces
// never have one. Skip it during conformance checks.
func isReservedInterfaceField(field string, scheme *hm.Scheme) bool {
	if field != "id" {
		return false
	}
	t, ok := scheme.Type()
	if !ok {
		return false
	}
	// Interface fields are stored as `() -> T`; unwrap to the return type.
	if fn, ok := t.(*hm.FunctionType); ok {
		t = fn.Ret(false)
	}
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}
	return t.Name() == "ID"
}

// fieldSignature renders a field and its scheme for conformance error
// messages. Method schemes already lead with their parameter list
// (`greet(): String!`); plain value fields need a separator
// (`message: String!`).
func fieldSignature(field string, scheme *hm.Scheme) string {
	sig := scheme.String()
	if strings.HasPrefix(sig, "(") {
		return field + sig
	}
	return field + ": " + sig
}

// validateInterfaceImplementations checks that this type correctly implements all declared interfaces
func (c *ObjectDecl) validateInterfaceImplementations(objectMod *Type, env TypeScope, ifaceSym *Symbol) error {
	ifaceType, found := env.NamedType(ifaceSym.Name)
	if !found {
		// no error; this is raised in Hoist instead
		return nil
	}

	ifaceMod, ok := ifaceType.(*Type)
	if !ok || ifaceMod.Kind != InterfaceKind {
		// no error; this is raised in Hoist instead
		return nil
	}

	var missingFields []string
	// Check that all interface fields are present in the object
	for field, fieldScheme := range ifaceMod.Bindings(PrivateVisibility) {
		if isReservedInterfaceField(field, fieldScheme) {
			continue
		}
		objectFieldScheme, objectHasField := objectMod.SchemeOf(field)
		if !objectHasField {
			missingFields = append(missingFields, field)
			continue
		}

		// Get the types from the schemes
		ifaceFieldType, _ := fieldScheme.Type()
		objectFieldType, _ := objectFieldScheme.Type()

		// Validate field type compatibility
		if err := validateFieldImplementation(field, ifaceFieldType, objectFieldType, ifaceMod.String(), objectMod.String()); err != nil {
			return WrapInferError(err, ifaceSym)
		}
	}

	if len(missingFields) > 0 {
		errs := &InferenceErrors{}
		sort.Strings(missingFields)
		for _, field := range missingFields {
			fieldScheme, _ := ifaceMod.SchemeOf(field)
			errs.Add(WrapInferError(
				fmt.Errorf("object %s is missing `%s`, required by interface %s", objectMod, fieldSignature(field, fieldScheme), ifaceMod),
				ifaceSym,
			))
		}
		return errs
	}

	return nil
}

// extractConstructorParametersAndCleanBody extracts public non-function fields and private
// required fields (no default) as constructor parameters and returns the filtered forms that
// should be evaluated in the object body
func (c *ObjectDecl) extractConstructorParameters() []*FieldDecl {
	var params []*FieldDecl

	for _, form := range c.Value.Forms {
		if field, ok := form.(*FieldDecl); ok {
			// Skip function fields
			if _, isFun := field.Value.(*FunDecl); isFun {
				continue
			}

			// Include public non-function fields as constructor parameters
			if field.Visibility == PublicVisibility {
				params = append(params, field)
				continue
			}

			// Include private fields that are required (no default value)
			if field.Visibility == PrivateVisibility && field.Value == nil {
				params = append(params, field)
			}
		}
	}

	return params
}

// buildConstructorType creates a function type for the constructor based on the parameters
func (c *ObjectDecl) buildConstructorType(ctx context.Context, env hm.Env, params []*FieldDecl, blockParam *FieldDecl, objectType *Type, fresh hm.Fresher) (*hm.FunctionType, error) {
	fnDecl := FunctionBase{
		Args:       params,
		BlockParam: blockParam,
	}
	signatureCtx := contextWithInferFunctionControlBoundary(ctx)
	argEnv := env.Clone()
	args, directives, docStrings, err := fnDecl.declareFunctionSignatureArguments(signatureCtx, argEnv, fresh)
	if err != nil {
		return nil, fmt.Errorf("%s Constructor.Declare: %w", objectType.Named, err)
	}
	argsRec := NewRecordType("", args...)
	argsRec.Directives = directives
	argsRec.DocStrings = docStrings

	constructorType := hm.NewFnType(argsRec, hm.NonNullType{Type: objectType})
	if blockParam != nil {
		blockParamType, err := blockParam.Type_.Infer(signatureCtx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("%s Constructor.Declare block parameter: %w", objectType.Named, err)
		}
		blockType, ok := blockParamType.(*hm.FunctionType)
		if !ok {
			return nil, fmt.Errorf("%s Constructor.Declare: block parameter must be a function type, got %T", objectType.Named, blockParamType)
		}
		constructorType.SetBlock(blockType)
	}
	return constructorType, nil
}

// inferNewConstructor infers the body of an explicit new() constructor
func (c *ObjectDecl) inferNewConstructor(ctx context.Context, newDecl *NewConstructorDecl, env hm.Env, fresh hm.Fresher) error {
	constructorCtx := contextWithInferFunctionControlBoundary(ctx)

	// Create an environment with the constructor args in scope
	newEnv := env.Clone().(*OverlayTypeScope)
	for _, arg := range newDecl.Args {
		// Fully infer constructor arguments here so default expressions are
		// validated during normal inference. Declaration may have recorded the
		// signature without checking computed defaults.
		argType, err := arg.Infer(constructorCtx, newEnv, fresh)
		if err != nil {
			return fmt.Errorf("inferring new() arg %s: %w", arg.Name.Name, err)
		}
		newEnv.Add(arg.Name.Name, hm.NewScheme(nil, argType))
	}
	if newDecl.BlockParam != nil {
		var blockType hm.Type
		if c.ConstructorFnType != nil && c.ConstructorFnType.Block() != nil {
			blockType = c.ConstructorFnType.Block()
		} else {
			var err error
			blockType, err = newDecl.BlockParam.Type_.Infer(constructorCtx, env, fresh)
			if err != nil {
				return fmt.Errorf("inferring new() block parameter %s: %w", newDecl.BlockParam.Name.Name, err)
			}
		}
		newEnv.Add(newDecl.BlockParam.Name.Name, hm.NewScheme(nil, blockType))
	}

	// Infer the new() body with a constructor return target. Constructors are
	// function boundaries for break/continue just like ordinary functions.
	returnTarget := NewInferControlTarget(ReturnFrame)
	bodyCtx := contextWithInferReturnTarget(constructorCtx, returnTarget)
	bodyType, err := newDecl.BodyBlock.Infer(bodyCtx, newEnv, fresh)
	if err != nil {
		return fmt.Errorf("inferring new() body: %w", err)
	}

	// The new() body must return the object type
	expectedType := hm.NonNullType{Type: c.Inferred}
	if _, err := hm.Assignable(bodyType, expectedType); err != nil {
		errorNode := Node(newDecl.BodyBlock)
		if len(newDecl.BodyBlock.Forms) > 0 {
			errorNode = newDecl.BodyBlock.Forms[len(newDecl.BodyBlock.Forms)-1]
		}
		return NewInferError(
			fmt.Errorf("new() must return %s, got %s", expectedType.Name(), bodyType.Name()),
			errorNode,
		)
	}

	for _, ret := range collectReturnStatements(newDecl.BodyBlock, returnTarget) {
		retType := returnValueType(ret)
		if retType == nil {
			continue
		}
		if _, err := hm.Assignable(retType, expectedType); err != nil {
			return NewInferError(
				fmt.Errorf("new() must return %s, got %s", expectedType, retType),
				ret.Value,
			)
		}
	}

	return nil
}

func (c *ObjectDecl) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, c, func() (Value, error) {
		if c.Inferred == nil {
			panic(fmt.Errorf("ObjectDecl.Eval: object %q has not been inferred", c.Name.Name))
		}

		// Set doc string for the object/module itself
		if c.DocString != "" {
			c.Inferred.SetTypeDocString(c.DocString)
		}

		// Find explicit new() or derive constructor from fields
		newDecl := c.findNewConstructor()
		var constructorParams []*FieldDecl
		var constructorBlockParam *FieldDecl
		var newBody *Block
		if newDecl != nil {
			constructorParams = newDecl.Args
			constructorBlockParam = newDecl.BlockParam
			newBody = newDecl.BodyBlock
		} else {
			constructorParams = c.extractConstructorParameters()
		}

		var blockParamName string
		if constructorBlockParam != nil {
			blockParamName = constructorBlockParam.Name.Name
		}

		// Create a constructor function that evaluates the object body when called
		constructor := &ConstructorFunction{
			Closure:         scope,
			ObjectName:      c.Name.Name,
			Parameters:      constructorParams,
			BlockParamName:  blockParamName,
			ObjectType:      c.Inferred,
			ObjectBodyForms: c.bodyFormsWithoutNew(),
			FnType:          c.ConstructorFnType,
			NewBody:         newBody,
		}

		// Add the constructor to the evaluation environment
		scope.Bind(c.Name.Name, constructor, c.Visibility)

		return constructor, nil
	})
}

func (c *ObjectDecl) Walk(fn func(Node) bool) {
	if !fn(c) {
		return
	}
	for _, d := range c.Directives {
		d.Walk(fn)
	}
	c.Value.Walk(fn)
}

type EnumDecl struct {
	InferredTypeHolder
	Name       *Symbol
	Values     []*Symbol
	Visibility Visibility
	Directives []*DirectiveApplication
	DocString  string
	Loc        *SourceLocation

	Inferred *Type
}

var _ Node = &EnumDecl{}
var _ Evaluator = &EnumDecl{}

func (e *EnumDecl) DeclaredSymbols() []string {
	return []string{e.Name.Name}
}

func (e *EnumDecl) ReferencedSymbols() []string {
	return nil // Enum declarations don't reference other symbols
}

func (e *EnumDecl) Body() hm.Expression { return nil }

func (e *EnumDecl) GetSourceLocation() *SourceLocation { return e.Loc }

var _ Hoister = &EnumDecl{}

func (e *EnumDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	mod, ok := env.(TypeScope)
	if !ok {
		return fmt.Errorf("EnumDecl.Hoist: environment does not support module operations")
	}

	enumType, declareErr := declareLocalType(mod, e.Name.Name, EnumKind)
	if declareErr != nil {
		return WrapInferError(declareErr, e.Name)
	}

	e.Inferred = enumType
	e.SetInferredType(enumType)
	if e.DocString != "" {
		mod.SetDocString(e.Name.Name, e.DocString)
	}

	// Add the enum module to the environment so it can be referenced
	// Note: We add the enum type itself, not wrapped in NonNullType, matching GraphQL enum behavior
	enumScheme := hm.NewScheme(nil, NonNull(enumType))
	env.Add(e.Name.Name, enumScheme)

	if pass > 0 {
		// Add each enum value as a field in the enum module
		for _, value := range e.Values {
			// Each enum value has the type of the enum itself (not wrapped)
			valueScheme := hm.NewScheme(nil, NonNull(enumType))
			enumType.Add(value.Name, valueScheme)
			enumType.SetVisibility(value.Name, PublicVisibility)

		}

		// Add the values() method that returns all enum values as a list
		valuesType := hm.NewScheme(nil, NonNull(ListType{NonNull(enumType)}))
		enumType.Add("values", valuesType)
		enumType.SetVisibility("values", PublicVisibility)
	}

	return nil
}

func (e *EnumDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod, ok := env.(TypeScope)
	if !ok {
		return nil, fmt.Errorf("EnumDecl.Infer: environment does not support module operations")
	}

	enumType, declareErr := declareLocalType(mod, e.Name.Name, EnumKind)
	if declareErr != nil {
		return nil, WrapInferError(declareErr, e.Name)
	}
	if e.DocString != "" {
		mod.SetDocString(e.Name.Name, e.DocString)
	}

	e.Inferred = enumType
	e.SetInferredType(enumType)

	return enumType, nil
}

func (e *EnumDecl) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	// Create a module value for the enum
	enumModule := NewObject(e.Inferred)

	// Add each enum value to the module
	enumValues := make([]Value, len(e.Values))
	for i, value := range e.Values {
		enumVal := EnumValue{
			Val:      value.Name,
			EnumType: e.Inferred,
		}
		enumModule.Values[value.Name] = enumVal
		enumValues[i] = enumVal
	}

	// Add the values() method that returns all enum values as a list
	enumModule.Values["values"] = ListValue{
		Elements: enumValues,
		ElemType: NonNull(e.Inferred),
	}

	// Register the enum module in the environment
	scope.Bind(e.Name.Name, enumModule, e.Visibility)

	return enumModule, nil
}

func (e *EnumDecl) Walk(fn func(Node) bool) {
	if !fn(e) {
		return
	}
	for _, d := range e.Directives {
		d.Walk(fn)
	}
	// Enum values are just symbols, no need to walk them
}

type ScalarDecl struct {
	InferredTypeHolder
	Name       *Symbol
	Value      *Block // optional body: method members and an optional new() hook
	Visibility Visibility
	DocString  string
	Directives []*DirectiveApplication
	Loc        *SourceLocation

	Inferred          *Type
	ConstructorFnType *hm.FunctionType // set when the body declares new()
}

var _ Node = &ScalarDecl{}
var _ Evaluator = &ScalarDecl{}

func (s *ScalarDecl) DeclaredSymbols() []string {
	return []string{s.Name.Name}
}

func (s *ScalarDecl) ReferencedSymbols() []string {
	var symbols []string
	for _, directive := range s.Directives {
		symbols = append(symbols, directive.ReferencedSymbols()...)
	}
	if s.Value != nil {
		symbols = append(symbols, s.Value.ReferencedSymbols()...)
	}
	return symbols
}

func (s *ScalarDecl) Body() hm.Expression {
	if s.Value == nil {
		return nil
	}
	return s.Value
}

func (s *ScalarDecl) GetSourceLocation() *SourceLocation { return s.Loc }

var _ Hoister = &ScalarDecl{}

// validateScalarBodyForms enforces that a scalar body declares only methods
// (function-shaped members) and at most one new() hook. Scalars carry no
// state beyond their underlying string, so stored fields are rejected.
func (s *ScalarDecl) validateScalarBodyForms() error {
	sawNew := false
	for _, form := range s.Value.Forms {
		switch f := form.(type) {
		case *NewConstructorDecl:
			if sawNew {
				return NewInferError(fmt.Errorf("scalar %s declares more than one new()", s.Name.Name), f)
			}
			sawNew = true
		case *FieldDecl:
			if f.Name.Name == "new" {
				vis := "pub"
				if f.Visibility == PrivateVisibility {
					vis = "let"
				}
				return NewInferError(
					fmt.Errorf("'new' is a constructor, not a method; use `new(...) { ... }` without `%s` or a return type", vis),
					f,
				)
			}
			if _, isFun := f.Value.(*FunDecl); !isFun {
				return NewInferError(
					fmt.Errorf("scalar member %q must be a method; scalars carry no fields beyond their underlying string", f.Name.Name),
					f,
				)
			}
		default:
			return NewInferError(fmt.Errorf("scalar bodies may only declare methods and new()"), form)
		}
	}
	return nil
}

// buildScalarConstructorType computes the type of the constructor function
// derived from a scalar's new() hook: exactly one non-null String parameter,
// returning the non-null scalar.
func (s *ScalarDecl) buildScalarConstructorType(ctx context.Context, env hm.Env, newDecl *NewConstructorDecl, fresh hm.Fresher) (*hm.FunctionType, error) {
	if newDecl.BlockParam != nil {
		return nil, NewInferError(fmt.Errorf("scalar new() cannot take a block parameter"), newDecl)
	}
	if len(newDecl.Args) != 1 {
		return nil, NewInferError(fmt.Errorf("scalar new() must take exactly one String! parameter"), newDecl)
	}
	arg := newDecl.Args[0]
	if arg.Value != nil {
		return nil, NewInferError(fmt.Errorf("scalar new() parameter cannot have a default"), arg)
	}

	fnDecl := FunctionBase{Args: newDecl.Args}
	signatureCtx := contextWithInferFunctionControlBoundary(ctx)
	argEnv := env.Clone()
	args, directives, docStrings, err := fnDecl.declareFunctionSignatureArguments(signatureCtx, argEnv, fresh)
	if err != nil {
		return nil, fmt.Errorf("%s new(): %w", s.Name.Name, err)
	}
	argsRec := NewRecordType("", args...)
	argsRec.Directives = directives
	argsRec.DocStrings = docStrings

	argType, _ := argsRec.Fields[0].Value.Type()
	if nn, ok := argType.(hm.NonNullType); !ok || nn.Type != StringType {
		return nil, NewInferError(
			fmt.Errorf("scalar new() parameter must be String!, got %s", argType),
			arg,
		)
	}

	return hm.NewFnType(argsRec, hm.NonNullType{Type: s.Inferred}), nil
}

// inferScalarNew infers the body of a scalar's new() hook. Unlike an object
// constructor, the hook computes the scalar's canonical *underlying string*
// (the runtime wraps it into the scalar value), so the body must return
// String! — and `self` is unavailable, since no value exists yet.
func (s *ScalarDecl) inferScalarNew(ctx context.Context, newDecl *NewConstructorDecl, env hm.Env, fresh hm.Fresher) error {
	var selfErr error
	newDecl.BodyBlock.Walk(func(n Node) bool {
		if _, ok := n.(*SelfKeyword); ok {
			selfErr = NewInferError(fmt.Errorf("self is not available in scalar new(); use the parameter instead"), n)
			return false
		}
		return true
	})
	if selfErr != nil {
		return selfErr
	}

	constructorCtx := contextWithInferFunctionControlBoundary(ctx)
	newEnv := env.Clone().(*OverlayTypeScope)
	arg := newDecl.Args[0]
	argType, err := arg.Infer(constructorCtx, newEnv, fresh)
	if err != nil {
		return fmt.Errorf("inferring new() arg %s: %w", arg.Name.Name, err)
	}
	newEnv.Add(arg.Name.Name, hm.NewScheme(nil, argType))

	returnTarget := NewInferControlTarget(ReturnFrame)
	bodyCtx := contextWithInferReturnTarget(constructorCtx, returnTarget)
	bodyType, err := newDecl.BodyBlock.Infer(bodyCtx, newEnv, fresh)
	if err != nil {
		return fmt.Errorf("inferring new() body: %w", err)
	}

	expectedType := hm.NonNullType{Type: StringType}
	if _, err := hm.Assignable(bodyType, expectedType); err != nil {
		errorNode := Node(newDecl.BodyBlock)
		if len(newDecl.BodyBlock.Forms) > 0 {
			errorNode = newDecl.BodyBlock.Forms[len(newDecl.BodyBlock.Forms)-1]
		}
		return NewInferError(
			fmt.Errorf("scalar new() must return String!, got %s", bodyType.Name()),
			errorNode,
		)
	}
	for _, ret := range collectReturnStatements(newDecl.BodyBlock, returnTarget) {
		retType := returnValueType(ret)
		if retType == nil {
			continue
		}
		if _, err := hm.Assignable(retType, expectedType); err != nil {
			return NewInferError(
				fmt.Errorf("scalar new() must return String!, got %s", retType),
				ret.Value,
			)
		}
	}
	return nil
}

func (s *ScalarDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	mod, ok := env.(TypeScope)
	if !ok {
		return fmt.Errorf("ScalarDecl.Hoist: environment does not support module operations")
	}

	scalarType, declareErr := declareLocalType(mod, s.Name.Name, ScalarKind)
	if declareErr != nil {
		return WrapInferError(declareErr, s.Name)
	}

	s.Inferred = scalarType
	s.SetInferredType(scalarType)

	if s.DocString != "" {
		mod.SetDocString(s.Name.Name, s.DocString)
		scalarType.SetTypeDocString(s.DocString)
	}
	if len(s.Directives) > 0 {
		mod.SetDirectives(s.Name.Name, s.Directives)
	}

	// The scalar's value binding. A scalar with new() binds its constructor
	// function instead (added in pass 1 below); a builtin scalar (JSON, ...)
	// doubles as its namespace and is always present, so it binds non-null —
	// otherwise member access like JSON.encode would inherit the binding's
	// nullability and weaken String! to String.
	newDecl := findNewConstructorIn(s.Value)
	if newDecl == nil {
		scalarScheme := hm.NewScheme(nil, scalarType)
		if _, isBuiltin := BuiltinScalarModule(s.Name.Name); isBuiltin {
			scalarScheme = hm.NewScheme(nil, hm.NonNullType{Type: scalarType})
		}
		env.Add(s.Name.Name, scalarScheme)
		mod.SetVisibility(s.Name.Name, s.Visibility)
	}

	if s.Value == nil {
		return nil
	}

	// Pass 0 must only register the type name; see ObjectDecl.Hoist.
	if pass == 0 {
		return nil
	}

	if err := s.validateScalarBodyForms(); err != nil {
		return err
	}

	inferTypeScope := &OverlayTypeScope{
		primary: scalarType,
		lexical: env.(TypeScope),
	}

	// self resolves to the non-null scalar inside method bodies.
	scalarType.SetDynamicScopeType(hm.NonNullType{Type: scalarType})

	if newDecl != nil {
		ctorType, err := s.buildScalarConstructorType(ctx, inferTypeScope, newDecl, fresh)
		if err != nil {
			return err
		}
		s.ConstructorFnType = ctorType
		env.Add(s.Name.Name, hm.NewScheme(nil, ctorType))
		mod.SetVisibility(s.Name.Name, s.Visibility)
	}

	// Hoist member signatures onto the scalar type so methods can
	// forward-reference each other and other types.
	for _, form := range formsWithoutNew(s.Value) {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, inferTypeScope, fresh, 0); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *ScalarDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod, ok := env.(TypeScope)
	if !ok {
		return nil, fmt.Errorf("ScalarDecl.Infer: environment does not support module operations")
	}

	scalarType, declareErr := declareLocalType(mod, s.Name.Name, ScalarKind)
	if declareErr != nil {
		return nil, WrapInferError(declareErr, s.Name)
	}
	if s.DocString != "" {
		mod.SetDocString(s.Name.Name, s.DocString)
		scalarType.SetTypeDocString(s.DocString)
	}
	if len(s.Directives) > 0 {
		mod.SetDirectives(s.Name.Name, s.Directives)
	}

	s.Inferred = scalarType
	s.SetInferredType(scalarType)

	// Validate directive applications
	for _, directive := range s.Directives {
		if _, err := directive.Infer(ctx, env, fresh); err != nil {
			return nil, fmt.Errorf("ScalarDecl.Infer: directive validation: %w", err)
		}
	}

	if s.Value == nil {
		return scalarType, nil
	}

	// Body-form validation happens in Hoist pass 1 (which always precedes
	// Infer for type declarations); re-validating here would double-report.

	inferTypeScope := &OverlayTypeScope{
		primary: scalarType,
		lexical: env.(TypeScope),
	}
	scalarType.SetDynamicScopeType(hm.NonNullType{Type: scalarType})

	if _, err := InferFormsWithPhases(ctx, formsWithoutNew(s.Value), inferTypeScope, fresh); err != nil {
		return nil, err
	}

	if newDecl := findNewConstructorIn(s.Value); newDecl != nil {
		if err := s.inferScalarNew(ctx, newDecl, inferTypeScope, fresh); err != nil {
			return nil, err
		}
	}

	return scalarType, nil
}

func (s *ScalarDecl) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, s, func() (Value, error) {
		newDecl := findNewConstructorIn(s.Value)

		var evalScope ValueScope
		if s.Value != nil {
			// Evaluate method members into a methods object hung off the *Type,
			// where Select.Eval dispatches them for ScalarValue receivers. The
			// members' shared closure overlays the methods object itself, so
			// sibling methods see each other by bare name.
			methods := NewObject(s.Inferred)
			evalScope = CreateOverlayValueScope(methods, scope)
			for _, form := range formsWithoutNew(s.Value) {
				if _, err := EvalNode(ctx, evalScope, form); err != nil {
					return nil, err
				}
			}
			// Methods are dynamic: a bare sibling call inside a method body
			// re-dispatches through the receiver (see FunctionValue.Call).
			for _, kv := range methods.Bindings(PrivateVisibility) {
				if fnVal, ok := kv.Value.(FunctionValue); ok {
					fnVal.IsDynamic = true
					methods.Bind(kv.Key, fnVal, PublicVisibility)
				}
			}
			s.Inferred.SetScalarMethods(methods)
		}

		if newDecl != nil {
			// The new() hook computes the canonical underlying string; the
			// runtime wraps it (see runScalarHook). It runs with no self —
			// deliberately not IsDynamic, so a caller's dynamic scope can
			// never be mistaken for a receiver.
			argName := newDecl.Args[0].Name.Name
			hookArgs := NewRecordType("", Keyed[*hm.Scheme]{
				Key:   argName,
				Value: hm.NewScheme(nil, hm.NonNullType{Type: StringType}),
			})
			hook := FunctionValue{
				Args:    []string{argName},
				Body:    newDecl.BodyBlock,
				Closure: evalScope,
				FnType:  hm.NewFnType(hookArgs, hm.NonNullType{Type: StringType}),
			}
			s.Inferred.SetScalarHook(hook, argName)

			ctor := ScalarConstructor{
				ScalarType: s.Inferred,
				FnType:     s.ConstructorFnType,
				ArgName:    argName,
			}
			scope.Bind(s.Name.Name, ctor, s.Visibility)
			return ctor, nil
		}

		// No constructor: the scalar's name binds its namespace object. A
		// scalar whose name matches a builtin scalar (e.g. JSON) doubles as
		// that scalar's namespace: bind Dang's members onto its runtime object.
		scalarModule := NewObject(s.Inferred)
		attachBuiltinMethods(scalarModule, s.Inferred)
		scope.Bind(s.Name.Name, scalarModule, s.Visibility)
		return scalarModule, nil
	})
}

func (s *ScalarDecl) Walk(fn func(Node) bool) {
	if !fn(s) {
		return
	}
	for _, d := range s.Directives {
		d.Walk(fn)
	}
	if s.Value != nil {
		s.Value.Walk(fn)
	}
}

type InterfaceDecl struct {
	InferredTypeHolder
	Name       *Symbol
	Value      *Block
	Implements []*Symbol
	Visibility Visibility
	Directives []*DirectiveApplication
	DocString  string
	Loc        *SourceLocation

	Inferred *Type
}

var _ Node = &InterfaceDecl{}
var _ Evaluator = &InterfaceDecl{}

func (i *InterfaceDecl) DeclaredSymbols() []string {
	return []string{i.Name.Name}
}

func (i *InterfaceDecl) ReferencedSymbols() []string {
	var symbols []string
	for _, parent := range i.Implements {
		symbols = append(symbols, parent.Name)
	}
	// Interface declarations reference symbols from their body (the Block)
	symbols = append(symbols, i.Value.ReferencedSymbols()...)
	return symbols
}

func (i *InterfaceDecl) Body() hm.Expression { return i.Value }

func (i *InterfaceDecl) GetSourceLocation() *SourceLocation { return i.Loc }

var _ Hoister = &InterfaceDecl{}

func (i *InterfaceDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	mod, ok := env.(TypeScope)
	if !ok {
		return fmt.Errorf("InterfaceDecl.Hoist: environment does not support module operations")
	}

	iface, declareErr := declareLocalType(mod, i.Name.Name, InterfaceKind)
	if declareErr != nil {
		return WrapInferError(declareErr, i.Name)
	}
	i.Inferred = iface
	i.SetInferredType(iface)
	if i.DocString != "" {
		mod.SetDocString(i.Name.Name, i.DocString)
		iface.SetTypeDocString(i.DocString)
	}

	// Pass 0: Register the interface type.
	if pass == 0 {
		// Add the interface type to the environment so it can be referenced.
		interfaceScheme := hm.NewScheme(nil, iface)
		env.Add(i.Name.Name, interfaceScheme)
		return nil
	}

	// Link parent interfaces after all type names have been registered by
	// pass 0. Reuse the implementers index used by objects so inline
	// fragments and downstream subtyping see this interface as well.
	if len(i.Implements) > 0 {
		for _, parentSym := range i.Implements {
			parentType, found := mod.NamedType(parentSym.Name)
			if !found {
				return WrapInferError(
					fmt.Errorf("interface %s not found", parentSym.Name),
					parentSym,
				)
			}
			parentMod, ok := parentType.(*Type)
			if !ok || parentMod.Kind != InterfaceKind {
				return WrapInferError(
					fmt.Errorf("%s is not an interface", parentSym.Name),
					parentSym,
				)
			}
			iface.AddInterface(parentType)
			if localParent, found := mod.LocalNamedType(parentSym.Name); found && localParent == parentType {
				parentMod.AddImplementer(iface)
			}
		}
	}

	// Pass 1: Declare interface field and method signatures without inferring
	// implementation bodies. Interface bodies only describe the public shape.
	inferTypeScope := &OverlayTypeScope{
		primary: iface,
		lexical: mod,
	}
	for _, form := range i.Value.Forms {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, inferTypeScope, fresh, 0); err != nil {
				return err
			}
		}
	}

	return nil
}

func (i *InterfaceDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(i, func() (hm.Type, error) {
		mod, ok := env.(TypeScope)
		if !ok {
			return nil, fmt.Errorf("InterfaceDecl.Infer: environment does not support module operations")
		}

		iface, found := mod.LocalNamedType(i.Name.Name)
		if !found {
			return nil, fmt.Errorf("interface %s not found", i.Name.Name)
		}

		// Infer the interface fields using composite environment
		inferTypeScope := &OverlayTypeScope{
			primary: iface,
			lexical: env.(TypeScope),
		}

		// Use phased inference approach (like ObjectDecl) to avoid environment cloning
		if _, err := InferFormsWithPhases(ctx, i.Value.Forms, inferTypeScope, fresh); err != nil {
			return nil, err
		}

		i.Inferred = iface.(*Type)
		i.SetInferredType(iface)

		// Validate parent interface field compatibility once this interface's
		// own field signatures have been inferred.
		if len(i.Implements) > 0 {
			for _, parentSym := range i.Implements {
				if err := validateInterfaceExtension(i.Inferred, mod, parentSym); err != nil {
					return nil, err
				}
			}
		}

		// Set doc string
		if i.DocString != "" {
			mod.SetDocString(i.Name.Name, i.DocString)
			i.Inferred.SetTypeDocString(i.DocString)
		}

		return iface, nil
	})
}

// validateInterfaceExtension checks that a child interface satisfies every
// field declared by a parent interface it claims to implement.
func validateInterfaceExtension(child *Type, env TypeScope, parentSym *Symbol) error {
	parentType, found := env.NamedType(parentSym.Name)
	if !found {
		// Already raised in Hoist
		return nil
	}
	parentMod, ok := parentType.(*Type)
	if !ok || parentMod.Kind != InterfaceKind {
		return nil
	}

	var missingFields []string
	for field, fieldScheme := range parentMod.Bindings(PrivateVisibility) {
		if isReservedInterfaceField(field, fieldScheme) {
			continue
		}
		childScheme, has := child.SchemeOf(field)
		if !has {
			missingFields = append(missingFields, field)
			continue
		}
		parentFieldType, _ := fieldScheme.Type()
		childFieldType, _ := childScheme.Type()
		if err := validateFieldImplementation(field, parentFieldType, childFieldType, parentMod.String(), child.String()); err != nil {
			return WrapInferError(err, parentSym)
		}
	}

	if len(missingFields) > 0 {
		errs := &InferenceErrors{}
		sort.Strings(missingFields)
		for _, field := range missingFields {
			fieldScheme, _ := parentMod.SchemeOf(field)
			errs.Add(WrapInferError(
				fmt.Errorf("interface %s is missing `%s`, required by interface %s", child, fieldSignature(field, fieldScheme), parentMod),
				parentSym,
			))
		}
		return errs
	}
	return nil
}

func (i *InterfaceDecl) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	// Interfaces are pure type declarations - they don't have runtime values
	// Just register the interface module in the environment
	interfaceModule := NewObject(i.Inferred)
	scope.Bind(i.Name.Name, interfaceModule, i.Visibility)
	return interfaceModule, nil
}

func (i *InterfaceDecl) Walk(fn func(Node) bool) {
	if !fn(i) {
		return
	}
	for _, d := range i.Directives {
		d.Walk(fn)
	}
	i.Value.Walk(fn)
}

type UnionDecl struct {
	InferredTypeHolder
	Name       *Symbol
	Members    []*Symbol
	Visibility Visibility
	DocString  string
	Loc        *SourceLocation

	Inferred *Type
}

var _ Node = &UnionDecl{}
var _ Evaluator = &UnionDecl{}
var _ Hoister = &UnionDecl{}

func (u *UnionDecl) DeclaredSymbols() []string {
	return []string{u.Name.Name}
}

func (u *UnionDecl) ReferencedSymbols() []string {
	symbols := make([]string, len(u.Members))
	for i, m := range u.Members {
		symbols[i] = m.Name
	}
	return symbols
}

func (u *UnionDecl) Body() hm.Expression { return nil }

func (u *UnionDecl) GetSourceLocation() *SourceLocation { return u.Loc }

func (u *UnionDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	mod, ok := env.(TypeScope)
	if !ok {
		return fmt.Errorf("UnionDecl.Hoist: environment does not support module operations")
	}

	unionMod, declareErr := declareLocalType(mod, u.Name.Name, UnionKind)
	if declareErr != nil {
		return WrapInferError(declareErr, u.Name)
	}
	u.Inferred = unionMod
	u.SetInferredType(unionMod)
	if u.DocString != "" {
		mod.SetDocString(u.Name.Name, u.DocString)
	}

	if pass == 0 {
		// Pass 0: Register the union type so it can be referenced.
		env.Add(u.Name.Name, hm.NewScheme(nil, unionMod))
		return nil
	}

	// Pass 1: Link member names. This is still signature information; no
	// expression bodies are inferred.
	for _, memberSym := range u.Members {
		memberType, found := mod.NamedType(memberSym.Name)
		if !found {
			continue
		}

		memberMod, ok := memberType.(*Type)
		if !ok || memberMod.Kind != ObjectKind {
			continue
		}

		if localMember, found := mod.LocalNamedType(memberSym.Name); found && localMember == memberType {
			unionMod.LinkMember(memberType)
		} else {
			unionMod.AddMember(memberType)
		}
	}

	return nil
}

func (u *UnionDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(u, func() (hm.Type, error) {
		mod, ok := env.(TypeScope)
		if !ok {
			return nil, fmt.Errorf("UnionDecl.Infer: environment does not support module operations")
		}

		unionType, found := mod.LocalNamedType(u.Name.Name)
		if !found {
			return nil, fmt.Errorf("union %s not found", u.Name.Name)
		}

		unionMod := unionType.(*Type)

		// Resolve and link each member type
		for _, memberSym := range u.Members {
			memberType, found := mod.NamedType(memberSym.Name)
			if !found {
				return nil, NewInferError(
					fmt.Errorf("union member %s not found", memberSym.Name),
					memberSym,
				)
			}

			memberMod, ok := memberType.(*Type)
			if !ok {
				return nil, NewInferError(
					fmt.Errorf("union member %s is not a type", memberSym.Name),
					memberSym,
				)
			}

			if memberMod.Kind != ObjectKind {
				return nil, NewInferError(
					fmt.Errorf("union member %s must be an object type, got %s", memberSym.Name, memberMod.Kind),
					memberSym,
				)
			}

			if localMember, found := mod.LocalNamedType(memberSym.Name); found && localMember == memberType {
				unionMod.LinkMember(memberType)
			} else {
				unionMod.AddMember(memberType)
			}
		}

		u.Inferred = unionMod
		u.SetInferredType(unionMod)

		// Set doc string
		if u.DocString != "" {
			mod.SetDocString(u.Name.Name, u.DocString)
		}

		return unionMod, nil
	})
}

func (u *UnionDecl) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	// Unions are pure type declarations - register the union module
	unionModule := NewObject(u.Inferred)
	scope.Bind(u.Name.Name, unionModule, u.Visibility)
	return unionModule, nil
}

func (u *UnionDecl) Walk(fn func(Node) bool) {
	if !fn(u) {
		return
	}
	for _, m := range u.Members {
		fn(m)
	}
}
