package tree_sitter_sprout_test

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_sprout "github.com/vito/sprout/treesitter/bindings/go"
)

func TestCanLoadGrammar(t *testing.T) {
	language := tree_sitter.NewLanguage(tree_sitter_sprout.Language())
	if language == nil {
		t.Errorf("Error loading Sprout grammar")
	}
}
