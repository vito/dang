//go:build !cgo || dang_gotreesitter

package dang

import (
	"sync"
	"time"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/vito/dang/v2/pkg/dang/tsgo"
)

// Without cgo, parse errors are recovered by gotreesitter, a pure-Go
// tree-sitter runtime running the same grammar tables and a Go port of the
// external scanner (see pkg/dang/tsgo). Build with -tags dang_gotreesitter
// to use it with cgo too.

// SyntaxRecoveryRuntime names the tree-sitter runtime that recovers parse
// errors in this build: "c" or "gotreesitter".
const SyntaxRecoveryRuntime = "gotreesitter"

var (
	gtsParser *gts.Parser
	gtsMu     sync.Mutex
)

// gtsTimeout bounds a recovery parse: error recovery is where a GLR parser
// can blow up, and a better message is never worth hanging the caller.
const gtsTimeout = time.Second

// parseSyntaxTree parses source with gotreesitter. release frees the tree.
// The parser stays locked until then, in case trees share its scratch memory.
func parseSyntaxTree(source []byte) (syntaxNode, func()) {
	lang := tsgo.Language()
	gtsMu.Lock()
	if gtsParser == nil {
		gtsParser = gts.NewParser(lang)
		gtsParser.SetTimeoutMicros(uint64(gtsTimeout.Microseconds()))
	}
	tree, err := gtsParser.Parse(source)
	if err != nil || tree == nil {
		gtsMu.Unlock()
		return nil, nil
	}
	release := func() {
		tree.Release()
		gtsMu.Unlock()
	}
	if tree.ParseStoppedEarly() {
		release()
		return nil, nil
	}
	return gtsNode{tree.RootNode(), lang, source}, release
}

// gtsNode is a syntaxNode backed by gotreesitter.
type gtsNode struct {
	n      *gts.Node
	lang   *gts.Language
	source []byte
}

func (n gtsNode) Kind() string    { return n.n.Type(n.lang) }
func (n gtsNode) IsMissing() bool { return n.n.IsMissing() }
func (n gtsNode) IsError() bool   { return n.n.IsError() }
func (n gtsNode) ChildCount() int { return n.n.ChildCount() }
func (n gtsNode) Text() string    { return n.n.Text(n.source) }
func (n gtsNode) ByteLen() int    { return int(n.n.EndByte() - n.n.StartByte()) }

func (n gtsNode) Child(i int) syntaxNode {
	child := n.n.Child(i)
	if child == nil {
		return nil
	}
	return gtsNode{child, n.lang, n.source}
}

func (n gtsNode) Start() (row, col uint) {
	p := n.n.StartPoint()
	return uint(p.Row), uint(p.Column)
}

func (n gtsNode) End() (row, col uint) {
	p := n.n.EndPoint()
	return uint(p.Row), uint(p.Column)
}
