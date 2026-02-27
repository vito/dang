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
	// Strategy 1: Argument context via tree-sitter.
	if ac := findArgContextAtCursor(root, source, line, col); ac != nil {
		return ac
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

// findArgContextAtCursor detects when the cursor is inside a function call's
// argument list using tree-sitter CST analysis. It handles three patterns:
//
// Pattern A: Well-formed arg_values node exists (e.g. "foo(" or "foo(x, ").
// Pattern B: ERROR sibling of select_or_call (e.g. "container.from(addr").
// Pattern C: Root ERROR containing func + immediate_paren + args (e.g. "foo(na").
func findArgContextAtCursor(root *tree_sitter.Node, source []byte, line, col uint) *CompletionContext {
	// Pattern A: Walk up from deepest node looking for arg_values.
	if cc := findArgInArgValues(root, source, line, col); cc != nil {
		return cc
	}

	// Pattern B & C: Look for ERROR nodes containing immediate_paren.
	return findArgInError(root, source, line, col)
}

// findArgInArgValues handles Pattern A: cursor is inside an arg_values node
// that tree-sitter successfully parsed (e.g. "foo(" with empty args, or
// "foo(x, " with trailing comma).
func findArgInArgValues(root *tree_sitter.Node, source []byte, line, col uint) *CompletionContext {
	var deepest *tree_sitter.Node
	walkTS(root, func(n *tree_sitter.Node) bool {
		if !tsContains(n.StartPosition(), n.EndPosition(), line, col) {
			return false
		}
		deepest = n
		return true
	})
	if deepest == nil {
		return nil
	}

	// Walk up from the deepest node looking for arg_values.
	node := deepest
	for node != nil {
		if node.Kind() == "arg_values" {
			return buildArgContextFromArgValues(node, source, line, col)
		}
		node = node.Parent()
	}

	// The cursor may be just past the end of an arg_values that has a
	// trailing comma and a missing ')'. Tree-sitter emits a zero-width
	// MISSING ')' so the arg_values range doesn't include the trailing
	// whitespace after the comma. Check for this by looking for arg_values
	// nodes that end just before the cursor.
	var bestArgValues *tree_sitter.Node
	walkTS(root, func(n *tree_sitter.Node) bool {
		if n.Kind() == "arg_values" {
			end := n.EndPosition()
			// The arg_values must end on the same line at or just before
			// the cursor (the cursor is in trailing whitespace).
			if end.Row == line && end.Column <= col && end.Column >= col-1 {
				bestArgValues = n
			}
		}
		return true
	})
	if bestArgValues != nil {
		// Verify the arg_values has a trailing sep (comma) as last real child.
		if hasTrailingSep(bestArgValues) {
			return buildArgContextFromArgValues(bestArgValues, source, line, col)
		}
	}

	return nil
}

// hasTrailingSep checks if an arg_values node's last non-MISSING child is a
// sep (comma), indicating the user typed "(" + args + "," and the cursor is
// after the comma expecting the next argument.
func hasTrailingSep(argValues *tree_sitter.Node) bool {
	count := argValues.ChildCount()
	for i := int(count) - 1; i >= 0; i-- {
		child := argValues.Child(uint(i))
		if child == nil {
			continue
		}
		// Skip the MISSING ')' that tree-sitter inserts.
		if child.IsMissing() {
			continue
		}
		return child.Kind() == "sep"
	}
	return false
}

// buildArgContextFromArgValues extracts completion context from a well-formed
// arg_values node.
func buildArgContextFromArgValues(argValues *tree_sitter.Node, source []byte, line, col uint) *CompletionContext {
	callNode := argValues.Parent()
	if callNode == nil {
		return nil
	}

	funcText := extractFuncText(callNode, source)
	if funcText == "" {
		return nil
	}

	// Check value position: cursor inside a key_value's value field.
	if isInValuePosition(argValues, source, line, col) {
		return nil
	}

	partial := extractPartialAtCursor(source, line, col)

	// Check colon-based value position for partial typing after "name:".
	if isAfterColon(source, line, col, partial) {
		return nil
	}

	providedArgs := collectProvidedArgs(argValues, source)

	return &CompletionContext{
		Kind:         ContextArgument,
		FuncText:     funcText,
		Partial:      partial,
		ProvidedArgs: providedArgs,
	}
}

// findArgInError handles Patterns B and C: the argument context is inside an
// ERROR node that contains immediate_paren tokens.
//
// Pattern B: ERROR is a sibling of select_or_call — the select_or_call is the
// function expression (e.g. "container.from(addr").
//
// Pattern C: ERROR contains everything — function, paren, and args as flat
// children (e.g. "foo(na" or "foo(name: x, addr").
func findArgInError(root *tree_sitter.Node, source []byte, line, col uint) *CompletionContext {
	// Find ERROR nodes containing the cursor that have immediate_paren children.
	var bestError *tree_sitter.Node
	walkTS(root, func(n *tree_sitter.Node) bool {
		if !tsContains(n.StartPosition(), n.EndPosition(), line, col) {
			return false
		}
		if n.Kind() == "ERROR" && hasChildKind(n, "immediate_paren") {
			bestError = n
		}
		return true
	})

	if bestError == nil {
		return nil
	}

	// Find the innermost unmatched immediate_paren by scanning children
	// right-to-left, tracking paren depth.
	parenIdx := findInnermostOpenParen(bestError, line, col)
	if parenIdx < 0 {
		return nil
	}

	// Reconstruct the function expression from children before the paren.
	funcText := reconstructFuncFromError(bestError, parenIdx, source)
	if funcText == "" {
		// Pattern B: the ERROR's preceding sibling is the function.
		parent := bestError.Parent()
		if parent != nil {
			funcText = extractFuncFromPrecedingSibling(parent, bestError, source)
		}
	}
	if funcText == "" {
		return nil
	}

	// Check if cursor is inside brackets (suppress arg completion).
	if isInsideBrackets(bestError, parenIdx, line, col) {
		return nil
	}

	partial := extractPartialAtCursor(source, line, col)

	// Check value position: after a colon_token.
	if isAfterColon(source, line, col, partial) {
		return nil
	}

	// Check value position: inside a key_value value field.
	if isInKeyValueValuePosition(bestError, source, line, col) {
		return nil
	}

	// Collect already-provided argument names.
	providedArgs := collectProvidedArgsFromError(bestError, parenIdx, source)

	return &CompletionContext{
		Kind:         ContextArgument,
		FuncText:     funcText,
		Partial:      partial,
		ProvidedArgs: providedArgs,
	}
}

// extractFuncText extracts the function expression text from a call or
// select_or_call node.
func extractFuncText(callNode *tree_sitter.Node, source []byte) string {
	switch callNode.Kind() {
	case "select_or_call":
		leftNode := callNode.ChildByFieldName("left")
		nameNode := callNode.ChildByFieldName("name")
		if leftNode != nil && nameNode != nil {
			return collapseWhitespace(leftNode.Utf8Text(source)) + "." + nameNode.Utf8Text(source)
		}
		if nameNode != nil {
			return nameNode.Utf8Text(source)
		}
		if leftNode != nil {
			return collapseWhitespace(leftNode.Utf8Text(source))
		}
	case "symbol_or_call":
		// symbol_or_call wraps a call node.
		for i := uint(0); i < callNode.ChildCount(); i++ {
			child := callNode.Child(i)
			if child != nil && child.Kind() == "call" {
				return extractFuncText(child, source)
			}
		}
	case "call":
		nameNode := callNode.ChildByFieldName("name")
		if nameNode != nil {
			return collapseWhitespace(nameNode.Utf8Text(source))
		}
	}
	return ""
}

// hasChildKind checks if a node has any child with the given kind.
func hasChildKind(node *tree_sitter.Node, kind string) bool {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil && child.Kind() == kind {
			return true
		}
	}
	return false
}

