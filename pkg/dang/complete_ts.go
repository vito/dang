package dang

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/tree-sitter/go-tree-sitter"
	"github.com/vito/dang/pkg/dang/danglang"
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

// CompletionContext describes the cursor context for completion.
type CompletionContext struct {
	Kind         ContextKind
	ReceiverText string   // for DotMember: normalized receiver source
	FuncText     string   // for Argument: normalized function expression
	Partial      string   // prefix the user is typing
	ProvidedArgs []string // for Argument: already-present named args
}

// ContextKind classifies what kind of completion the cursor needs.
type ContextKind int

const (
	ContextNone      ContextKind = iota
	ContextDotMember             // ctr.fr|  or  ctr.|
	ContextArgument              // container.from(addr|
	ContextBareIdent             // ct|  (no dot, no parens)
)

// CompletionResult holds the completions and where to start replacing.
type CompletionResult struct {
	Items       []Completion
	ReplaceFrom int // byte offset in the input where the completed token starts
}

// Complete returns completions for the given source text at the given cursor
// position, using env for type resolution.
//
// This is the single entry point for both LSP and REPL completion. It uses
// tree-sitter to parse the text and classify the cursor context, then resolves
// types from the provided environment.
func Complete(ctx context.Context, env Env, text string, line, col int) CompletionResult {
	source := []byte(text)

	// tree-sitter parsers are not thread-safe; serialize access.
	tsMu.Lock()
	tree := tsParser.Parse(source, nil)
	tsMu.Unlock()

	if tree == nil {
		return CompletionResult{}
	}
	defer tree.Close()

	root := tree.RootNode()

	cc := classifyCursorContext(root, source, uint(line), uint(col))
	if cc == nil {
		return CompletionResult{}
	}

	replaceFrom := computeReplaceFrom(text, line, col, cc)

	var items []Completion
	switch cc.Kind {
	case ContextDotMember:
		items = completeMember(ctx, env, cc.ReceiverText, cc.Partial)
	case ContextArgument:
		items = completeArgs(ctx, env, cc.FuncText, cc.Partial, cc.ProvidedArgs)
	case ContextBareIdent:
		items = completeLexical(env, cc.Partial)
	}

	return CompletionResult{
		Items:       items,
		ReplaceFrom: replaceFrom,
	}
}

// computeReplaceFrom calculates the byte offset in text where the partial
// token being completed starts.
func computeReplaceFrom(text string, line, col int, cc *CompletionContext) int {
	offset := lineColToOffset([]byte(text), uint(line), uint(col))
	return offset - len(cc.Partial)
}

// classifyCursorContext analyzes the tree-sitter CST to determine what kind
// of completion the cursor needs.
func classifyCursorContext(root *tree_sitter.Node, source []byte, line, col uint) *CompletionContext {
	// Strategy 1: Argument context via string-based detection.
	// Tree-sitter doesn't produce arg_values for incomplete calls (the
	// content ends up in ERROR nodes), so we use the proven string-based
	// splitArgExpr approach. This will be replaced by tree-sitter-based
	// arg detection in a future phase.
	textUpToCursor := string(source[:lineColToOffset(source, line, col)])
	if funcExpr, partial, alreadyProvided, ok := splitArgExpr(textUpToCursor); ok {
		return &CompletionContext{
			Kind:         ContextArgument,
			FuncText:     funcExpr,
			Partial:      partial,
			ProvidedArgs: alreadyProvided,
		}
	}

	// Strategy 2: Dot member — select_or_call with cursor on name field.
	if result := findSelectAtCursor(root, source, line, col); result != nil {
		return &CompletionContext{
			Kind:         ContextDotMember,
			ReceiverText: result.receiverText,
			Partial:      result.partial,
		}
	}

	// Strategy 3: Dot member — ERROR node with dot_token.
	if result := findDotErrorAtCursor(root, source, line, col); result != nil {
		return &CompletionContext{
			Kind:         ContextDotMember,
			ReceiverText: result.receiverText,
			Partial:      result.partial,
		}
	}

	// Strategy 4: Bare identifier.
	if partial := findBareIdentAtCursor(root, source, line, col); partial != "" {
		return &CompletionContext{
			Kind:    ContextBareIdent,
			Partial: partial,
		}
	}

	return nil
}

// tsReceiverResult holds the result of a tree-sitter receiver lookup.
type tsReceiverResult struct {
	receiverText string
	partial      string
}

// findSelectAtCursor looks for a select_or_call node whose field_id contains
// the cursor. Returns the receiver text (the "left" field) and partial.
func findSelectAtCursor(node *tree_sitter.Node, source []byte, line, col uint) *tsReceiverResult {
	var bestSelect *tree_sitter.Node

	walkTS(node, func(n *tree_sitter.Node) bool {
		start := n.StartPosition()
		end := n.EndPosition()

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

	leftNode := bestSelect.ChildByFieldName("left")
	if leftNode == nil {
		return nil
	}

	nameNode := bestSelect.ChildByFieldName("name")
	var partial string
	if nameNode != nil {
		partial = nameNode.Utf8Text(source)
	}

	receiverText := leftNode.Utf8Text(source)
	receiverText = collapseWhitespace(receiverText)

	slog.Debug("ts: found select_or_call",
		"receiver", receiverText,
		"partial", partial,
		"select_range", []any{bestSelect.StartPosition(), bestSelect.EndPosition()},
	)

	return &tsReceiverResult{
		receiverText: receiverText,
		partial:      partial,
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
		receiverText: receiverText,
		partial:      "",
	}
}

// extractPartialAtCursor extracts the identifier fragment at the cursor by
// walking backwards from the cursor position in the source bytes.
func extractPartialAtCursor(source []byte, line, col uint) string {
	offset := lineColToOffset(source, line, col)
	if offset <= 0 {
		return ""
	}
	i := offset - 1
	for i >= 0 && isIdentByte(source[i]) {
		i--
	}
	return string(source[i+1 : offset])
}

// lineColToOffset converts a 0-based line/col to a byte offset in source.
func lineColToOffset(source []byte, line, col uint) int {
	offset := 0
	currentLine := uint(0)
	for offset < len(source) && currentLine < line {
		if source[offset] == '\n' {
			currentLine++
		}
		offset++
	}
	offset += int(col)
	if offset > len(source) {
		offset = len(source)
	}
	return offset
}

// findBareIdentAtCursor checks if the cursor is on a bare identifier (not
// inside a dot expression or argument list).
func findBareIdentAtCursor(root *tree_sitter.Node, source []byte, line, col uint) string {
	offset := lineColToOffset(source, line, col)
	if offset <= 0 {
		return ""
	}

	// Walk backwards from cursor to extract the identifier.
	i := offset - 1
	for i >= 0 && isIdentByte(source[i]) {
		i--
	}

	if i+1 >= offset {
		return "" // no identifier at cursor
	}

	// If the character before the identifier is a dot, this is a dot-member
	// context, not a bare ident.
	if i >= 0 && source[i] == '.' {
		return ""
	}

	return string(source[i+1 : offset])
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
