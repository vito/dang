package dash

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
		for _, arg := range c.Args {
			k, v := arg.Key, arg.Value

			it, err := v.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("FunCall.Infer: %w", err)
			}

			scheme, has := ft.Arg().(*RecordType).SchemeOf(k)
			if !has {
				return nil, fmt.Errorf("FunCall.Infer: %q not found in %s", k, ft.Arg())
			}

			dt, isMono := scheme.Type()
			if !isMono {
				return nil, fmt.Errorf("FunCall.Infer: %q is not monomorphic", k)
			}

			if _, err := hm.Unify(dt, it); err != nil {
				return nil, fmt.Errorf("FunCall.Infer: %q cannot unify (%s ~ %s): %w", k, dt, it, err)
			}
		}
		// TODO: check required args are specified?
		return ft.Ret(false), nil
	case *Module:
		for _, arg := range c.Args {
			k, v := arg.Key, arg.Value

			it, err := v.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("FunCall.Infer: %w", err)
			}

			scheme, has := ft.SchemeOf(k)
			if !has {
				return nil, fmt.Errorf("FunCall.Infer: %q not found in %s", k, ft)
			}

			dt, isMono := scheme.Type()
			if !isMono {
				return nil, fmt.Errorf("FunCall.Infer: %q is not monomorphic", k)
			}

			if _, err := hm.Unify(dt, it); err != nil {
				return nil, fmt.Errorf("FunCall.Infer: %q cannot unify (%s ~ %s): %w", k, dt, it, err)
			}
		}
		return NonNullType{ft}, nil
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

	// closure
	env = env.Clone()

	args := []Keyed[*hm.Scheme]{}
	for _, arg := range f.Args {
		var definedArgType hm.Type

		if arg.Type_ != nil {
			// TODO should this take fresh? seems like maybe not?
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

		scheme := hm.NewScheme(nil, definedArgType)
		env.Add(arg.Named, scheme)
		args = append(args, Keyed[*hm.Scheme]{arg.Named, scheme})
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

type List struct {
	Elements []Node
}

var _ Node = List{}

func (l List) Infer(env hm.Env, f hm.Fresher) (hm.Type, error) {
	if len(l.Elements) == 0 {
		// TODO: is this right?
		return NonNullType{ListType{f.Fresh()}}, nil
	}

	var t hm.Type
	for i, el := range l.Elements {
		et, err := el.Infer(env, f)
		if err != nil {
			return nil, err
		}
		if t == nil {
			t = et
		} else if _, err := hm.Unify(t, et); err != nil {
			// TODO: is this right?
			return nil, fmt.Errorf("unify index %d: %w", i, err)
		}
	}
	return NonNullType{ListType{t}}, nil
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
	IntType     = NewModule("Int")
)

type String struct {
	Value string
}

var _ Node = String{}

func (s String) Body() hm.Expression { return s }

func (s String) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return NonNullTypeNode{NamedTypeNode{"String"}}.Infer(env, fresh)
}

type Quoted struct {
	Quoter string
	Raw    string
}

type Boolean bool

var _ Node = Boolean(false)

func (b Boolean) Body() hm.Expression { return b }

func (b Boolean) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

type Int int

var _ Node = Int(0)

func (i Int) Body() hm.Expression { return i }

func (i Int) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return NonNullTypeNode{NamedTypeNode{"Int"}}.Infer(env, fresh)
}
