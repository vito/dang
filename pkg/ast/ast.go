package ast

import (
	"fmt"

	"github.com/chewxy/hm"
)

type Node interface {
	hm.Expression
	hm.Inferer
}

type Keyed[X any] struct {
	Key   string
	Value X
}

type Visibility int

const (
	PublicVisibility Visibility = iota
	PrivateVisibility
)

type FunCall struct {
	Fun  Node
	Args Record
}

var _ Node = FunCall{}

func (c FunCall) Body() hm.Expression { return c.Args }

func (c FunCall) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	fun, err := c.Fun.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	switch ft := fun.(type) {
	case *hm.FunctionType:
		return ft.Ret(false), nil
	default:
		return nil, fmt.Errorf("FunCall.Infer: expected function, got %s (%T)", fun, fun)
	}
}

var _ hm.Apply = FunCall{}

func (c FunCall) Fn() hm.Expression { return c.Fun }

type FunDecl struct {
	Named      string
	Args       []SlotDecl
	Form       Node
	Ret        TypeNode
	Visibility Visibility
}

var _ hm.Expression = FunDecl{}

func (f FunDecl) Body() hm.Expression { return f.Form }

var _ hm.Inferer = FunDecl{}

func (f FunDecl) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// TODO: Lambda semantics

	var err error

	args := []Keyed[*hm.Scheme]{}
	for _, arg := range f.Args {
		var definedArgType hm.Type

		if arg.Type_ != nil {
			definedArgType, err = arg.Type_.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("FuncDecl.Infer arg: %w", err)
			}
		}

		var inferredValType hm.Type
		if arg.Value != nil {
			inferredValType, err = arg.Value.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("FuncDecl.Infer arg: %w", err)
			}
		}

		if definedArgType != nil && inferredValType != nil {
			if !definedArgType.Eq(inferredValType) {
				return nil, fmt.Errorf("FuncDecl.Infer arg: %q mismatch: defined as %s, inferred as %s", arg.Named, definedArgType, inferredValType)
			}
		} else if definedArgType != nil {
			inferredValType = definedArgType
		} else if inferredValType != nil {
			definedArgType = inferredValType
		} else {
			return nil, fmt.Errorf("FuncDecl.Infer arg: %q has no type or value", arg.Named)
		}

		args = append(args, Keyed[*hm.Scheme]{arg.Named, hm.NewScheme(nil, definedArgType)})
	}

	var definedRet hm.Type

	if f.Ret != nil {
		definedRet, err = f.Ret.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("FuncDecl.Infer: Ret: %w", err)
		}
	}

	inferredRet, err := f.Form.Infer(env, fresh)
	if err != nil {
		return nil, fmt.Errorf("FuncDecl.Infer: Form: %w", err)
	}

	if definedRet != nil {
		// TODO: Unify?
		if !definedRet.Eq(inferredRet) {
			return nil, fmt.Errorf("FuncDecl.Infer: %q mismatch: defined as %s, inferred as %s", f.Named, definedRet, inferredRet)
		}
	}

	return hm.NewFnType(NewRecordType("", args...), inferredRet), nil
}

// var _ hm.LetRec = FunDecl{}

// func (f FunDecl) Def() hm.Expression { return f.Form }

// func (f FunDecl) IsRecursive() bool { return true }

// var _ hm.Lambda = FunDecl{}

// func (f FunDecl) Name() string {
// 	if f.Named != "" {
// 		return f.Named
// 	}
// 	return fmt.Sprintf("(%v): %s", f.Args, f.Ret)
// }

// func (f FunDecl) IsLambda() bool { return true }

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

		if definedType == nil {
			definedType = inferredType
		}
		// scheme, err := Infer(env, s.Value)
		// if err != nil {
		// 	return nil, err
		// }

		// 		if definedType != nil {
		// 			it, isMono := scheme.Type()
		// 			if isMono {
		// 				if !definedType.Eq(it) {
		// 					return nil, fmt.Errorf("SlotDecl.Infer: %q mismatch: defined as %s, expected %s", s.Named, definedType, it)
		// 				}
		// 			} else {
		// 				subs, err := hm.Unify(it, definedType) // TODO does this make sense?
		// 				if err != nil {
		// 					return nil, fmt.Errorf("SlotDecl.Infer: Unify: %w", err)
		// 				}
		// 				scheme = scheme.Apply(subs).(*hm.Scheme)
		// 			}
		// 		}

		// 		env.Add(s.Named, scheme)
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

	env.Add(c.Named, hm.NewScheme(nil, hm.NewFnType(argsType, NonNullType{class})))

	// set special 'self' keyword to match the function signature.
	class.Add("self", hm.NewScheme(nil, hm.NewFnType(argsType, NonNullType{class})))

	// TODO: assign constructor

	return class, nil
}

var _ Hoister = ClassDecl{}

