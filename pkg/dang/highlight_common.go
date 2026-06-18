package dang

import "strings"

// HighlightSpan is a styled region of source code identified by tree-sitter.
// Start and End are rune offsets into the source, half-open [Start, End).
// Class is a presentation-neutral token class (see the constants below);
// callers map it onto colors/styles for their medium.
type HighlightSpan struct {
	Start, End int
	Class      string
}

// Token classes returned in HighlightSpan.Class. These collapse the
// editors' fine-grained tree-sitter captures into the handful of roles a
// terminal palette distinguishes. Class "" means unstyled.
const (
	ClassKeyword   = "keyword"   // let, pub, if, type, and/or, ...
	ClassType      = "type"      // capitalized type names
	ClassNumber    = "number"    // int/float/bool/null and other constants
	ClassString    = "string"    // string and template literals, docstrings
	ClassEscape    = "escape"    // escapes within strings
	ClassComment   = "comment"   // comments
	ClassOperator  = "operator"  // =, +=, ??, ->, &, comparisons, ...
	ClassFunction  = "function"  // function/method names
	ClassBuiltin   = "builtin"   // builtin functions (assert, print, ...)
	ClassDirective = "directive" // @directive names
	ClassSelf      = "self"      // the self keyword
	ClassLabel     = "label"     // language tags on templates
	ClassProperty  = "property"  // argument/key names
	ClassVariable  = "variable"  // bare identifiers
	ClassPunct     = "punct"     // brackets, separators
)

// captureClass maps a tree-sitter highlight query capture name onto a token
// class. It mirrors docs/go/highlight.go's captureClass (which maps the same
// captures onto the docs site's CSS classes); the two must stay in lockstep
// so the REPL, editors, and docs classify code identically. Unhandled
// captures (notably @error) return "" to stay unstyled.
func captureClass(name string) string {
	switch name {
	case "variable.special":
		return ClassSelf
	case "function.builtin":
		return ClassBuiltin
	case "function.macro":
		return ClassDirective
	case "string.escape":
		return ClassEscape
	case "property":
		return ClassProperty
	case "label":
		return ClassLabel
	case "type":
		return ClassType
	}
	switch strings.SplitN(name, ".", 2)[0] {
	case "keyword":
		return ClassKeyword
	case "constant", "number":
		return ClassNumber
	case "string":
		return ClassString
	case "comment":
		return ClassComment
	case "operator":
		return ClassOperator
	case "punctuation":
		return ClassPunct
	case "function":
		return ClassFunction
	case "variable":
		return ClassVariable
	}
	return ""
}
