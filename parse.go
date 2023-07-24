package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"

	sitter "github.com/smacker/go-tree-sitter"
	dash "github.com/vito/dash/grammar/bindings/go"
)

//go:embed test.dash
var sourceCode []byte

func main() {
	parser := sitter.NewParser()
	parser.SetLanguage(dash.Language())

	tree, err := parser.ParseCtx(context.TODO(), nil, sourceCode)
	if err != nil {
		panic(err)
	}

	n := tree.RootNode()

	debugNode(n)

	node, err := FromTS(n)
	if err != nil {
		panic(err)
	}

	fmt.Printf("%#v\n", node)
	return

	if err := eval(n); err != nil {
		panic(err)
	}

	for node, count := range evaled {
		if count > 1 {
			fmt.Println(node, count)
		}
	}
}

var evaled = map[*sitter.Node]int{}

func eval(node *sitter.Node) error {
	iter := sitter.NewNamedIterator(node, sitter.BFSMode)
	err := iter.ForEach(func(n *sitter.Node) error {
		fmt.Println(n.Symbol(), n.Type(), n.Content(sourceCode))
		evaled[n]++
		return nil
	})
	if err == io.EOF {
		err = nil
	}
	return err
}
