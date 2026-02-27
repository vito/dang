package lsp

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/tree-sitter/go-tree-sitter"
	"github.com/vito/dang/pkg/lsp/danglang"
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

// tsReceiverResult holds the result of a tree-sitter receiver lookup.
type tsReceiverResult struct {
	// ReceiverText is the source text of the receiver expression (e.g. "ctr"
	// or "ctr.from(\"alpine\")").
	ReceiverText string

	// Partial is the partial member name being typed (e.g. "fr" in "ctr.fr").
	// Empty when the cursor is right after a dot.
	Partial string
}

// tsParseAndFindReceiver parses the file text with tree-sitter and looks for
// a dot-completion receiver at the given cursor position. Returns nil if the
// cursor is not in a dot-completion context.
//
// This handles cases the PEG-based AST cannot:
//   - Multi-line method chains (indented .method() continuation)
//   - Local variable receivers not in the top-level TypeEnv
//   - Cursor positions past the end of Select nodes
func tsParseAndFindReceiver(text string, line, col int) *tsReceiverResult {
	source := []byte(text)

	// tree-sitter parsers are not thread-safe; serialize access.
	tsMu.Lock()
	tree := tsParser.Parse(source, nil)
	tsMu.Unlock()

	if tree == nil {
		return nil
	}
	defer tree.Close()

	root := tree.RootNode()

	// Strategy 1: Find a select_or_call node at the cursor position.
	// This handles "ctr.fr" where the partial method name is the field_id.
	if result := findSelectAtCursor(root, source, uint(line), uint(col)); result != nil {
		return result
	}

	// Strategy 2: Find an ERROR node containing a dot_token at/near the
	// cursor. This handles "ctr." where the user just typed a dot.
	if result := findDotErrorAtCursor(root, source, uint(line), uint(col)); result != nil {
		return result
	}

	return nil
}

// findSelectAtCursor looks for a select_or_call node whose field_id contains
// the cursor. Returns the receiver text (the "left" field) and partial.
func findSelectAtCursor(node *tree_sitter.Node, source []byte, line, col uint) *tsReceiverResult {
	// Walk down to find the most specific select_or_call containing the cursor.
	var bestSelect *tree_sitter.Node

	walkTS(node, func(n *tree_sitter.Node) bool {
		start := n.StartPosition()
		end := n.EndPosition()

		// Check if cursor is within this node's range (inclusive end).
		if !tsContains(start, end, line, col) {
			return false
		}

		if n.Kind() == "select_or_call" {
			bestSelect = n
		}
		return true
	})

	if bestSelect == nil {
		return nil
	}

	// The select_or_call has: left, dot_token, field_id (or selection)
	leftNode := bestSelect.ChildByFieldName("left")
	if leftNode == nil {
		return nil
	}

	// Get the field name node (partial being typed)
	nameNode := bestSelect.ChildByFieldName("name")
	var partial string
	if nameNode != nil {
		partial = nameNode.Utf8Text(source)
	}

	receiverText := leftNode.Utf8Text(source)
	// Normalize whitespace in receiver text (collapse multi-line to single line)
	receiverText = collapseWhitespace(receiverText)

	slog.Debug("ts: found select_or_call",
		"receiver", receiverText,
		"partial", partial,
		"select_range", []any{bestSelect.StartPosition(), bestSelect.EndPosition()},
	)

	return &tsReceiverResult{
		ReceiverText: receiverText,
		Partial:      partial,
	}
}

// findDotErrorAtCursor looks for an ERROR node at the cursor position that
// contains a dot_token. The receiver is the expression node just before the
// ERROR in the parent's children.
func findDotErrorAtCursor(root *tree_sitter.Node, source []byte, line, col uint) *tsReceiverResult {
	var errorNode *tree_sitter.Node

	walkTS(root, func(n *tree_sitter.Node) bool {
		start := n.StartPosition()
		end := n.EndPosition()

		if !tsContains(start, end, line, col) {
			return false
		}

		if n.Kind() == "ERROR" {
			// Check if this ERROR contains a dot_token
			for i := uint(0); i < n.ChildCount(); i++ {
				child := n.Child(i)
				if child != nil && child.Kind() == "dot_token" {
					errorNode = n
					break
				}
			}
		}
		return true
	})

	if errorNode == nil {
		return nil
	}

	// Find the sibling expression just before this ERROR node.
	parent := errorNode.Parent()
	if parent == nil {
		return nil
	}

	var prevExpr *tree_sitter.Node
	for i := uint(0); i < parent.ChildCount(); i++ {
		child := parent.Child(i)
		if child != nil && tsNodeEquals(child, errorNode) {
			break
		}
		// Track the last non-sep, non-error child as the potential receiver
		if child != nil && child.Kind() != "sep" && child.Kind() != "ERROR" && !child.IsExtra() {
			prevExpr = child
		}
	}

	if prevExpr == nil {
		return nil
	}

	receiverText := collapseWhitespace(prevExpr.Utf8Text(source))

	slog.Debug("ts: found ERROR with dot",
		"receiver", receiverText,
		"error_range", []any{errorNode.StartPosition(), errorNode.EndPosition()},
	)

	return &tsReceiverResult{
		ReceiverText: receiverText,
		Partial:      "",
	}
}

// walkTS does a depth-first walk of the tree-sitter node, calling fn for each
// node. If fn returns false, children are skipped.
func walkTS(node *tree_sitter.Node, fn func(*tree_sitter.Node) bool) {
	if node == nil {
		return
	}
	if !fn(node) {
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil {
			walkTS(child, fn)
		}
	}
}

// tsContains checks if a cursor position (line, col) is within [start, end).
// Uses tree-sitter's 0-based coordinates (same as LSP).
func tsContains(start, end tree_sitter.Point, line, col uint) bool {
	if line < start.Row || line > end.Row {
		return false
	}
	if line == start.Row && col < start.Column {
		return false
	}
	if line == end.Row && col > end.Column {
		return false
	}
	return true
}

// collapseWhitespace normalizes whitespace in receiver text: trims each line,
// joins with no separator, producing a compact single-line expression.
func collapseWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var parts []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, "")
}

// tsNodeEquals checks if two tree-sitter nodes are the same node.
func tsNodeEquals(a, b *tree_sitter.Node) bool {
	return a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte()
}