// findInnermostOpenParen scans the ERROR node's children right-to-left,
// tracking paren depth, and returns the index of the innermost unmatched
// immediate_paren that is before the cursor. Returns -1 if not found.
func findInnermostOpenParen(errorNode *tree_sitter.Node, line, col uint) int {
	depth := 0
	count := int(errorNode.ChildCount())
	for i := count - 1; i >= 0; i-- {
		child := errorNode.Child(uint(i))
		if child == nil {
			continue
		}
		// Only consider children at or before the cursor.
		if !tsPointBefore(child.StartPosition(), line, col) {
			continue
		}
		switch child.Kind() {
		case ")":
			depth++
		case "immediate_paren":
			if depth > 0 {
				depth--
			} else {
				return i
			}
		}
	}
	return -1
}

// tsPointBefore returns true if the point (row, col) is at or before (line, col).
func tsPointBefore(p tree_sitter.Point, line, col uint) bool {
	if p.Row < line {
		return true
	}
	if p.Row == line && p.Column <= col {
		return true
	}
	return false
}

// reconstructFuncFromError builds the function expression text from the
// ERROR node's children that appear before the given paren index.
//
// It handles patterns like:
//   - symbol + immediate_paren → "funcname"
//   - symbol_or_call + dot_token + field_id + immediate_paren → "receiver.method"
func reconstructFuncFromError(errorNode *tree_sitter.Node, parenIdx int, source []byte) string {
	// Collect non-trivial children before the paren.
	var parts []string
	for i := 0; i < parenIdx; i++ {
		child := errorNode.Child(uint(i))
		if child == nil {
			continue
		}
		kind := child.Kind()
		// Skip separators and other non-expression children.
		if kind == "sep" || kind == "immediate_paren" || kind == ")" {
			continue
		}
		switch kind {
		case "symbol", "symbol_or_call", "select_or_call":
			parts = append(parts, collapseWhitespace(child.Utf8Text(source)))
		case "dot_token":
			parts = append(parts, ".")
		case "field_id":
			parts = append(parts, child.Utf8Text(source))
		default:
			// Unknown child type before paren — not a function expression
			// we can reconstruct.
			parts = append(parts, collapseWhitespace(child.Utf8Text(source)))
		}
	}
	return strings.Join(parts, "")
}

