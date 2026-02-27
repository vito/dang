// Package danglang provides Go bindings for the Dang tree-sitter grammar.
package danglang

/*
#cgo CFLAGS: -I${SRCDIR}/../../../treesitter/src
#include "parser.c"
#include "scanner.c"
*/
import "C"
import "unsafe"

import tree_sitter "github.com/tree-sitter/go-tree-sitter"

func Language() *tree_sitter.Language {
	return tree_sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_dang()))
}
