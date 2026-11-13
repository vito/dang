//go:build !js && !dang_nowasm

package dang

import (
	"github.com/vito/dang/v2/pkg/dang/tswasm"
)

// parseSyntaxTree parses source with the C tree-sitter runtime compiled to
// WebAssembly (see pkg/dang/tswasm). Error-free subtrees come back without
// their children, which error reporting never reads. A better message is
// never worth hanging the caller: past treeSitterTimeout the parse error
// names the token the parser failed at instead.
func parseSyntaxTree(source []byte) (syntaxNode, func()) {
	ctx, cancel, err := treeSitterContext(source)
	if err != nil {
		return nil, nil
	}
	defer cancel()
	root, err := tswasm.Parse(ctx, source, tswasm.PruneErrorFree)
	if err != nil {
		return nil, nil
	}
	return wasmNode{root, source}, func() {}
}

// wasmNode is a syntaxNode of the WebAssembly runtime.
type wasmNode struct {
	n      *tswasm.Node
	source []byte
}

func (n wasmNode) Kind() string    { return n.n.Kind }
func (n wasmNode) IsMissing() bool { return n.n.Missing }
func (n wasmNode) IsError() bool   { return n.n.Error }
func (n wasmNode) ChildCount() int { return len(n.n.Children()) }
func (n wasmNode) Text() string    { return string(n.source[n.n.StartByte:n.n.EndByte]) }
func (n wasmNode) ByteLen() int    { return int(n.n.EndByte - n.n.StartByte) }

func (n wasmNode) Child(i int) syntaxNode {
	if i < 0 || i >= len(n.n.Children()) {
		return nil
	}
	return wasmNode{n.n.Children()[i], n.source}
}

func (n wasmNode) Start() (row, col uint) {
	return uint(n.n.StartRow), uint(n.n.StartColumn)
}

func (n wasmNode) End() (row, col uint) {
	return uint(n.n.EndRow), uint(n.n.EndColumn)
}
