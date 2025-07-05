package bind

import (
	"context"
	"fmt"

	"github.com/vito/bind/pkg/hm"
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

	Inferred *Module
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

	// set special 'self' keyword to match the function signature.
	self := hm.NewScheme(nil, hm.NonNullType{Type: class})
	class.Add("self", self)

	// Create and add constructor function type to environment
	constructorParams := c.extractConstructorParameters()
	constructorType := c.buildConstructorType(constructorParams, class, fresh)
	constructorScheme := hm.NewScheme(nil, constructorType)
	env.Add(c.Named, constructorScheme)

	hoistEnv := &CompositeModule{
		primary: class,
		lexical: env.(Env),
	}

	if pass > 0 {
		if err := c.Value.Hoist(hoistEnv, fresh, pass); err != nil {
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

	// Create a constructor function type based on public non-function slots
	constructorParams := c.extractConstructorParameters()
	constructorType := c.buildConstructorType(constructorParams, class, fresh)

	// Add the constructor function type to the environment
	constructorScheme := hm.NewScheme(nil, constructorType)
	env.Add(c.Named, constructorScheme)

	// Validate directive applications
	for _, directive := range c.Directives {
		_, err := directive.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("ClassDecl.Infer: directive validation: %w", err)
		}
	}

	c.Inferred = class.(*Module)
	return constructorType, nil
}

// extractConstructorParameters extracts public non-function slots from a class body
func (c *ClassDecl) extractConstructorParameters() []SlotDecl {
	var params []SlotDecl
	for _, form := range c.Value.Forms {
		if slot, ok := form.(SlotDecl); ok {
			// Include public slots that are not functions
			if slot.Visibility == PublicVisibility {
				if _, isFun := slot.Value.(*FunDecl); !isFun {
					params = append(params, slot)
				}
			}
		}
	}
	return params
}

// buildConstructorType creates a function type for the constructor based on the parameters
func (c *ClassDecl) buildConstructorType(params []SlotDecl, classType hm.Type, fresh hm.Fresher) hm.Type {
	if len(params) == 0 {
		// No parameters, so constructor is a function that takes no arguments and returns the class type
		argType := &RecordType{Fields: []Keyed[*hm.Scheme]{}}
		return hm.NewFnType(argType, hm.NonNullType{Type: classType})
	}

	// Build record type for constructor parameters
	fields := make([]Keyed[*hm.Scheme], len(params))
	for i, param := range params {
		var paramType hm.Type

		if param.Type_ != nil {
			// Use the declared type - for now, use a type variable since we'd need
			// to infer it properly in context, which would require refactoring
			paramType = fresh.Fresh()
		} else {
			// No explicit type, use a type variable
			paramType = fresh.Fresh()
		}

		// Check if parameter has a default value to determine if it's nullable
		if param.Value != nil {
			// Has default value, so parameter is optional (nullable)
			fields[i] = Keyed[*hm.Scheme]{
				Key:   param.Named,
				Value: hm.NewScheme(nil, paramType),
			}
		} else {
			// No default value, so parameter is required (non-null)
			fields[i] = Keyed[*hm.Scheme]{
				Key:   param.Named,
				Value: hm.NewScheme(nil, hm.NonNullType{Type: paramType}),
			}
		}
	}

	argType := &RecordType{Fields: fields}
	return hm.NewFnType(argType, hm.NonNullType{Type: classType})
}

func (c *ClassDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	if c.Inferred == nil {
		panic(fmt.Errorf("ClassDecl.Eval: class %q has not been inferred", c.Named))
	}

	// Extract constructor parameters from public non-function slots
	constructorParams := c.extractConstructorParameters()

	// Create a constructor function that evaluates the class body when called
	constructor := &ConstructorFunction{
		ClassName:  c.Named,
		ClassDecl:  c,
		Parameters: constructorParams,
		ClassType:  c.Inferred,
	}

	// Add the constructor to the evaluation environment
	env.SetWithVisibility(c.Named, constructor, c.Visibility)

	return constructor, nil
}
