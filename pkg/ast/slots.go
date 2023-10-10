package ast

import (
	"fmt"

	"github.com/chewxy/hm"
)

type SlotDecl struct {
	Named      string
	Type_      TypeNode
	Value      Node
	Visibility Visibility
}

var _ Node = SlotDecl{}

func (s SlotDecl) Body() hm.Expression {
	// TODO(vito): return Value? unclear how Body is used
	return s
}

var _ Hoister = SlotDecl{}

func (c SlotDecl) Hoist(env hm.Env, fresh hm.Fresher) error {
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
			_, err = hm.Unify(inferredType, definedType)
			if err != nil {
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

	cur, defined := env.SchemeOf(s.Named)
	if defined {
		curT, curMono := cur.Type()
		if !curMono {
			return nil, fmt.Errorf("SlotDecl.Infer: TODO: type is not monomorphic")
		}

		if !definedType.Eq(curT) {
			return nil, fmt.Errorf("SlotDecl.Infer: %q already defined as %s", s.Named, curT)
		}
	}

	if definedType != nil {
		env.Add(s.Named, hm.NewScheme(nil, definedType))
		return definedType, nil
	} else {
		return nil, fmt.Errorf("SlotDecl.Infer: no type or value")
	}
}

type ClassDecl struct {
	Named      string
	Value      Block
	Visibility Visibility // theoretically the type itself is public but its constructor value can be private
}

var _ Node = ClassDecl{}

func (c ClassDecl) Body() hm.Expression { return c.Value }

var _ Hoister = ClassDecl{}

func (c ClassDecl) Hoist(env hm.Env, fresh hm.Fresher) error {
	mod := env.(*Module)

	class := NewModule(c.Named)

	// TODO: hacky, see elsewhere
	class.Parent = mod
	defer func() {
		class.Parent = nil
	}()

	mod.AddClass(class)

	if err := c.Value.Hoist(class, fresh); err != nil {
		return err
	}

	args := []Keyed[*hm.Scheme]{}
	for name, slot := range class.vars { // TODO: respect privacy, order
		// TODO this is kind of interesting... we just reflect all of them as-is
		// instead of special-casing required slots. the result is you can just
		// override any slot, even function implementations.
		args = append(args, Keyed[*hm.Scheme]{name, slot})
	}
	argsType := NewRecordType(c.Named, args...)

	// set special 'self' keyword to match the function signature.
	self := hm.NewScheme(nil, hm.NewFnType(argsType, NonNullType{class}))
	class.Add("self", self)
	env.Add(c.Named, self)
	return nil
}

func (c ClassDecl) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	e := env.(*Module)

	class, found := e.classes[c.Named]
	if !found {
		class = NewModule(c.Named)
		e.AddClass(class)
	}

	// TODO: this feels a little hacky, but we basically want classes to infer by
	// writing to the class while using the original env to resolve types/etc.,
	// so we set the class - even an existing - parent to the current call site,
	// and set it back to nil after as we don't want methods selected from the
	// class to actually recurse to the original context.
	class.Parent = e

	_, err := c.Value.Infer(class, fresh)
	if err != nil {
		return nil, err
	}

	class.Parent = nil

	args := []Keyed[*hm.Scheme]{}
	for name, slot := range class.vars { // TODO: respect privacy, order
		// TODO this is kind of interesting... we just reflect all of them as-is
		// instead of special-casing required slots. the result is you can just
		// override any slot, even function implementations.
		args = append(args, Keyed[*hm.Scheme]{name, slot})
	}
	argsType := NewRecordType(c.Named, args...)

	// set special 'self' keyword to match the function signature.
	self := hm.NewScheme(nil, hm.NewFnType(argsType, NonNullType{class}))
	class.Add("self", self)
	env.Add(c.Named, self)

	return class, nil
}
