package main

import (
	"encoding/json"
	"os"

	"github.com/vito/dash/pkg/ast"
)

//go:generate go run .

//go:generate tree-sitter generate

func main() {
	grammarFile, err := os.Create("src/grammar.json")
	if err != nil {
		panic(err)
	}
	defer grammarFile.Close()

	json.NewEncoder(grammarFile).Encode(ast.TreesitterGrammar())
}
