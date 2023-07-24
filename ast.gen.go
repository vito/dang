package main

import (
	"fmt"
	gotreesitter "github.com/smacker/go-tree-sitter"
)

type unimplementedNode struct{}

func (unimplementedNode) UnmarshalTS(node *gotreesitter.TreeCursor) error {
	return fmt.Errorf("unimplemented node type")
}

var Nodes = map[gotreesitter.Symbol]func() Node{
	1:  func() Node { return &Symbol{} },
	2:  func() Node { return &Path{} },
	3:  func() Node { return &keyword{} },
	9:  func() Node { return &fnKeyword{} },
	14: func() Node { return &dotOperator{} },
	15: func() Node { return &assignOperator{} },
	17: func() Node { return &semicolon{} },
	18: func() Node { return &comma{} },
	19: func() Node { return &Number{} },
	21: func() Node { return &stringFragment{} },
	23: func() Node { return &immediateEscapeSequence{} },
	25: func() Node { return &quotedFragment{} },
	26: func() Node { return &quotedEscape{} },
	27: func() Node { return &Null{} },
	30: func() Node { return &comment{} },
	31: func() Node { return &source{} },
	33: func() Node { return &keyval{} },
	34: func() Node { return &Call{} },
	35: func() Node { return &kwargs{} },
	36: func() Node { return &Fn{} },
	37: func() Node { return &kwtypes{} },
	38: func() Node { return &keytype{} },
	39: func() Node { return &type_{} },
	40: func() Node { return &fnType{} },
	41: func() Node { return &listType{} },
	42: func() Node { return &Infix{} },
	43: func() Node { return &dollarOperator{} },
	44: func() Node { return &Shell{} },
	45: func() Node { return &argument{} },
	46: func() Node { return &textarg{} },
	47: func() Node { return &shellvar{} },
	48: func() Node { return &List{} },
	49: func() Node { return &Record{} },
	51: func() Node { return &String{} },
	52: func() Node { return &escapeSequence{} },
	53: func() Node { return &Quoted{} },
	54: func() Node { return &Boolean{} }}

type blank struct {
	unimplementedNode
}

type Node interface {
	UnmarshalTS(*gotreesitter.TreeCursor) error
}
type Boolean struct {
	unimplementedNode
}

type Call struct {
	unimplementedNode
	Name *Symbol
	Args *kwargs
}

type Fn struct {
	unimplementedNode
	Name       struct{}
	ArgTypes   *kwtypes
	ReturnType struct {
		Type *type_
	}
	Body []*form
}

type Infix struct {
	unimplementedNode
	Dollar struct {
		Left     *form
		Operator *dollarOperator
		Right    *Shell
	}
	Dot struct {
		Left     *form
		Operator *dotOperator
		Right    *form
	}
	Equal struct {
		Left     *form
		Operator *assignOperator
		Right    *form
	}
}

type List struct {
	unimplementedNode
	Values []struct {
		Value *form
	}
}

type Null struct {
	unimplementedNode
}

type Number struct {
	unimplementedNode
	Token string
}

type Path struct {
	unimplementedNode
	Token string
}

type Quoted struct {
	unimplementedNode
	Token string
}

type Record struct {
	unimplementedNode
	KeyValues []struct {
		KeyVal *keyval
	}
}

type Shell struct {
	unimplementedNode
	Command   *argument
	Arguments []*argument
}

type String struct {
	unimplementedNode
	Content []struct{}
}

type Symbol struct {
	unimplementedNode
	Token string
}

type argument struct {
	unimplementedNode
}

type assignOperator struct {
	unimplementedNode
}

type comma struct {
	unimplementedNode
	Token string
}

type comment struct {
	unimplementedNode
	Token string
}

type dollarOperator struct {
	unimplementedNode
}

type dotOperator struct {
	unimplementedNode
}

type escapeSequence struct {
	unimplementedNode
	Token string
}

type fnKeyword struct {
	unimplementedNode
	Token string
}

type fnType struct {
	unimplementedNode
}

type form struct {
	unimplementedNode
	Call    *Call
	Infix   *Infix
	Fn      *Fn
	Literal *literal
	Symbol  *Symbol
	List    *List
	Record  *Record
	Path    *Path
}

type immediateEscapeSequence struct {
	unimplementedNode
	Ignore             string
	Octal              string
	Hex                string
	UnicodeUnbracketed string
	UnicodeBracketed   string
}

type keytype struct {
	unimplementedNode
}

type keyval struct {
	unimplementedNode
	Keyword *keyword
	Value   *form
}

type keyword struct {
	unimplementedNode
	Token string
}

type kwargs struct {
	unimplementedNode
	AnonymousArgs []struct {
		Form *form
	}
	NamedArgs []struct {
		NamedArg *keyval
	}
}

type kwtypes struct {
	unimplementedNode
	NamedArgs []struct {
		NamedArg *keytype
	}
}

type listType struct {
	unimplementedNode
	Inner *type_
}

type literal struct {
	unimplementedNode
}

type quotedEscape struct {
	unimplementedNode
}

type quotedFragment struct {
	unimplementedNode
	Token string
}

type semicolon struct {
	unimplementedNode
}

type shellvar struct {
	unimplementedNode
}

type source struct {
	unimplementedNode
	Body []*form
}

type stringFragment struct {
	unimplementedNode
	Token string
}

type textarg struct {
	unimplementedNode
	Token string
}

type type_ struct {
	unimplementedNode
}
