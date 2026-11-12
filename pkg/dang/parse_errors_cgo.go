//go:build cgo

package dang

import (
	"sync"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/vito/dang/v2/pkg/dang/danglang"
)

var (
	tsParser *tree_sitter.Parser
	tsMu     sync.Mutex
)

func init() {
	tsParser = tree_sitter.NewParser()
	if err := tsParser.SetLanguage(danglang.Language()); err != nil {
		panic("failed to set tree-sitter language: " + err.Error())
	}
}

// parseSyntaxTree parses source with the C tree-sitter runtime. release
// frees the tree.
func parseSyntaxTree(source []byte) (syntaxNode, func()) {
	tsMu.Lock()
	tree := tsParser.Parse(source, nil)
	tsMu.Unlock()
	if tree == nil {
		return nil, nil
	}
	return cNode{tree.RootNode(), source}, tree.Close
}

// cNode is a syntaxNode of the C runtime.
type cNode struct {
	n      *tree_sitter.Node
	source []byte
}

func (n cNode) Kind() string    { return n.n.Kind() }
func (n cNode) IsMissing() bool { return n.n.IsMissing() }
func (n cNode) IsError() bool   { return n.n.IsError() }
func (n cNode) ChildCount() int { return int(n.n.ChildCount()) }
func (n cNode) Text() string    { return n.n.Utf8Text(n.source) }
func (n cNode) ByteLen() int    { return int(n.n.EndByte() - n.n.StartByte()) }

func (n cNode) Child(i int) syntaxNode {
	child := n.n.Child(uint(i))
	if child == nil {
		return nil
	}
	return cNode{child, n.source}
}

func (n cNode) Start() (row, col uint) {
	p := n.n.StartPosition()
	return p.Row, p.Column
}

func (n cNode) End() (row, col uint) {
	p := n.n.EndPosition()
	return p.Row, p.Column
}
