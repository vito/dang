package main

import (
	"fmt"
	"log"

	sitter "github.com/smacker/go-tree-sitter"
)

func FromTS(n *sitter.Node, input []byte) (Node, error) {
	node := Nodes[n.Symbol()]()
	return node, node.UnmarshalTS(n, input)
}

//go:generate go run ./pkg/ast/gen ./grammar/src/grammar.json ./ast.gen.go

func (n *source) UnmarshalTS(node *sitter.Node, input []byte) error {
	// TODO: this would be a good sanity check
	// if node.Symbol() != dash.SymbolSource {
	// 	return fmt.Errorf("expected source node, got %v", node.Symbol())
	// }
	n.Body = make([]*form, node.ChildCount())
	log.Println("!!! SOURCE", node.ChildCount())
	sitter.NewTreeCursor(node)
	for i := 0; i < int(node.ChildCount()); i++ {
		log.Println("!!! SOURCE CHILD", node.FieldNameForChild(i))
		debugNode(node.Child(i))
		n.Body[i] = &form{}
		if err := n.Body[i].UnmarshalTS(node.Child(i), input); err != nil {
			return fmt.Errorf("child %d: %w", i, err)
		}
	}
	return nil
}

func (n *form) UnmarshalTS(node *sitter.Node, input []byte) error {
	*n = form{}
	// TODO: this would be a good sanity check
	// if node.Symbol() != dash.SymbolSource {
	// 	return fmt.Errorf("expected source node, got %v", node.Symbol())
	// }
	log.Println("!!!!!!!!!!!!!!!!!!!! FORM", node.Type())
	debugNode(node)
	cons, defined := Nodes[node.Symbol()]
	if !defined {
		return fmt.Errorf("unknown symbol: %v", node.Symbol())
	}

	switch x := cons().(type) {
	case *Call:
		n.Call = x
		return n.Call.UnmarshalTS(node, input)
	case *Infix:
		n.Infix = x
		return n.Infix.UnmarshalTS(node, input)
	case *Fun:
		n.Fun = x
		return n.Fun.UnmarshalTS(node, input)
	case *literal:
		n.Literal = x
		return n.Literal.UnmarshalTS(node, input)
	case *Symbol:
		n.Symbol = x
		return n.Symbol.UnmarshalTS(node, input)
	case *List:
		n.List = x
		return n.List.UnmarshalTS(node, input)
	case *Record:
		n.Record = x
		return n.Record.UnmarshalTS(node, input)
	case *Path:
		n.Path = x
		return n.Path.UnmarshalTS(node, input)
	default:
		return fmt.Errorf("unknown form: %T (%+v)", x, x)
	}
}

func (list *List) UnmarshalTS(node *sitter.Node, input []byte) error {
	cursor := sitter.NewTreeCursor(node)
	defer cursor.Close()
	log.Println("!!!!!!!!!!!!!!!!!!!!!!! UNMARSHAL LIST", node)
	debugNode(cursor.CurrentNode())
	return nil
}

func debugNode(n *sitter.Node) {
	// fmt.Printf("TS NODE: %s %#v\n", dash.Language().SymbolName(n.Symbol()), n)
	if dashNode, defined := Nodes[n.Symbol()]; defined {
		fmt.Printf("DASH NODE: %T\n", dashNode())
	} else {
		fmt.Printf("UNKNOWN NODE: %s\n", n.Type())
	}
	fmt.Println("CHILD COUNT:", n.ChildCount())
	fmt.Println("NAMED CHILD COUNT:", n.NamedChildCount())
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		fmt.Println("CHILD", i, n.FieldNameForChild(i), child)
	}
}
