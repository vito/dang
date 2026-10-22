// Package danglang provides Go bindings for the Dang tree-sitter grammar.
package danglang

/*
#cgo CFLAGS: -I${SRCDIR}/../../../treesitter/src
// Go's build cache doesn't track these includes (they live outside the
// package), so a regenerated grammar can be masked by a stale cached build:
// run `go clean -cache` after regenerating, or touch this preamble.
#include "parser.c"
#include "scanner.c"
*/
import "C"
import "unsafe"

import tree_sitter "github.com/tree-sitter/go-tree-sitter"

func Language() *tree_sitter.Language {
	return tree_sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_dang()))
}
