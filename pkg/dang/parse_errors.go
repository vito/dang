package dang

import (
	"fmt"
	"os"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// ParseWithRecovery parses source using the PEG parser. On failure, it uses
// tree-sitter to produce a SourceError with precise location and a
// human-readable message.
func ParseWithRecovery(filename string, source []byte, opts ...Option) (any, error) {
	result, err := Parse(filename, source, opts...)
	if err != nil {
		return nil, EnhanceParseError(err, filename, source)
	}
	return result, nil
}

// ParseFileWithRecovery is like ParseFile but enhances parse errors with
// tree-sitter error recovery.
func ParseFileWithRecovery(filename string, opts ...Option) (any, error) {
	result, err := ParseFile(filename, opts...)
	if err != nil {
		// Read source for tree-sitter — ParseFile reads internally but
		// doesn't expose the bytes, so we read again.
		source, readErr := os.ReadFile(filename)
		if readErr != nil {
			return nil, err // can't enhance, return original
		}
		return nil, EnhanceParseError(err, filename, source)
	}
	return result, nil
}

// EnhanceParseError uses tree-sitter to produce a SourceError with precise
// location and a human-readable message when the PEG parser fails. If
// tree-sitter can't improve the error, the original error is returned.
func EnhanceParseError(pegErr error, filename string, source []byte) error {
	tsMu.Lock()
	tree := tsParser.Parse(source, nil)
	tsMu.Unlock()

	if tree == nil {
		return pegErr
	}
	defer tree.Close()

	root := tree.RootNode()

	// Collect all ERROR and MISSING nodes from the CST.
	var errors []tsError
	collectTSErrors(root, source, &errors)

	if len(errors) == 0 {
		return pegErr
	}

	// Use the first (earliest) error to produce the diagnostic.
	first := errors[0]

	loc := &SourceLocation{
		Filename: filename,
		Line:     int(first.line) + 1, // 1-based
		Column:   int(first.col) + 1,  // 1-based
		Length:   max(1, first.length),
	}

	return NewSourceError(fmt.Errorf("syntax error: %s", first.message), loc, string(source))
}

// tsError represents a single error found by tree-sitter.
type tsError struct {
	line    uint
	col     uint
	length  int
	message string
}

// collectTSErrors walks the tree-sitter CST and collects error information.
func collectTSErrors(node *tree_sitter.Node, source []byte, errors *[]tsError) {
	if node == nil {
		return
	}

	if node.IsMissing() {
		msg := describeMissing(node)
		*errors = append(*errors, tsError{
			line:    node.StartPosition().Row,
			col:     node.StartPosition().Column,
			length:  1,
			message: msg,
		})
		return
	}

	if node.IsError() {
		msg := describeError(node, source)
		start := node.StartPosition()
		end := node.EndPosition()
		length := int(node.EndByte() - node.StartByte())
		if start.Row == end.Row {
			length = int(end.Column - start.Column)
		}
		if length < 1 {
			length = 1
		}
		*errors = append(*errors, tsError{
			line:    start.Row,
			col:     start.Column,
			length:  length,
			message: msg,
		})
		// Don't recurse into ERROR children — we've described it.
		return
	}

	// Recurse into children to find nested ERROR/MISSING nodes.
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil {
			collectTSErrors(child, source, errors)
		}
	}
}

// describeMissing produces a message for a MISSING node (tree-sitter
// expected a token but didn't find one).
func describeMissing(node *tree_sitter.Node) string {
	kind := node.Kind()
	// Tree-sitter MISSING nodes have the kind of the expected token.
	switch kind {
	case ")":
		return "missing closing ')'"
	case "]":
		return "missing closing ']'"
	case "}":
		return "missing closing '}'"
	case "immediate_quote_token", "\"":
		return "unterminated string literal"
	case "'":
		return "unterminated string literal"
	default:
		return fmt.Sprintf("missing '%s'", kind)
	}
}

// describeError produces a message for an ERROR node by examining its
// children and surrounding context to infer what went wrong.
func describeError(node *tree_sitter.Node, source []byte) string {
	// Collect the kinds of children inside the ERROR to understand context.
	var childKinds []string
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil {
			childKinds = append(childKinds, child.Kind())
		}
	}

	// Check for common patterns.

	// Pattern: let/pub + symbol but no "=" → missing assignment
	if containsAll(childKinds, "visibility", "symbol") && !contains(childKinds, "equal_token") {
		return "expected '=' after variable name"
	}

	// Pattern: visibility + "fn" → function declaration syntax error
	if contains(childKinds, "visibility") && containsAny(childKinds, "ERROR") {
		// Check if nested ERROR is "fn"
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child != nil && child.IsError() {
				text := child.Utf8Text(source)
				if text == "fn" {
					return "invalid function declaration"
				}
			}
		}
	}

	// Pattern: if_token alone → incomplete if expression
	if len(childKinds) == 1 && childKinds[0] == "if_token" {
		return "incomplete 'if' expression"
	}

	// Pattern: ERROR contains just a closing delimiter
	text := strings.TrimSpace(node.Utf8Text(source))
	if text == ")" {
		return "unexpected ')'"
	}
	if text == "]" {
		return "unexpected ']'"
	}
	if text == "}" {
		return "unexpected '}'"
	}

	// Pattern: ERROR with visibility + symbol + equal_token → bad value expression
	if containsAll(childKinds, "visibility", "symbol", "equal_token") {
		return "invalid expression after '='"
	}

	// Default: show the unexpected text.
	if len(text) > 40 {
		text = text[:40] + "..."
	}
	return fmt.Sprintf("unexpected '%s'", text)
}

// contains checks if a string slice contains a value.
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// containsAny checks if a string slice contains any of the given values.
func containsAny(s []string, vals ...string) bool {
	for _, v := range vals {
		if contains(s, v) {
			return true
		}
	}
	return false
}

// containsAll checks if a string slice contains all of the given values.
func containsAll(s []string, vals ...string) bool {
	for _, v := range vals {
		if !contains(s, v) {
			return false
		}
	}
	return true
}