func (c ClassDecl) Hoist(env hm.Env) hm.Env {
	e := env.(*Module)
	class := NewModule(c.Named)
	e = e.AddClass(class)
	for _, form := range c.Value.Forms {
		if hoister, ok := form.(Hoister); ok {
			class = hoister.Hoist(class).(*Module)
		}
	}

	args := []Keyed[*hm.Scheme]{}
	for name, slot := range class.vars { // TODO: respect privacy, order
		// TODO this is kind of interesting... we just reflect all of them as-is
		// instead of special-casing required slots. the result is you can just
		// override any slot, even function implementations.
		args = append(args, Keyed[*hm.Scheme]{name, slot})
	}
	argsType := NewRecordType(c.Named, args...)
	env.Add(c.Named, hm.NewScheme(nil, hm.NewFnType(argsType, NonNullType{class})))

	// set special 'self' keyword to match the function signature.
	class.Add("self", hm.NewScheme(nil, hm.NewFnType(argsType, NonNullType{class})))

	return e
}

// // TODO: is this proper use of Var?
// var _ hm.Var = SlotDecl{}

// // TODO: prob don't want to do this, it takes precedence over Inferer
// func (s SlotDecl) Type() hm.Type {
// 	t, _ := s.Scheme.Type()
// 	return t
// }

// func (s SlotDecl) Name() string { return s.Named }

// 	switch et := expr.(type) {
// 	case Literal:
// 	case Var:
// 	case Lambda:
// 	case Apply:
// 	case LetRec:
// 	case Let:
// 	default:
// 		return errors.Errorf("Expression of %T is unhandled", expr)
// 	}

type List struct {
	Elements []Node
}

var _ Node = List{}

func (l List) Infer(env hm.Env, f hm.Fresher) (hm.Type, error) {
	if len(l.Elements) == 0 {
		// TODO: is this right?
		return ListType{f.Fresh()}, nil
	}

	var t hm.Type
	for _, el := range l.Elements {
		et, err := el.Infer(env, f)
		if err != nil {
			return nil, err
		}
		if t == nil {
			t = et
		} else if !t.Eq(et) {
			// TODO: is this right?
			return ListType{f.Fresh()}, nil
		}
	}
	return ListType{t}, nil
}

func (l List) Body() hm.Expression { return l }

// TODO record literals?

type Symbol struct {
	Name string
}

var _ Node = Symbol{}

func (s Symbol) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	scheme, found := env.SchemeOf(s.Name)
	if !found {
		return nil, fmt.Errorf("Symbol.Infer: %q not found in env", s.Name)
	}
	t, _ := scheme.Type()
	return t, nil
}

func (s Symbol) Body() hm.Expression { return s }

type Select struct {
	Receiver Node
	Field    string
}

var _ Node = Select{}

func (d Select) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := d.Receiver.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	nn, ok := lt.(NonNullType)
	if !ok {
		return nil, fmt.Errorf("Select.Infer: expected %T, got %T", nn, lt)
	}
	rec, ok := nn.Type.(*Module)
	if !ok {
		return nil, fmt.Errorf("Select.Infer: expected %T, got %T", rec, nn.Type)
	}
	scheme, found := rec.SchemeOf(d.Field)
	if !found {
		return nil, fmt.Errorf("Select.Infer: field %q not found in record %s", d.Field, rec)
	}
	t, mono := scheme.Type()
	if !mono {
		return nil, fmt.Errorf("Select.Infer: type of field %q is not monomorphic", d.Field)
	}
	return t, nil
}

func (d Select) Body() hm.Expression { return d }

type Default struct {
	Left  Node
	Right Node
}

var _ Node = Default{}

func (d Default) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := d.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := d.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	lt = NonNullType{lt}
	if !lt.Eq(rt) {
		return nil, fmt.Errorf("Default.Infer: mismatched types: %s != %s", lt, rt)
	}
	return rt, nil
}

func (d Default) Body() hm.Expression { return d }

type Null struct{}

var _ Node = Null{}

func (n Null) Body() hm.Expression { return n }

func (Null) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return fresh.Fresh(), nil
}

var (
	// Null does not have a type. Its type is always inferred as a free variable.
	// NullType    = NewClass("Null")

	BooleanType = NewModule("Boolean")
	StringType  = NewModule("String")
	IntegerType = NewModule("Integer")
)

type String struct {
	Value string
}

var _ Node = String{}

func (s String) Type() hm.Type       { return NonNullType{StringType} }
func (s String) Body() hm.Expression { return s }

func (s String) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return s.Type(), nil
}

type Quoted struct {
	Quoter string
	Raw    string
}

type Boolean bool

var _ Node = Boolean(false)

func (b Boolean) Type() hm.Type       { return NonNullType{BooleanType} }
func (b Boolean) Body() hm.Expression { return b }

func (b Boolean) Infer(hm.Env, hm.Fresher) (hm.Type, error) {
	return b.Type(), nil
}

type Integer int

var _ Node = Integer(0)

func (i Integer) Body() hm.Expression { return i }

func (i Integer) Type() hm.Type { return NonNullType{IntegerType} }

func (i Integer) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return i.Type(), nil
}

type Self struct{}

var _ Node = Self{}

func (Self) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return env.Clone().(*Module), nil
}

func (s Self) Body() hm.Expression { return s }
