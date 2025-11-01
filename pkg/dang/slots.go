package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

type SlotDecl struct {
	InferredTypeHolder
	Name       *Symbol
	Type_      TypeNode
	Value      Node
	Visibility Visibility
	Directives []*DirectiveApplication
	DocString  string
	Loc        *SourceLocation
}

var _ Declarer = SlotDecl{}

func (f SlotDecl) IsDeclarer() bool {
	// SlotDecl always declares a symbol (the Named field)
	// regardless of what its Value is
	return true
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
	// If the slot value is a hoister, delegate
	if funDecl, ok := s.Value.(Hoister); ok {
		return funDecl.Hoist(ctx, env, fresh, pass)
	}

	// For non-function slots, hoisting is handled in the normal inference phase
	return nil
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
		} else {
			definedType = inferredType
		}
	}

	if definedType == nil {
		return nil, fmt.Errorf("SlotDecl.Infer: no type or value")
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
		val, defined := env.GetLocal(s.Name.Name)
		if defined {
			// already defined (e.g. through constructor), nothing to do
			return val, nil
		}

		if s.Value == nil {
			// Check if this is a required (non-null) type without a value
			// This is a runtime error - required types must have values
			if inferredType := s.GetInferredType(); inferredType != nil {
				if _, isNonNull := inferredType.(hm.NonNullType); isNonNull {
					return nil, fmt.Errorf("required slot %q (type %s) has no value", s.Name.Name, inferredType.Name())
				}
			}

			// If no value is provided, this is just a type declaration
			// Add a null value to the environment as a placeholder
			env.SetWithVisibility(s.Name.Name, NullValue{}, s.Visibility)
			return NullValue{}, nil
		}

		// Evaluate the value expression with proper error context
		val, err := EvalNode(ctx, env, s.Value)
		if err != nil {
			// Convert error with proper source location from the failing node
			return nil, err
		}

		// Add the value to the environment for future use
		// If it's a ModuleValue, use SetWithVisibility to track visibility
		env.SetWithVisibility(s.Name.Name, val, s.Visibility)

		return val, nil
	})
}

func (s *SlotDecl) Walk(fn func(Node) bool) {
	if !fn(s) {
		return
	}
	if s.Type_ != nil {
		// TypeNode doesn't have Walk method - skip
	}
	if s.Value != nil {
		s.Value.Walk(fn)
	}
}

type ClassDecl struct {
	InferredTypeHolder
	Name       *Symbol
	Value      *Block
	Implements []*Symbol
	Visibility Visibility
	Directives []*DirectiveApplication
	DocString  string
	Loc        *SourceLocation

	Inferred          *Module
	ConstructorFnType *hm.FunctionType
}

func (f *ClassDecl) IsDeclarer() bool {
	return true
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

func (c *ClassDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	mod, ok := env.(Env)
	if !ok {
		return fmt.Errorf("ClassDecl.Hoist: environment does not support module operations")
	}

	class, found := mod.NamedType(c.Name.Name)
	if !found {
		class = NewModule(c.Name.Name)
		mod.AddClass(c.Name.Name, class)
	}

	inferEnv := &CompositeModule{
		primary: class,
		lexical: env.(Env),
	}

	// Create a constructor function type based on public non-function slots
	constructorParams := c.extractConstructorParameters()
	constructorType, err := c.buildConstructorType(ctx, inferEnv, constructorParams, class.(*Module), fresh)
	if err != nil {
		return err
	}
	c.ConstructorFnType = constructorType

	// Add the constructor function type to the environment
	constructorScheme := hm.NewScheme(nil, constructorType)
	env.Add(c.Name.Name, constructorScheme)

	if pass == 0 {
		// Link the implementation
		if len(c.Implements) > 0 {
			classMod := class.(*Module)
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

				// Add "blindly" initially, we'll validate later
				classMod.AddInterface(ifaceType)
				ifaceMod.AddImplementer(classMod)
			}
		}
	}

	if pass > 0 {
		// set special 'self' keyword to match the function signature.
		self := hm.NewScheme(nil, hm.NonNullType{Type: class})
		class.Add("self", self)

		if err := c.Value.Hoist(ctx, inferEnv, fresh, pass); err != nil {
			return err
		}
	}

	return nil
}

