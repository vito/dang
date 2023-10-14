package dash

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

func (c ClassDecl) Hoist(env hm.Env, fresh hm.Fresher, depth int) error {
	mod := env.(*Module)

	class, found := mod.NamedType(c.Named)
	if !found {
		class = NewModule(c.Named)
		mod.AddClass(class)
	}

	// set special 'self' keyword to match the function signature.
	self := hm.NewScheme(nil, class)
	class.Add("self", self)
	env.Add(c.Named, self)

	// TODO: hacky, see elsewhere
	class.Parent = mod
	defer func() {
		class.Parent = nil
	}()

	if depth > 0 {
		if err := c.Value.Hoist(class, fresh, depth); err != nil {
			return err
		}
	}

	return nil
}

func (c ClassDecl) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod := env.(*Module)

	class, found := mod.NamedType(c.Named)
	if !found {
		class = NewModule(c.Named)
		mod.AddClass(class)
	}

	// TODO: this feels a little hacky, but we basically want classes to infer by
	// writing to the class while using the original env to resolve types/etc.,
	// so we set the class - even an existing - parent to the current call site,
	// and set it back to nil after as we don't want methods selected from the
	// class to actually recurse to the original context.
	class.Parent = mod

	_, err := c.Value.Infer(class, fresh)
	if err != nil {
		return nil, err
	}

	class.Parent = nil

	// set special 'self' keyword to match the function signature.
	self := hm.NewScheme(nil, class)
	class.Add("self", self)
	env.Add(c.Named, self)

	return class, nil
}
