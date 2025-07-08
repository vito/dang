package sprout

import (
	"context"
	"fmt"

	"github.com/vito/sprout/pkg/hm"
)

type SlotDecl struct {
	Named      string
	Type_      TypeNode
	Value      Node
	Visibility Visibility
	Directives []DirectiveApplication
	Loc        *SourceLocation
}

var _ Declarer = SlotDecl{}

func (f SlotDecl) IsDeclarer() bool {
	// SlotDecl always declares a symbol (the Named field)
	// regardless of what its Value is
	return true
}

var _ Node = SlotDecl{}
var _ Evaluator = SlotDecl{}
var _ Hoister = SlotDecl{}

func (s SlotDecl) DeclaredSymbols() []string {
	return []string{s.Named} // Slot declarations declare their name
}

func (s SlotDecl) ReferencedSymbols() []string {
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

func (s SlotDecl) Body() hm.Expression {
	// TODO(vito): return Value? unclear how Body is used
	return s
}

func (s SlotDecl) GetSourceLocation() *SourceLocation { return s.Loc }

func (s SlotDecl) Hoist(env hm.Env, fresh hm.Fresher, pass int) error {
	// If the slot value is a hoister, delegate
	if funDecl, ok := s.Value.(Hoister); ok {
		return funDecl.Hoist(env, fresh, pass)
	}

	// For non-function slots, hoisting is handled in the normal inference phase
	return nil
}

func (s SlotDecl) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var err error

	var definedType hm.Type
	if s.Type_ != nil {
		definedType, err = s.Type_.Infer(env, fresh)
		if err != nil {
			return nil, WrapInferError(err, s)
		}
	}

	var inferredType hm.Type
	if s.Value != nil {
		inferredType, err = s.Value.Infer(env, fresh)
		if err != nil {
			return nil, WrapInferError(err, s.Value)
		}

		if definedType != nil {
			if _, err := hm.Unify(definedType, inferredType); err != nil {
				return nil, WrapInferError(err, s.Value)
			}
		} else {
			definedType = inferredType
		}
	}

	if definedType == nil {
		return nil, fmt.Errorf("SlotDecl.Infer: no type or value")
	}

	// definedType = definedType.Apply(subs)

	// if !definedType.Eq(inferredType) {
	// 	return nil, fmt.Errorf("SlotDecl.Infer: %q mismatch: defined as %s, inferred as %s", s.Named, definedType, inferredType)
	// }

	if bindEnv, ok := env.(Env); ok {
		cur, defined := bindEnv.LocalSchemeOf(s.Named)
		if defined {
			curT, curMono := cur.Type()
			if !curMono {
				return nil, fmt.Errorf("SlotDecl.Infer: TODO: type is not monomorphic")
			}

			if !definedType.Eq(curT) {
				return nil, fmt.Errorf("SlotDecl.Infer: %q already defined as %s", s.Named, curT)
			}
		}

		bindEnv.SetVisibility(s.Named, s.Visibility)
	}

	// Validate directive applications
	for _, directive := range s.Directives {
		_, err := directive.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("SlotDecl.Infer: directive validation: %w", err)
		}
	}

	env.Add(s.Named, hm.NewScheme(nil, definedType))
	return definedType, nil
}

func (s SlotDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	val, defined := env.GetLocal(s.Named)
	if defined {
		// already defined (e.g. through constructor), nothing to do
		return val, nil
	}

	if s.Value == nil {
		// If no value is provided, this is just a type declaration
		// Add a null value to the environment as a placeholder
		env.SetWithVisibility(s.Named, NullValue{}, s.Visibility)
		return NullValue{}, nil
	}

	// Evaluate the value expression with proper error context
	val, err := EvalNode(ctx, env, s.Value)
	if err != nil {
		// Convert error with proper source location from the failing node
		return nil, CreateEvalError(ctx, err, s.Value)
	}

	// Add the value to the environment for future use
	// If it's a ModuleValue, use SetWithVisibility to track visibility
	env.SetWithVisibility(s.Named, val, s.Visibility)

	return val, nil
}

