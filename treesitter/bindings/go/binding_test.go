package tree_sitter_bind_test

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_bind "github.com/tree-sitter/tree-sitter-bind/bindings/go"
)

func TestCanLoadGrammar(t *testing.T) {
	language := tree_sitter.NewLanguage(tree_sitter_bind.Language())
	if language == nil {
		t.Errorf("Error loading Bind grammar")
	}
}
