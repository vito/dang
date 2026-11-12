// Package danglang provides cgo Go bindings for the Dang tree-sitter grammar,
// for the docs site's highlighter. Dang itself runs the grammar compiled to
// WebAssembly (pkg/dang/tswasm), without cgo.
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
