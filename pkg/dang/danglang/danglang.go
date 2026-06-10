// Package danglang provides Go bindings for the Dang tree-sitter grammar.
package danglang

/*
#cgo CFLAGS: -I${SRCDIR}/../../../treesitter/src

// Rename the grammar's exported C symbols with a _v1 suffix. C symbols live
// in a single global namespace, so without this a binary linking both this
// module and github.com/vito/dang/v2 (which defines the canonical
// tree_sitter_dang symbols) fails with duplicate-definition errors at link
// time. parser.c and scanner.c are textually included below, so these macros
// rewrite every definition and reference in one place.
#define tree_sitter_dang tree_sitter_dang_v1
#define tree_sitter_dang_external_scanner_create tree_sitter_dang_v1_external_scanner_create
#define tree_sitter_dang_external_scanner_destroy tree_sitter_dang_v1_external_scanner_destroy
#define tree_sitter_dang_external_scanner_serialize tree_sitter_dang_v1_external_scanner_serialize
#define tree_sitter_dang_external_scanner_deserialize tree_sitter_dang_v1_external_scanner_deserialize
#define tree_sitter_dang_external_scanner_scan tree_sitter_dang_v1_external_scanner_scan

#include "parser.c"
#include "scanner.c"
*/
import "C"
import "unsafe"

import tree_sitter "github.com/tree-sitter/go-tree-sitter"

func Language() *tree_sitter.Language {
	return tree_sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_dang_v1()))
}