func (c *ClassDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod, ok := env.(Env)
	if !ok {
		return nil, fmt.Errorf("ClassDecl.Infer: environment does not support module operations")
	}

	class, found := mod.NamedType(c.Name.Name)
	if !found {
		class = NewModule(c.Name.Name)
		mod.AddClass(c.Name.Name, class)

		// Store doc string for the class name in the environment
		if c.DocString != "" {
			mod.SetDocString(c.Name.Name, c.DocString)
		}
	}

	// Set this early so we can at least partially infer.
	c.Inferred = class.(*Module)

	self := hm.NewScheme(nil, hm.NonNullType{Type: class})
	class.Add("self", self)

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

	// Use phased inference approach to handle forward references within the class body
	if _, err := InferFormsWithPhases(ctx, c.Value.Forms, inferEnv, fresh); err != nil {
		return nil, err
	}

	// Validate interface implementations after fields have been inferred
	if len(c.Implements) > 0 {
		classMod := c.Inferred
		for _, ifaceSym := range c.Implements {
			if err := c.validateInterfaceImplementations(classMod, mod, ifaceSym); err != nil {
				return nil, WrapInferError(err, ifaceSym)
			}
		}
	}

	return c.ConstructorFnType, nil
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

	// Check that all interface fields are present in the class
	for field, fieldScheme := range ifaceMod.Bindings(PrivateVisibility) {
		classFieldScheme, classHasField := classMod.SchemeOf(field)
		if !classHasField {
			return fmt.Errorf("missing field %q required by interface %s", field, ifaceSym.Name)
		}

		// Get the types from the schemes
		ifaceFieldType, _ := fieldScheme.Type()
		classFieldType, _ := classFieldScheme.Type()

		// Validate field type compatibility
		if err := validateFieldImplementation(field, ifaceFieldType, classFieldType, ifaceSym.Name, c.Name.Name); err != nil {
			return err
		}
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

// buildConstructorType creates a function type for the constructor based on the parameters
func (c *ClassDecl) buildConstructorType(ctx context.Context, env hm.Env, params []*SlotDecl, classType *Module, fresh hm.Fresher) (*hm.FunctionType, error) {
	fnDecl := FunctionBase{
		Args: params,
		Body: &Block{
			Forms: []Node{
				&ValueNode{Val: NewModuleValue(classType)},
			},
		},
	}
	return fnDecl.inferFunctionType(ctx, env, fresh, false, nil, classType.Named+" Constructor")
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

		// Extract constructor parameters and get filtered class body forms
		constructorParams := c.extractConstructorParameters()

		// Create a constructor function that evaluates the class body when called
		constructor := &ConstructorFunction{
			Closure:        env,
			ClassName:      c.Name.Name,
			Parameters:     constructorParams,
			ClassType:      c.Inferred,
			ClassBodyForms: c.Value.Forms,
			FnType:         c.ConstructorFnType,
		}

		// Add the constructor to the evaluation environment
		env.SetWithVisibility(c.Name.Name, constructor, c.Visibility)

		return constructor, nil
	})
}

func (c *ClassDecl) Walk(fn func(Node) bool) {
	if !fn(c) {
		return
	}
	c.Value.Walk(fn)
}

type EnumDecl struct {
	InferredTypeHolder
	Name       *Symbol
	Values     []*Symbol
	Visibility Visibility
	DocString  string
	Loc        *SourceLocation

	Inferred *Module
}

func (e *EnumDecl) IsDeclarer() bool {
	return true
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

	// Create the enum type (module) if it doesn't exist
	enumType, found := mod.NamedType(e.Name.Name)
	if !found {
		enumType = NewModule(e.Name.Name)
		enumType.(*Module).Kind = EnumKind
		mod.AddClass(e.Name.Name, enumType)
	}

	e.Inferred = enumType.(*Module)

	// Add the enum module to the environment so it can be referenced
	// Note: We add the enum type itself, not wrapped in NonNullType, matching GraphQL enum behavior
	enumScheme := hm.NewScheme(nil, enumType)
	env.Add(e.Name.Name, enumScheme)

	if pass > 0 {
		// Add each enum value as a field in the enum module
		for _, value := range e.Values {
			// Each enum value has the type of the enum itself (not wrapped)
			valueScheme := hm.NewScheme(nil, enumType)
			enumType.Add(value.Name, valueScheme)

			// Store doc string if present
			if e.DocString != "" {
				mod.SetDocString(e.Name.Name, e.DocString)
			}
		}
	}

	return nil
}

