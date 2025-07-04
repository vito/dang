package main

import (
	"encoding/json"
	"os"

	"github.com/vito/bind/pkg/bind"
)

//go:generate go run .

//go:generate tree-sitter generate src/grammar.json

func main() {
	grammarFile, err := os.Create("src/grammar.json")
	if err != nil {
		panic(err)
	}
	defer grammarFile.Close()

	enc := json.NewEncoder(grammarFile)
	enc.SetIndent("", "  ")
	enc.Encode(bind.TreesitterGrammar())
}