// extractFuncFromPrecedingSibling finds the expression sibling just before
// the ERROR node and returns its text as the function expression.
func extractFuncFromPrecedingSibling(parent, errorNode *tree_sitter.Node, source []byte) string {
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
		return ""
	}
	// The preceding sibling might be a select_or_call — extract its full
	// dotted expression including left.name.
	return extractFuncText(prevExpr, source)
}

// isInsideBrackets checks if the cursor is inside an unmatched '[' between
// the open paren and the cursor position within the ERROR node.
func isInsideBrackets(errorNode *tree_sitter.Node, parenIdx int, line, col uint) bool {
	bracketDepth := 0
	count := int(errorNode.ChildCount())
	for i := parenIdx + 1; i < count; i++ {
		child := errorNode.Child(uint(i))
		if child == nil {
			continue
		}
		// Stop scanning past the cursor.
		if !tsPointBefore(child.StartPosition(), line, col) {
			break
		}
		switch child.Kind() {
		case "[":
			bracketDepth++
		case "]":
			if bracketDepth > 0 {
				bracketDepth--
			}
		default:
			// Check inside child nodes for brackets.
			walkTS(child, func(n *tree_sitter.Node) bool {
				switch n.Kind() {
				case "[":
					bracketDepth++
				case "]":
					if bracketDepth > 0 {
						bracketDepth--
					}
				}
				return true
			})
		}
	}
	return bracketDepth > 0
}

// isInValuePosition checks if the cursor is inside a key_value node's value
// field within an arg_values node.
func isInValuePosition(argValues *tree_sitter.Node, source []byte, line, col uint) bool {
	return isInKeyValueValuePosition(argValues, source, line, col)
}

// isInKeyValueValuePosition walks the subtree looking for key_value nodes
// where the cursor is in the value field.
func isInKeyValueValuePosition(node *tree_sitter.Node, source []byte, line, col uint) bool {
	found := false
	walkTS(node, func(n *tree_sitter.Node) bool {
		if found {
			return false
		}
		if !tsContains(n.StartPosition(), n.EndPosition(), line, col) {
			return false
		}
		if n.Kind() == "key_value" {
			valueNode := n.ChildByFieldName("value")
			if valueNode != nil && tsContains(valueNode.StartPosition(), valueNode.EndPosition(), line, col) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// isAfterColon checks if the text before the partial (ignoring whitespace)
// ends with a colon, indicating value position after "name:".
func isAfterColon(source []byte, line, col uint, partial string) bool {
	offset := lineColToOffset(source, line, col)
	beforePartial := offset - len(partial)
	if beforePartial <= 0 {
		return false
	}
	j := beforePartial - 1
	for j >= 0 && (source[j] == ' ' || source[j] == '\t' || source[j] == '\n') {
		j--
	}
	return j >= 0 && source[j] == ':'
}

// collectProvidedArgs extracts already-provided argument names from key_value
// children of an arg_values node.
func collectProvidedArgs(argValues *tree_sitter.Node, source []byte) []string {
	var names []string
	for i := uint(0); i < argValues.ChildCount(); i++ {
		child := argValues.Child(i)
		if child == nil {
			continue
		}
		collectKeyValueNames(child, source, &names)
	}
	return names
}

// collectProvidedArgsFromError extracts already-provided argument names from
// key_value children that appear after the given paren index in an ERROR node.
func collectProvidedArgsFromError(errorNode *tree_sitter.Node, parenIdx int, source []byte) []string {
	var names []string
	count := int(errorNode.ChildCount())
	for i := parenIdx + 1; i < count; i++ {
		child := errorNode.Child(uint(i))
		if child == nil {
			continue
		}
		collectKeyValueNames(child, source, &names)
	}
	return names
}

// collectKeyValueNames recursively finds key_value nodes and appends their
// key text to names.
func collectKeyValueNames(node *tree_sitter.Node, source []byte, names *[]string) {
	if node.Kind() == "key_value" {
		keyNode := node.ChildByFieldName("key")
		if keyNode != nil {
			*names = append(*names, keyNode.Utf8Text(source))
		}
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil {
			collectKeyValueNames(child, source, names)
		}
	}
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
