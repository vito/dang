package dang

import (
	stdErrors "errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// ParseWithRecovery parses source using the PEG parser. On failure, it uses
// tree-sitter to produce a SourceError with precise location and a
// human-readable message.
func ParseWithRecovery(filename string, source []byte, opts ...Option) (any, error) {
	result, err := Parse(filename, source, opts...)
	if err != nil {
		return nil, enhanceParseError(err, filename, source, optionsVersion(opts))
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
		return nil, enhanceParseError(err, filename, source, optionsVersion(opts))
	}
	return result, nil
}

// EnhanceParseError produces a SourceError with a precise location and a
// human-readable message when the PEG parser fails: a pitfall's (see
// Pitfall) if the failure is one, else tree-sitter's diagnosis, else the
// token the parser failed at. If none applies, the original error is
// returned.
//
// Tree-sitter is the C runtime with cgo, and the same runtime compiled to
// WebAssembly without it (see parse_errors_wazero.go), so both builds say the
// same. Only GOOS=js and builds tagged dang_nowasm go without, and say
// which token the parser failed at instead.
func EnhanceParseError(pegErr error, filename string, source []byte) error {
	return enhanceParseError(pegErr, filename, source, CurrentVersion)
}

// enhanceParseError is EnhanceParseError for source parsed as Dang v.
func enhanceParseError(pegErr error, filename string, source []byte, v Version) error {
	if rejected, ok := rejectedSourceError(pegErr, filename, source); ok {
		return rejected
	}
	if pitfall, ok := syntaxPitfallError(pegErr, filename, source, v); ok {
		return pitfall
	}
	if diagnosed, ok := treeSitterSyntaxError(pegErr, filename, source); ok {
		return diagnosed
	}
	if unexpected, ok := unexpectedSyntaxError(pegErr, filename, source); ok {
		return unexpected
	}
	return pegErr
}

// syntaxNode is the part of a tree-sitter node that error reporting reads,
// whichever runtime parsed it (see parseSyntaxTree).
type syntaxNode interface {
	Kind() string
	IsMissing() bool
	IsError() bool
	ChildCount() int
	Child(i int) syntaxNode
	Text() string
	Start() (row, col uint)
	End() (row, col uint)
	ByteLen() int
}

// treeSitterSyntaxError diagnoses a syntax error the PEG parser failed with
// (pegErr) with tree-sitter's error recovery, if it finds one.
func treeSitterSyntaxError(pegErr error, filename string, source []byte) (*SourceError, bool) {
	root, release := parseSyntaxTree(source)
	if root == nil {
		return nil, false
	}
	defer release()

	// Collect all ERROR and MISSING nodes from the CST.
	var errors []tsError
	collectTSErrors(root, &errors)

	if len(errors) == 0 {
		return nil, false
	}

	// Use the first (earliest) error to produce the diagnostic, unless the
	// PEG got past it: tree-sitter lexes a docstring like a `"""` string (see
	// patchDocStringShape), so it may see an error in a verbatim docstring
	// the PEG accepted, like at a `${` that starts no expression. The last
	// error before where the PEG failed is then the one to report.
	first := errors[0]
	var pe *parserError
	if stdErrors.As(pegErr, &pe) {
		pegLine, pegCol := uint(pe.pos.line-1), uint(pe.pos.col)
		for _, e := range errors[1:] {
			if e.line < pegLine || (e.line == pegLine && e.col <= pegCol) {
				first = e
			}
		}
	}

	loc := &SourceLocation{
		Filename: filename,
		Line:     int(first.line) + 1, // 1-based
		Column:   int(first.col) + 1,  // 1-based
		Length:   max(1, first.length),
	}

	return NewSourceError(fmt.Errorf("syntax error: %s", first.message), loc, string(source)), true
}

// tsError represents a single error found by tree-sitter.
type tsError struct {
	line    uint
	col     uint
	length  int
	message string
}

// collectTSErrors walks the tree-sitter CST and collects error information.
func collectTSErrors(node syntaxNode, errors *[]tsError) {
	if node == nil {
		return
	}

	if node.IsMissing() {
		msg := describeMissing(node)
		row, col := node.Start()
		*errors = append(*errors, tsError{
			line:    row,
			col:     col,
			length:  1,
			message: msg,
		})
		return
	}

	if node.IsError() {
		msg := describeError(node)
		startRow, startCol := node.Start()
		endRow, endCol := node.End()
		length := node.ByteLen()
		if startRow == endRow {
			length = int(endCol - startCol)
		}
		if length < 1 {
			length = 1
		}
		*errors = append(*errors, tsError{
			line:    startRow,
			col:     startCol,
			length:  length,
			message: msg,
		})
		// Don't recurse into ERROR children — we've described it.
		return
	}

	// Recurse into children to find nested ERROR/MISSING nodes.
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil {
			collectTSErrors(child, errors)
		}
	}
}

// describeMissing produces a message for a MISSING node (tree-sitter
// expected a token but didn't find one).
func describeMissing(node syntaxNode) string {
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
func describeError(node syntaxNode) string {
	// Collect the kinds of children inside the ERROR to understand context.
	var childKinds []string
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil {
			childKinds = append(childKinds, child.Kind())
		}
	}

	// Check for common patterns.

	// Pattern: let/pub + symbol but no "=" → missing assignment. The name
	// may still be a bare id: a function field with type parameters names
	// itself with one (see patchTypeParamsName).
	if contains(childKinds, "visibility") && containsAny(childKinds, "symbol", "id") && !contains(childKinds, "equal_token") {
		return "expected '=' after variable name"
	}

	// Pattern: visibility + "fn" → function declaration syntax error
	if contains(childKinds, "visibility") && containsAny(childKinds, "ERROR") {
		// Check if nested ERROR is "fn"
		for i := 0; i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child != nil && child.IsError() {
				if child.Text() == "fn" {
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
	text := strings.TrimSpace(node.Text())
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
		// A `${` token appearing as a direct ERROR child (rather than nested
		// inside a single_template_part subtree, which is what a properly
		// closed interpolation produces) means the parser tokenized an
		// interpolation start it could never finish. Surface that case.
		if contains(childKinds, "${") {
			return "unclosed '${' interpolation in template"
		}
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
	return slices.Contains(s, v)
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