func (e *EnumDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod, ok := env.(Env)
	if !ok {
		return nil, fmt.Errorf("EnumDecl.Infer: environment does not support module operations")
	}

	enumType, found := mod.NamedType(e.Name.Name)
	if !found {
		enumType = NewModule(e.Name.Name)
		enumType.(*Module).Kind = EnumKind
		mod.AddClass(e.Name.Name, enumType)

		if e.DocString != "" {
			mod.SetDocString(e.Name.Name, e.DocString)
		}
	}

	e.Inferred = enumType.(*Module)
	e.SetInferredType(enumType)

	return enumType, nil
}

func (e *EnumDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Create a module value for the enum
	enumModule := NewModuleValue(e.Inferred)

	// Add each enum value to the module
	for _, value := range e.Values {
		enumModule.Values[value.Name] = EnumValue{
			Val:      value.Name,
			EnumType: e.Inferred,
		}
	}

	// Register the enum module in the environment
	env.SetWithVisibility(e.Name.Name, enumModule, e.Visibility)

	return enumModule, nil
}

func (e *EnumDecl) Walk(fn func(Node) bool) {
	if !fn(e) {
		return
	}
	// Enum values are just symbols, no need to walk them
}

type ScalarDecl struct {
	InferredTypeHolder
	Name       *Symbol
	Visibility Visibility
	DocString  string
	Loc        *SourceLocation

	Inferred *Module
}

func (s *ScalarDecl) IsDeclarer() bool {
	return true
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

	// Create the scalar type (module) if it doesn't exist
	scalarType, found := mod.NamedType(s.Name.Name)
	if !found {
		scalarType = NewModule(s.Name.Name)
		scalarType.(*Module).Kind = ScalarKind
		mod.AddClass(s.Name.Name, scalarType)
	}

	s.Inferred = scalarType.(*Module)

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

	scalarType, found := mod.NamedType(s.Name.Name)
	if !found {
		scalarType = NewModule(s.Name.Name)
		scalarType.(*Module).Kind = ScalarKind
		mod.AddClass(s.Name.Name, scalarType)

		if s.DocString != "" {
			mod.SetDocString(s.Name.Name, s.DocString)
		}
	}

	s.Inferred = scalarType.(*Module)
	s.SetInferredType(scalarType)

	return scalarType, nil
}

func (s *ScalarDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Scalars are just type placeholders, similar to enums but with no values
	// The actual scalar values come from GraphQL or are just strings
	scalarModule := NewModuleValue(s.Inferred)

	// Register the scalar type in the environment
	env.SetWithVisibility(s.Name.Name, scalarModule, s.Visibility)

	return scalarModule, nil
}

func (s *ScalarDecl) Walk(fn func(Node) bool) {
	if !fn(s) {
		return
	}
	// Scalar declarations have no children to walk
}

type InterfaceDecl struct {
	InferredTypeHolder
	Name       *Symbol
	Value      *Block
	Visibility Visibility
	DocString  string
	Loc        *SourceLocation

	Inferred *Module
}

func (i *InterfaceDecl) IsDeclarer() bool {
	return true
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

	// Pass 0: Register the interface type
	if pass == 0 {
		iface := NewModule(i.Name.Name)
		iface.Kind = InterfaceKind
		mod.AddClass(i.Name.Name, iface)

		// Add the interface type to the environment so it can be referenced
		interfaceScheme := hm.NewScheme(nil, iface)
		env.Add(i.Name.Name, interfaceScheme)
	}

	// Interface fields are handled in Infer, not Hoist
	return nil
}

func (i *InterfaceDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(i, func() (hm.Type, error) {
		mod, ok := env.(Env)
		if !ok {
			return nil, fmt.Errorf("InterfaceDecl.Infer: environment does not support module operations")
		}

		iface, found := mod.NamedType(i.Name.Name)
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
	env.SetWithVisibility(i.Name.Name, interfaceModule, i.Visibility)
	return interfaceModule, nil
}

func (i *InterfaceDecl) Walk(fn func(Node) bool) {
	if !fn(i) {
		return
	}
	i.Value.Walk(fn)
}
