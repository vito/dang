//go:build !cgo && !js && !dang_nowasm

package dang

import (
	"context"
	"time"

	"github.com/vito/dang/v2/pkg/dang/tswasm"
)

// syntaxRecoveryTimeout bounds one recovery parse of source: a better message
// is never worth hanging the caller, and past it the parse error names the
// token the parser failed at instead. It's there to stop a runaway parse, not
// to cut short a slow one, so it grows with the source: the wasm parser takes
// about 4µs a byte (measured on a 96 KB file), and this allows five times
// that, with a second's floor.
func syntaxRecoveryTimeout(source []byte) time.Duration {
	return max(time.Second, time.Duration(len(source))*20*time.Microsecond)
}

// parseSyntaxTree parses source with the C tree-sitter runtime compiled to
// WebAssembly (see pkg/dang/tswasm). Error-free subtrees come back without
// their children, which error reporting never reads.
func parseSyntaxTree(source []byte) (syntaxNode, func()) {
	ctx, cancel := context.WithTimeout(context.Background(), syntaxRecoveryTimeout(source))
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
