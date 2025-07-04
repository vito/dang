package grammar

//#include "tree_sitter/parser.h"
//TSLanguage *tree_sitter_bind();
import "C"
import (
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"
)

func Language() *sitter.Language {
	ptr := unsafe.Pointer(C.tree_sitter_bind())
	return sitter.NewLanguage(ptr)
}
