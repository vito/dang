package dang

import (
	"context"
	"fmt"
	"sort"

	"github.com/vito/dang/pkg/hm"
)

type SlotDecl struct {
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

var _ Node = (*SlotDecl)(nil)
var _ Evaluator = (*SlotDecl)(nil)
var _ Hoister = (*SlotDecl)(nil)

func (s *SlotDecl) DeclaredSymbols() []string {
	return []string{s.Name.Name} // Slot declarations declare their name
}

func (s *SlotDecl) ReferencedSymbols() []string {
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

func (s *SlotDecl) Body() hm.Expression {
	// TODO(vito): return Value? unclear how Body is used
	return s
}

func (s *SlotDecl) GetSourceLocation() *SourceLocation { return s.Loc }

func (s *SlotDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	// If the slot value is a hoister (e.g. wraps a FunDecl), delegate while
	// preserving the slot's name and metadata. This is the signature boundary:
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
			if e, ok := env.(Env); ok {
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

	// For non-function slots, register the type during pass 0 so that
	// sibling declarations (e.g. method default-value expressions) can
	// reference it before full inference runs. This mirrors the declaration
	// pass for function signatures.
	//
	// The type is determined from the explicit annotation if present,
	// otherwise from the value if it implements Constant (literals whose
	// type is known without consulting the environment). Computed values are
	// intentionally not inferred at the hoist boundary.
	if pass == 0 {
		slotType, err := s.signatureType(ctx, env, fresh, false)
		if err != nil {
			return err
		}
		if slotType != nil {
			env.Add(s.Name.Name, hm.NewScheme(nil, slotType))
			s.SetInferredType(slotType)
			if e, ok := env.(Env); ok {
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

func (s *SlotDecl) signatureType(ctx context.Context, env hm.Env, fresh hm.Fresher, allowComputed bool) (hm.Type, error) {
	if s.Type_ != nil {
		slotType, err := s.Type_.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}
		return slotType, nil
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

func (s *SlotDecl) DeclareKnownSignature(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	slotType, err := s.signatureType(ctx, env, fresh, false)
	if err != nil {
		return nil, err
	}
	if slotType == nil {
		return nil, nil
	}
	env.Add(s.Name.Name, hm.NewScheme(nil, slotType))
	s.SetInferredType(slotType)
	if e, ok := env.(Env); ok {
		e.SetVisibility(s.Name.Name, s.Visibility)
		if s.DocString != "" {
			e.SetDocString(s.Name.Name, s.DocString)
		}
		if len(s.Directives) > 0 {
			e.SetDirectives(s.Name.Name, s.Directives)
		}
	}
	return slotType, nil
}

func (s *SlotDecl) DeclareSignature(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	slotType, err := s.signatureType(ctx, env, fresh, true)
	if err != nil {
		return nil, err
	}
	if slotType == nil {
		slotType = fresh.Fresh()
	}
	env.Add(s.Name.Name, hm.NewScheme(nil, slotType))
	s.SetInferredType(slotType)
	if e, ok := env.(Env); ok {
		e.SetVisibility(s.Name.Name, s.Visibility)
		if s.DocString != "" {
			e.SetDocString(s.Name.Name, s.DocString)
		}
		if len(s.Directives) > 0 {
			e.SetDirectives(s.Name.Name, s.Directives)
		}
	}
	return slotType, nil
}

func (s *SlotDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
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
			if _, err := hm.Assignable(inferredType, definedType); err != nil {
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

	if e, ok := env.(Env); ok {
		cur, defined := e.LocalSchemeOf(s.Name.Name)
		if defined {
			curT, curMono := cur.Type()
			if !curMono {
				return nil, fmt.Errorf("SlotDecl.Infer: TODO: type is not monomorphic")
			}

			if !definedType.Eq(curT) {
				return nil, WrapInferError(
					fmt.Errorf("SlotDecl.Infer: %q already defined as %s, trying to redefine as %s", s.Name.Name, curT, definedType),
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
			return nil, fmt.Errorf("SlotDecl.Infer: directive validation: %w", err)
		}
	}

	env.Add(s.Name.Name, hm.NewScheme(nil, definedType))
	s.SetInferredType(definedType)
	return definedType, nil
}

func (s *SlotDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, s, func() (Value, error) {
		val, defined := env.LookupLocal(s.Name.Name)
		if defined {
			// Already defined (e.g. through constructor). The value reached us
			// through a Coerce-wrapped argument so it is already materialized.
			return val, nil
		}

		if s.Value == nil {
			// Check if this is a required (non-null) type without a value
			// This is a runtime error - required types must have values
			if inferredType := s.GetInferredType(); inferredType != nil {
				if _, isNonNull := inferredType.(hm.NonNullType); isNonNull {
					return nil, fmt.Errorf("required slot %q (type %s) has no value", s.Name.Name, inferredType)
				}
			}

			// If no value is provided, this is just a type declaration
			// Add a null value to the environment as a placeholder
			env.Bind(s.Name.Name, NullValue{}, s.Visibility)
			return NullValue{}, nil
		}

		// Evaluate the value expression with proper error context. The Value
		// node is wrapped in a Coerce by SlotDecl.Infer when the slot has an
		// explicit type, so materialization happens during EvalNode.
		val, err := EvalNode(ctx, env, s.Value)
		if err != nil {
			return nil, err
		}

		env.Bind(s.Name.Name, val, s.Visibility)
		return val, nil
	})
}

func (s *SlotDecl) Walk(fn func(Node) bool) {
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

type ClassDecl struct {
	InferredTypeHolder
	Name       *Symbol
	Value      *Block
	TypeParams []hm.TypeVariable
	Implements []*Symbol
	Visibility Visibility
	Directives []*DirectiveApplication
	DocString  string
	Loc        *SourceLocation

	Inferred          *Module
	ConstructorFnType *hm.FunctionType
}

// NewConstructorDecl represents an explicit `new(...) { ... }` constructor
type NewConstructorDecl struct {
	InferredTypeHolder
	Args       []*SlotDecl
	BlockParam *SlotDecl
	BodyBlock  *Block
	DocString  string
	Loc        *SourceLocation

	Inferred *hm.FunctionType
}

var _ Node = &NewConstructorDecl{}
var _ Evaluator = &NewConstructorDecl{}

func (n *NewConstructorDecl) DeclaredSymbols() []string {
	return nil // new doesn't declare a symbol, it's handled specially by ClassDecl
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

// Infer returns an error since new() is only valid inside a class body.
// When used inside a class, it is inferred by ClassDecl.inferNewConstructor instead.
func (n *NewConstructorDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return nil, NewInferError(fmt.Errorf("new() constructor can only be defined inside a type body"), n)
}

// Eval is a no-op since NewConstructorDecl is evaluated as part of ConstructorFunction.Call
func (n *NewConstructorDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// The new() constructor body is evaluated by ConstructorFunction.Call
	return NullValue{}, nil
}

var _ Node = &ClassDecl{}
var _ Evaluator = &ClassDecl{}

func (c *ClassDecl) DeclaredSymbols() []string {
	return []string{c.Name.Name} // Class declarations declare their name
}

func (c *ClassDecl) ReferencedSymbols() []string {
	var symbols []string
	// Class declarations reference symbols from their body (the Block)
	symbols = append(symbols, c.Value.ReferencedSymbols()...)
	// And from directive applications
	for _, directive := range c.Directives {
		symbols = append(symbols, directive.ReferencedSymbols()...)
	}
	return symbols
}

func (c *ClassDecl) Body() hm.Expression { return c.Value }

func (c *ClassDecl) GetSourceLocation() *SourceLocation { return c.Loc }

var _ Hoister = &ClassDecl{}

// Imported schema types are installed into the local type map so they can be
// referenced unqualified. A real type declaration with the same name must
// replace an unqualified imported alias, not mutate the imported schema type.
// Qualified import aliases like Dagger are protected so Dagger.Container keeps
// resolving through the imported module.
func localTypeShadowsImport(env Env, name string) bool {
	origin, found := env.LocalTypeOrigin(name)
	return found && origin.IsUnqualifiedImport()
}

func localTypeIsQualifiedImport(env Env, name string) bool {
	origin, found := env.LocalTypeOrigin(name)
	return found && origin.Kind == BindingOriginImport && origin.Qualified
}

func declareLocalType(env Env, name string, kind ModuleKind) (*Module, error) {
	if existing, found := env.LocalNamedType(name); found {
		if localTypeShadowsImport(env, name) {
			// Local declarations intentionally shadow unqualified imports.
		} else if localTypeIsQualifiedImport(env, name) {
			return nil, fmt.Errorf("type %q conflicts with import alias", name)
		} else if mod, ok := existing.(*Module); ok {
			return mod, nil
		} else {
			return nil, fmt.Errorf("type %q conflicts with existing type", name)
		}
	}

	mod := NewModule(name, kind)
	env.AddClass(name, mod)
	return mod, nil
}

// findNewConstructor returns the NewConstructorDecl from the class body, if any
func (c *ClassDecl) findNewConstructor() *NewConstructorDecl {
	for _, form := range c.Value.Forms {
		if newDecl, ok := form.(*NewConstructorDecl); ok {
			return newDecl
		}
	}
	return nil
}

// bodyFormsWithoutNew returns the class body forms excluding the NewConstructorDecl
func (c *ClassDecl) bodyFormsWithoutNew() []Node {
	var forms []Node
	for _, form := range c.Value.Forms {
		if _, ok := form.(*NewConstructorDecl); !ok {
			forms = append(forms, form)
		}
	}
	return forms
}

func (c *ClassDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	mod, ok := env.(Env)
	if !ok {
		return fmt.Errorf("ClassDecl.Hoist: environment does not support module operations")
	}

	class, declareErr := declareLocalType(mod, c.Name.Name, ObjectKind)
	if declareErr != nil {
		return WrapInferError(declareErr, c.Name)
	}
	c.Inferred = class
	c.SetInferredType(class)

	// Pass 0 must only register the type name. Other top-level types may refer
	// to this class before its file is reached, so anything that resolves field
	// annotations, constructor params, or implemented interfaces has to wait
	// until every type has had this registration pass.
	if pass == 0 {
		if err := c.validateTypeParams(); err != nil {
			return err
		}
		if len(c.TypeParams) > 0 && len(c.Implements) > 0 {
			return WrapInferError(
				fmt.Errorf("generic type %s cannot declare `implements`; generic interfaces are not supported", c.Name.Name),
				c.Name,
			)
		}
		class.TypeParams = c.TypeParams
		return nil
	}

	inferEnv := &CompositeModule{
		primary: class,
		lexical: env.(Env),
	}

	// Build constructor type: use explicit new() if present, otherwise derive from fields
	newDecl := c.findNewConstructor()
	var constructorParams []*SlotDecl
	var constructorBlockParam *SlotDecl
	if newDecl != nil {
		constructorParams = newDecl.Args
		constructorBlockParam = newDecl.BlockParam
	} else {
		constructorParams = c.extractConstructorParameters()
	}
	selfType := classSelfType(class)
	constructorType, err := c.buildConstructorType(ctx, inferEnv, constructorParams, constructorBlockParam, selfType, fresh)
	if err != nil {
		return err
	}
	c.ConstructorFnType = constructorType

	// Add the constructor function type to the environment. For generic
	// classes, quantify over the class type parameters so each call site
	// instantiates fresh type variables.
	constructorScheme := hm.NewScheme(c.TypeParams, constructorType)
	env.Add(c.Name.Name, constructorScheme)

	// Link the implementation after all interface type names have been
	// registered by pass 0.
	if len(c.Implements) > 0 {
		classMod := class
		for _, ifaceSym := range c.Implements {
			ifaceType, found := mod.NamedType(ifaceSym.Name)
			if !found {
				return WrapInferError(
					fmt.Errorf("interface %s not found", ifaceSym.Name),
					ifaceSym,
				)
			}

			ifaceMod, ok := ifaceType.(*Module)
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
			classMod.AddInterface(ifaceType)
			if localIface, found := mod.LocalNamedType(ifaceSym.Name); found && localIface == ifaceType {
				ifaceMod.AddImplementer(classMod)
			}
		}
	}

	// Set dynamic scope type to the class type. For generic classes,
	// use the applied self type so method bodies see fields with class
	// type parameters substituted by their own free type variables.
	class.SetDynamicScopeType(hm.NonNullType{Type: selfType})

	// Hoist body forms directly (not via Block.Hoist which clones the env)
	// to register method signatures on the class module. This enables
	// forward references between types defined in any order. We hoist at
	// pass 0 so that FunDecl.Hoist registers signatures and
	// SlotDecl.Hoist registers typed field declarations.
	bodyForms := c.bodyFormsWithoutNew()
	for _, form := range bodyForms {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, inferEnv, fresh, 0); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *ClassDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod, ok := env.(Env)
	if !ok {
		return nil, fmt.Errorf("ClassDecl.Infer: environment does not support module operations")
	}

	class, declareErr := declareLocalType(mod, c.Name.Name, ObjectKind)
	if declareErr != nil {
		return nil, WrapInferError(declareErr, c.Name)
	}

	// Store doc string for the class name in the environment
	if c.DocString != "" {
		mod.SetDocString(c.Name.Name, c.DocString)
	}

	// Set this early so we can at least partially infer.
	c.Inferred = class

	// Set dynamic scope type to the class type
	class.SetDynamicScopeType(hm.NonNullType{Type: classSelfType(class)})

	// Validate directive applications
	for _, directive := range c.Directives {
		_, err := directive.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("ClassDecl.Infer: directive validation: %w", err)
		}
	}

	inferEnv := &CompositeModule{
		primary: class,
		lexical: env.(Env),
	}

	// Check for slots named "new" — the user likely intended a constructor
	for _, form := range c.Value.Forms {
		if slot, ok := form.(*SlotDecl); ok && slot.Name.Name == "new" {
			vis := "pub"
			if slot.Visibility == PrivateVisibility {
				vis = "let"
			}
			return nil, NewInferError(
				fmt.Errorf("'new' is a constructor, not a method; use `new(...) { ... }` without `%s` or a return type", vis),
				slot,
			)
		}
	}

	// Infer body forms (excluding new() which is handled separately)
	bodyForms := c.bodyFormsWithoutNew()
	if _, err := InferFormsWithPhases(ctx, bodyForms, inferEnv, fresh); err != nil {
		return nil, err
	}

	// If there's an explicit new(), infer its body with its args in scope.
	// Errors here (e.g. wrong return type) are collected but don't prevent
	// the class from being usable, avoiding cascading type errors.
	newDecl := c.findNewConstructor()
	var newBodyErr error
	if newDecl != nil {
		newBodyErr = c.inferNewConstructor(ctx, newDecl, inferEnv, fresh)
	}

	// Validate interface implementations after fields have been inferred
	if len(c.Implements) > 0 {
		classMod := c.Inferred
		for _, ifaceSym := range c.Implements {
			if err := c.validateInterfaceImplementations(classMod, mod, ifaceSym); err != nil {
				return nil, err
			}
		}
	}

	return c.ConstructorFnType, newBodyErr
}

// validateInterfaceImplementations checks that this type correctly implements all declared interfaces
func (c *ClassDecl) validateInterfaceImplementations(classMod *Module, env Env, ifaceSym *Symbol) error {
	ifaceType, found := env.NamedType(ifaceSym.Name)
	if !found {
		// no error; this is raised in Hoist instead
		return nil
	}

	ifaceMod, ok := ifaceType.(*Module)
	if !ok || ifaceMod.Kind != InterfaceKind {
		// no error; this is raised in Hoist instead
		return nil
	}

	var missingFields []string
	// Check that all interface fields are present in the class
	for field, fieldScheme := range ifaceMod.Bindings(PrivateVisibility) {
		classFieldScheme, classHasField := classMod.SchemeOf(field)
		if !classHasField {
			missingFields = append(missingFields, field)
			continue
		}

		// Get the types from the schemes
		ifaceFieldType, _ := fieldScheme.Type()
		classFieldType, _ := classFieldScheme.Type()

		// Validate field type compatibility
		if err := validateFieldImplementation(field, ifaceFieldType, classFieldType, ifaceMod.String(), classMod.String()); err != nil {
			return WrapInferError(err, ifaceSym)
		}
	}

	if len(missingFields) > 0 {
		errs := &InferenceErrors{}
		sort.Strings(missingFields)
		for _, field := range missingFields {
			fieldScheme, _ := ifaceMod.SchemeOf(field)
			errs.Add(WrapInferError(
				fmt.Errorf("class %s is missing `%s%s`, required by interface %s", classMod, field, fieldScheme, ifaceMod),
				ifaceSym,
			))
		}
		return errs
	}

	return nil
}

// extractConstructorParametersAndCleanBody extracts public non-function slots and private
// required slots (no default) as constructor parameters and returns the filtered forms that
// should be evaluated in the class body
func (c *ClassDecl) extractConstructorParameters() []*SlotDecl {
	var params []*SlotDecl

	for _, form := range c.Value.Forms {
		if slot, ok := form.(*SlotDecl); ok {
			// Skip function slots
			if _, isFun := slot.Value.(*FunDecl); isFun {
				continue
			}

			// Include public non-function slots as constructor parameters
			if slot.Visibility == PublicVisibility {
				params = append(params, slot)
				continue
			}

			// Include private slots that are required (no default value)
			if slot.Visibility == PrivateVisibility && slot.Value == nil {
				params = append(params, slot)
			}
		}
	}

	return params
}

// classSelfType returns the type a class declaration uses for self-references
// (e.g. the constructor's return type and the dynamic-scope receiver). For a
// non-generic class this is just the *Module; for a generic class it is the
// AppliedType formed by reusing the class's declared type parameters as args.
func classSelfType(class *Module) hm.Type {
	if len(class.TypeParams) == 0 {
		return class
	}
	args := make([]hm.Type, len(class.TypeParams))
	for i, p := range class.TypeParams {
		args[i] = p
	}
	return &AppliedType{Base: class, Args: args}
}

// validateTypeParams checks the class's declared type parameters for
// duplicates. Names are limited to single lowercase letters by the parser.
func (c *ClassDecl) validateTypeParams() error {
	if len(c.TypeParams) == 0 {
		return nil
	}
	seen := make(map[hm.TypeVariable]bool, len(c.TypeParams))
	for _, p := range c.TypeParams {
		if seen[p] {
			return WrapInferError(
				fmt.Errorf("duplicate type parameter %q in type %s", string(p), c.Name.Name),
				c.Name,
			)
		}
		seen[p] = true
	}
	return nil
}

// buildConstructorType creates a function type for the constructor based on the parameters
func (c *ClassDecl) buildConstructorType(ctx context.Context, env hm.Env, params []*SlotDecl, blockParam *SlotDecl, selfType hm.Type, fresh hm.Fresher) (*hm.FunctionType, error) {
	fnDecl := FunctionBase{
		Args:       params,
		BlockParam: blockParam,
	}
	signatureCtx := contextWithInferFunctionControlBoundary(ctx)
	argEnv := env.Clone()
	args, directives, docStrings, err := fnDecl.declareFunctionSignatureArguments(signatureCtx, argEnv, fresh)
	if err != nil {
		return nil, fmt.Errorf("%s Constructor.Declare: %w", c.Name.Name, err)
	}
	argsRec := NewRecordType("", args...)
	argsRec.Directives = directives
	argsRec.DocStrings = docStrings

	constructorType := hm.NewFnType(argsRec, hm.NonNullType{Type: selfType})
	if blockParam != nil {
		blockParamType, err := blockParam.Type_.Infer(signatureCtx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("%s Constructor.Declare block parameter: %w", c.Name.Name, err)
		}
		blockType, ok := blockParamType.(*hm.FunctionType)
		if !ok {
			return nil, fmt.Errorf("%s Constructor.Declare: block parameter must be a function type, got %T", c.Name.Name, blockParamType)
		}
		constructorType.SetBlock(blockType)
	}
	return constructorType, nil
}

// inferNewConstructor infers the body of an explicit new() constructor
func (c *ClassDecl) inferNewConstructor(ctx context.Context, newDecl *NewConstructorDecl, inferEnv *CompositeModule, fresh hm.Fresher) error {
	constructorCtx := contextWithInferFunctionControlBoundary(ctx)

	// Create an environment with the constructor args in scope
	newEnv := inferEnv.Clone().(*CompositeModule)
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
			blockType, err = newDecl.BlockParam.Type_.Infer(constructorCtx, inferEnv, fresh)
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

	// The new() body must return the class type
	expectedType := hm.NonNullType{Type: classSelfType(c.Inferred)}
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

func (c *ClassDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, c, func() (Value, error) {
		if c.Inferred == nil {
			panic(fmt.Errorf("ClassDecl.Eval: class %q has not been inferred", c.Name.Name))
		}

		// Set doc string for the class/module itself
		if c.DocString != "" {
			c.Inferred.SetModuleDocString(c.DocString)
		}

		// Find explicit new() or derive constructor from fields
		newDecl := c.findNewConstructor()
		var constructorParams []*SlotDecl
		var constructorBlockParam *SlotDecl
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

		// Create a constructor function that evaluates the class body when called
		constructor := &ConstructorFunction{
			Closure:        env,
			ClassName:      c.Name.Name,
			Parameters:     constructorParams,
			BlockParamName: blockParamName,
			ClassType:      c.Inferred,
			ClassBodyForms: c.bodyFormsWithoutNew(),
			FnType:         c.ConstructorFnType,
			NewBody:        newBody,
		}

		// Add the constructor to the evaluation environment
		env.Bind(c.Name.Name, constructor, c.Visibility)

		return constructor, nil
	})
}

func (c *ClassDecl) Walk(fn func(Node) bool) {
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

	Inferred *Module
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
	mod, ok := env.(Env)
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
	mod, ok := env.(Env)
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

func (e *EnumDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Create a module value for the enum
	enumModule := NewModuleValue(e.Inferred)

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
	env.Bind(e.Name.Name, enumModule, e.Visibility)

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
	Visibility Visibility
	DocString  string
	Directives []*DirectiveApplication
	Loc        *SourceLocation

	Inferred *Module
}

var _ Node = &ScalarDecl{}
var _ Evaluator = &ScalarDecl{}

func (s *ScalarDecl) DeclaredSymbols() []string {
	return []string{s.Name.Name}
}

func (s *ScalarDecl) ReferencedSymbols() []string {
	return nil // Scalar declarations don't reference other symbols
}

func (s *ScalarDecl) Body() hm.Expression { return nil }

func (s *ScalarDecl) GetSourceLocation() *SourceLocation { return s.Loc }

var _ Hoister = &ScalarDecl{}

func (s *ScalarDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	mod, ok := env.(Env)
	if !ok {
		return fmt.Errorf("ScalarDecl.Hoist: environment does not support module operations")
	}

	scalarType, declareErr := declareLocalType(mod, s.Name.Name, ScalarKind)
	if declareErr != nil {
		return WrapInferError(declareErr, s.Name)
	}

	s.Inferred = scalarType
	s.SetInferredType(scalarType)

	// Add the scalar type to the environment
	scalarScheme := hm.NewScheme(nil, scalarType)
	env.Add(s.Name.Name, scalarScheme)

	if s.DocString != "" {
		mod.SetDocString(s.Name.Name, s.DocString)
	}

	return nil
}

func (s *ScalarDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod, ok := env.(Env)
	if !ok {
		return nil, fmt.Errorf("ScalarDecl.Infer: environment does not support module operations")
	}

	scalarType, declareErr := declareLocalType(mod, s.Name.Name, ScalarKind)
	if declareErr != nil {
		return nil, WrapInferError(declareErr, s.Name)
	}
	if s.DocString != "" {
		mod.SetDocString(s.Name.Name, s.DocString)
	}

	s.Inferred = scalarType
	s.SetInferredType(scalarType)

	return scalarType, nil
}

func (s *ScalarDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Scalars are just type placeholders, similar to enums but with no values
	// The actual scalar values come from GraphQL or are just strings
	scalarModule := NewModuleValue(s.Inferred)

	// Register the scalar type in the environment
	env.Bind(s.Name.Name, scalarModule, s.Visibility)

	return scalarModule, nil
}

func (s *ScalarDecl) Walk(fn func(Node) bool) {
	if !fn(s) {
		return
	}
	for _, d := range s.Directives {
		d.Walk(fn)
	}
}

type InterfaceDecl struct {
	InferredTypeHolder
	Name       *Symbol
	Value      *Block
	Visibility Visibility
	Directives []*DirectiveApplication
	DocString  string
	Loc        *SourceLocation

	Inferred *Module
}

var _ Node = &InterfaceDecl{}
var _ Evaluator = &InterfaceDecl{}

func (i *InterfaceDecl) DeclaredSymbols() []string {
	return []string{i.Name.Name}
}

func (i *InterfaceDecl) ReferencedSymbols() []string {
	var symbols []string
	// Interface declarations reference symbols from their body (the Block)
	symbols = append(symbols, i.Value.ReferencedSymbols()...)
	return symbols
}

func (i *InterfaceDecl) Body() hm.Expression { return i.Value }

func (i *InterfaceDecl) GetSourceLocation() *SourceLocation { return i.Loc }

var _ Hoister = &InterfaceDecl{}

func (i *InterfaceDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	mod, ok := env.(Env)
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
	}

	// Pass 0: Register the interface type.
	if pass == 0 {
		// Add the interface type to the environment so it can be referenced.
		interfaceScheme := hm.NewScheme(nil, iface)
		env.Add(i.Name.Name, interfaceScheme)
		return nil
	}

	// Pass 1: Declare interface field and method signatures without inferring
	// implementation bodies. Interface bodies only describe the public shape.
	inferEnv := &CompositeModule{
		primary: iface,
		lexical: mod,
	}
	for _, form := range i.Value.Forms {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, inferEnv, fresh, 0); err != nil {
				return err
			}
		}
	}

	return nil
}

func (i *InterfaceDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(i, func() (hm.Type, error) {
		mod, ok := env.(Env)
		if !ok {
			return nil, fmt.Errorf("InterfaceDecl.Infer: environment does not support module operations")
		}

		iface, found := mod.LocalNamedType(i.Name.Name)
		if !found {
			return nil, fmt.Errorf("interface %s not found", i.Name.Name)
		}

		// Infer the interface fields using composite environment
		inferEnv := &CompositeModule{
			primary: iface,
			lexical: env.(Env),
		}

		// Use phased inference approach (like ClassDecl) to avoid environment cloning
		if _, err := InferFormsWithPhases(ctx, i.Value.Forms, inferEnv, fresh); err != nil {
			return nil, err
		}

		i.Inferred = iface.(*Module)
		i.SetInferredType(iface)

		// Set doc string
		if i.DocString != "" {
			mod.SetDocString(i.Name.Name, i.DocString)
		}

		return iface, nil
	})
}

func (i *InterfaceDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Interfaces are pure type declarations - they don't have runtime values
	// Just register the interface module in the environment
	interfaceModule := NewModuleValue(i.Inferred)
	env.Bind(i.Name.Name, interfaceModule, i.Visibility)
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

	Inferred *Module
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
	mod, ok := env.(Env)
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

		memberMod, ok := memberType.(*Module)
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
		mod, ok := env.(Env)
		if !ok {
			return nil, fmt.Errorf("UnionDecl.Infer: environment does not support module operations")
		}

		unionType, found := mod.LocalNamedType(u.Name.Name)
		if !found {
			return nil, fmt.Errorf("union %s not found", u.Name.Name)
		}

		unionMod := unionType.(*Module)

		// Resolve and link each member type
		for _, memberSym := range u.Members {
			memberType, found := mod.NamedType(memberSym.Name)
			if !found {
				return nil, NewInferError(
					fmt.Errorf("union member %s not found", memberSym.Name),
					memberSym,
				)
			}

			memberMod, ok := memberType.(*Module)
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

func (u *UnionDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Unions are pure type declarations - register the union module
	unionModule := NewModuleValue(u.Inferred)
	env.Bind(u.Name.Name, unionModule, u.Visibility)
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
