package tree_sitter_dash_test

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_dash "github.com/tree-sitter/tree-sitter-dash/bindings/go"
)

func TestCanLoadGrammar(t *testing.T) {
	language := tree_sitter.NewLanguage(tree_sitter_dash.Language())
	if language == nil {
		t.Errorf("Error loading Dash grammar")
	}
}