type ClassDecl struct {
	Named      string
	Value      Block
	Visibility Visibility
	Directives []DirectiveApplication
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
	return []string{c.Named} // Class declarations declare their name
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

func (c *ClassDecl) Hoist(env hm.Env, fresh hm.Fresher, pass int) error {
	mod, ok := env.(Env)
	if !ok {
		return fmt.Errorf("ClassDecl.Hoist: environment does not support module operations")
	}

	class, found := mod.NamedType(c.Named)
	if !found {
		class = NewModule(c.Named)
		mod.AddClass(c.Named, class)
	}

	inferEnv := &CompositeModule{
		primary: class,
		lexical: env.(Env),
	}

	// Create a constructor function type based on public non-function slots
	constructorParams := c.extractConstructorParameters()
	constructorType, err := c.buildConstructorType(inferEnv, constructorParams, class.(*Module), fresh)
	if err != nil {
		return err
	}
	c.ConstructorFnType = constructorType

	// Add the constructor function type to the environment
	constructorScheme := hm.NewScheme(nil, constructorType)
	env.Add(c.Named, constructorScheme)

	if pass > 0 {
		// set special 'self' keyword to match the function signature.
		self := hm.NewScheme(nil, hm.NonNullType{Type: class})
		class.Add("self", self)

		if err := c.Value.Hoist(inferEnv, fresh, pass); err != nil {
			return err
		}
	}

	return nil
}

func (c *ClassDecl) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod, ok := env.(Env)
	if !ok {
		return nil, fmt.Errorf("ClassDecl.Infer: environment does not support module operations")
	}

	class, found := mod.NamedType(c.Named)
	if !found {
		class = NewModule(c.Named)
		mod.AddClass(c.Named, class)
	}

	inferEnv := &CompositeModule{
		primary: class,
		lexical: env.(Env),
	}

	// Use phased inference approach to handle forward references within the class body
	if _, err := InferFormsWithPhases(c.Value.Forms, inferEnv, fresh); err != nil {
		return nil, err
	}

	self := hm.NewScheme(nil, hm.NonNullType{Type: class})
	class.Add("self", self)

	// Validate directive applications
	for _, directive := range c.Directives {
		_, err := directive.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("ClassDecl.Infer: directive validation: %w", err)
		}
	}

	c.Inferred = class.(*Module)
	return c.ConstructorFnType, nil
}

// extractConstructorParametersAndCleanBody extracts public non-function slots as constructor
// parameters and returns the filtered forms that should be evaluated in the class body
func (c *ClassDecl) extractConstructorParameters() []SlotDecl {
	var params []SlotDecl

	for _, form := range c.Value.Forms {
		if slot, ok := form.(SlotDecl); ok {
			// Check if this is a public non-function slot (constructor parameter)
			if slot.Visibility == PublicVisibility {
				if _, isFun := slot.Value.(*FunDecl); !isFun {
					// This is a constructor parameter - extract it but don't include in filtered forms
					params = append(params, slot)
				}
			}
		}
	}

	return params
}

// buildConstructorType creates a function type for the constructor based on the parameters
func (c *ClassDecl) buildConstructorType(env hm.Env, params []SlotDecl, classType *Module, fresh hm.Fresher) (*hm.FunctionType, error) {
	fnDecl := FunctionBase{
		Args: params,
		Body: Block{
			Forms: []Node{
				ValueNode{Val: NewModuleValue(classType)},
			},
		},
	}
	return fnDecl.inferFunctionType(env, fresh, false, nil, classType.Named+" Constructor")
}

func (c *ClassDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	if c.Inferred == nil {
		panic(fmt.Errorf("ClassDecl.Eval: class %q has not been inferred", c.Named))
	}

	// Extract constructor parameters and get filtered class body forms
	constructorParams := c.extractConstructorParameters()

	// Create a constructor function that evaluates the class body when called
	constructor := &ConstructorFunction{
		Closure:        env,
		ClassName:      c.Named,
		Parameters:     constructorParams,
		ClassType:      c.Inferred,
		ClassBodyForms: c.Value.Forms,
		FnType:         c.ConstructorFnType,
	}

	// Add the constructor to the evaluation environment
	env.SetWithVisibility(c.Named, constructor, c.Visibility)

	return constructor, nil
}
