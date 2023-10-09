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
	Named string
	Args  []SlotDecl
	Form  Node
	Ret   Type
}

var _ hm.Expression = FunDecl{}

func (f FunDecl) Body() hm.Expression { return f.Form }

var _ hm.Inferer = FunDecl{}

func (f FunDecl) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	panic("FUNCDECL INFER")
	args := []Keyed[*hm.Scheme]{}
	for _, arg := range f.Args {
		t := arg.Type_
		if t == nil {
			if arg.Value != nil {
				var err error
				t, err = arg.Value.Infer(env, fresh)
				if err != nil {
					return nil, fmt.Errorf("FuncDecl.Infer arg: %w", err)
				}
			} else {
				return nil, fmt.Errorf("FuncDecl.Infer arg: no type or value")
			}
		}
		args = append(args, Keyed[*hm.Scheme]{arg.Named, hm.NewScheme(nil, t)})
	}

	if f.Ret == nil {
		var err error
		// TODO just a guess, not sure if nil env makes more sense, but i think we
		// want it to be able to refer to outer slots
		f.Ret, err = f.Form.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("FuncDecl.Infer: %w", err)
		}
	}

	return hm.NewFnType(NewRecordType("", args...), f.Ret), nil
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
	Type_      Type
	Value      Node
	Visibility Visibility
}

var _ Node = SlotDecl{}

func (s SlotDecl) Body() hm.Expression {
	// TODO(vito): return Value? unclear how Body is used
	return s
}

func (s SlotDecl) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	panic("INFERRING SLOT")
	env.Add(s.Named, hm.NewScheme(nil, s.Type_))

	if s.Type_ != nil {
		return s.Type_, nil
	}
	if s.Value != nil {
		return s.Value.Infer(env, fresh)
	}
	return nil, fmt.Errorf("SlotDecl.Infer: no type or value")
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
	rec, ok := lt.(*RecordType)
	if !ok {
		return nil, fmt.Errorf("Select.Infer: expected record type, got %s", lt)
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

// Literals

const yahh = true

var (
	NullType    = NewRecordType("Null")
	BooleanType = NewRecordType("Boolean")
	StringType  = NewRecordType("String")
	IntegerType = NewRecordType("Integer")
)

type String struct {
	Value string
}

var _ Node = String{}

func (s String) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return NonNullType{StringType}, nil
}

var _ hm.Literal = String{}

func (s String) IsLit() bool         { return yahh }
func (s String) Name() string        { return "String" }
func (s String) Type() hm.Type       { return StringType }
func (s String) Body() hm.Expression { return s }

type Quoted struct {
	Quoter string
	Raw    string
}

type Boolean bool

var _ Node = Boolean(false)

func (b Boolean) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return NonNullType{BooleanType}, nil
}

var _ hm.Literal = Boolean(false)

func (s Boolean) IsLit() bool         { return yahh }
func (s Boolean) Name() string        { return "Boolean" }
func (s Boolean) Type() hm.Type       { return BooleanType }
func (s Boolean) Body() hm.Expression { return s }

type Null struct{}

var _ Node = Null{}

func (b Null) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// making this NonNull would certainly be cursed, so let's see how it goes...
	return NullType, nil
}

var _ hm.Literal = Null{}

func (s Null) IsLit() bool         { return yahh }
func (s Null) Name() string        { return "Null" }
func (s Null) Type() hm.Type       { return NullType }
func (s Null) Body() hm.Expression { return s }

type Integer int

var _ Node = Integer(0)

func (b Integer) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return NonNullType{IntegerType}, nil
}

var _ hm.Literal = Integer(0)

func (s Integer) IsLit() bool         { return yahh }
func (s Integer) Name() string        { return "Integer" }
func (s Integer) Type() hm.Type       { return IntegerType }
func (s Integer) Body() hm.Expression { return s }

type Self struct{}

var _ Node = Self{}

func (Self) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return env.Clone().(*RecordType), nil
}

func (s Self) Body() hm.Expression { return s }
