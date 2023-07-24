package main

import (
	"fmt"
	"log"

	sitter "github.com/smacker/go-tree-sitter"
	dash "github.com/vito/dash/grammar/bindings/go"
)

func FromTS(n *sitter.Node) (Node, error) {
	cursor := sitter.NewTreeCursor(n)
	node := Nodes[n.Symbol()]()
	return node, node.UnmarshalTS(cursor)
}

//go:generate go run ./pkg/ast/gen ./grammar/src/grammar.json ./ast.gen.go

func (n *source) UnmarshalTS(cursor *sitter.TreeCursor) error {
	if !cursor.GoToFirstChild() {
		panic("wat")
	}

	node := cursor.CurrentNode()

	// TODO: this would be a good sanity check
	// if node.Symbol() != dash.SymbolSource {
	// 	return fmt.Errorf("expected source node, got %v", node.Symbol())
	// }
	n.Body = make([]*form, node.ChildCount())

	for {
		log.Printf("!!! CHILD %q: %s", cursor.CurrentFieldName(), cursor.CurrentNode())

		var f *form
		if err := f.UnmarshalTS(cursor); err != nil {
			return err
		}

		if !cursor.GoToNextSibling() {
			break
		}
	}

	return nil
}

func (n *form) UnmarshalTS(cursor *sitter.TreeCursor) error {
	// TODO: this would be a good sanity check
	// if node.Symbol() != dash.SymbolSource {
	// 	return fmt.Errorf("expected source node, got %v", node.Symbol())
	// }

	n = &form{}
	switch cursor.CurrentFieldName() {
	case "List":
		n.List = &List{}
		cursor.GoToFirstChild()
		if err := n.List.UnmarshalTS(cursor); err != nil {
			return err
		}
		// XXX HERE
	}
	log.Println("!!! FORM:", cursor.CurrentFieldName())

	for {
		log.Printf("!!! CHILD %q: %s", cursor.CurrentFieldName(), cursor.CurrentNode())

		if !cursor.GoToNextSibling() {
			break
		}
	}

	return nil
}

func (n *List) UnmarshalTS(cursor *sitter.TreeCursor) error {
	// TODO: this would be a good sanity check
	// if node.Symbol() != dash.SymbolSource {
	// 	return fmt.Errorf("expected source node, got %v", node.Symbol())
	// }

	n = &List{}

	log.Println("!!! LIST:", cursor.CurrentFieldName())

	for {
		log.Printf("!!! LIST CHILD %q: %s", cursor.CurrentFieldName(), cursor.CurrentNode())

		if !cursor.GoToNextSibling() {
			break
		}
	}

	return nil
}

func debugNode(n *sitter.Node) {
	fmt.Printf("TS NODE: %s %#v\n", dash.Language().SymbolName(n.Symbol()), n)
	if dashNode, defined := Nodes[n.Symbol()]; defined {
		fmt.Printf("DASH NODE: %#v\n", dashNode())
	}
	fmt.Println("CHILD COUNT:", n.ChildCount())
	fmt.Println("NAMED CHILD COUNT:", n.NamedChildCount())
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		fmt.Println("CHILD", i, n.FieldNameForChild(i), child)
	}
}
