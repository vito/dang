package hm

import "context"

// Expression is basically an AST node
type Expression interface {
	Body() Expression
}

// Namer is anything that knows its own name
type Namer interface {
	Name() string
}

// Typer is an Expression node that knows its own Type
type Typer interface {
	Type() Type
}

// Inferer is an Expression that can infer its own Type given an Env
type Inferer interface {
	Infer(context.Context, Env, Fresher) (Type, error)
}

// Var is an expression representing a variable
type Var interface {
	Expression
	Namer
	Typer
}

// Literal is an Expression/AST Node representing a literal
type Literal interface {
	Var
	IsLit() bool
}

// Apply is an Expression/AST node that represents a function application
type Apply interface {
	Expression
	Fn() Expression
}

// Lambda is an Expression/AST node that represents a lambda abstraction
type Lambda interface {
	Expression
	Namer // name of the parameter
}

// Let is an Expression/AST node that represents let polymorphism
type Let interface {
	Expression
	Namer            // name of the bound variable
	Def() Expression // definition
}

// LetRec is an Expression/AST node that represents recursive let
type LetRec interface {
	Let
	IsRecursive() bool
}

// Cloner is any type that can clone itself
type Cloner interface {
	Clone() any
}
