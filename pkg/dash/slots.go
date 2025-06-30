package dash

import (
	"context"
	"fmt"

	"github.com/chewxy/hm"
)

type SlotDecl struct {
	Named      string
	Type_      TypeNode
	Value      Node
	Visibility Visibility
	Loc        *SourceLocation
}

var _ Node = SlotDecl{}
var _ Evaluator = SlotDecl{}

func (s SlotDecl) Body() hm.Expression {
	// TODO(vito): return Value? unclear how Body is used
	return s
}

func (s SlotDecl) GetSourceLocation() *SourceLocation { return s.Loc }

var _ Hoister = SlotDecl{}

func (c SlotDecl) Hoist(env hm.Env, fresh hm.Fresher, depth int) error {
	if depth == 0 {
		// first pass only collects classes
		return nil
	}

	if c.Type_ != nil {
		dt, err := c.Type_.Infer(env, fresh)
		if err != nil {
			return fmt.Errorf("SlotDecl.Hoist: Infer %T: %w", c.Type_, err)
		}

		env.Add(c.Named, hm.NewScheme(nil, dt))
	}

	return nil
}

func (s SlotDecl) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var err error

	var definedType hm.Type
	if s.Type_ != nil {
		definedType, err = s.Type_.Infer(env, fresh)
		if err != nil {
			return nil, err
		}
	}

	var inferredType hm.Type
	if s.Value != nil {
		inferredType, err = s.Value.Infer(env, fresh)
		if err != nil {
			return nil, err
		}

		if definedType != nil {
			if _, err := UnifyWithCompatibility(inferredType, definedType); err != nil {
				return nil, fmt.Errorf("SlotDecl.Infer: Unify %T(%s) ~ %T(%s): %s", inferredType, inferredType, definedType, definedType, err)
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

	if dashEnv, ok := env.(Env); ok {
		cur, defined := dashEnv.LocalSchemeOf(s.Named)
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

	env.Add(s.Named, hm.NewScheme(nil, definedType))
	return definedType, nil
}

func (s SlotDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	if s.Value == nil {
		// If no value is provided, this is just a type declaration
		// Add a null value to the environment as a placeholder
		env.Set(s.Named, NullValue{})
		return NullValue{}, nil
	}

	// Use direct evaluation without wrapping the error - let the specific node error be preserved
	val, err := EvalNode(ctx, env, s.Value)
	if err != nil {
		return nil, err // Don't wrap the error here - preserve the original location
	}

	// Add the value to the environment for future use
	// If it's a ModuleValue, use SetWithVisibility to track visibility
	env.SetWithVisibility(s.Named, val, s.Visibility)

	return val, nil
}

type ClassDecl struct {
	Named      string
	Value      Block
	Visibility Visibility // theoretically the type itself is public but its constructor value can be private
	Loc        *SourceLocation
}

var _ Node = ClassDecl{}
var _ Evaluator = ClassDecl{}

func (c ClassDecl) Body() hm.Expression { return c.Value }

func (c ClassDecl) GetSourceLocation() *SourceLocation { return c.Loc }

var _ Hoister = ClassDecl{}

func (c ClassDecl) Hoist(env hm.Env, fresh hm.Fresher, depth int) error {
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
	self := hm.NewScheme(nil, NonNullType{class})
	class.Add("self", self)
	env.Add(c.Named, self)

	hoistEnv := &CompositeModule{
		primary: class,
		lexical: env.(Env),
	}

	if depth > 0 {
		if err := c.Value.Hoist(hoistEnv, fresh, depth); err != nil {
			return err
		}
	}

	return nil
}

func (c ClassDecl) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
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

	for _, node := range c.Value.Forms {
		_, err := node.Infer(inferEnv, fresh)
		if err != nil {
			return nil, err
		}
	}

	// set special 'self' keyword to match the function signature.
	self := hm.NewScheme(nil, NonNullType{class})
	class.Add("self", self)
	env.Add(c.Named, self)

	return class, nil
}

func (c ClassDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	classEnv := env.Clone()

	modValue := classEnv.(Value)

	// Bind 'self' to the module
	classEnv.Set("self", modValue)

	// Evaluate the class body (Block) which contains all the slots
	for _, node := range c.Value.Forms {
		_, err := EvalNode(ctx, classEnv, node)
		if err != nil {
			return nil, fmt.Errorf("ClassDecl.Eval: evaluating class body for %q: %w", c.Named, err)
		}
	}

	// Add the class to the evaluation environment so it can be referenced
	env.Set(c.Named, modValue)

	return modValue, nil
}
