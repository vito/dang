//go:build js || dang_nowasm

package dang

// Highlight returns no spans without WebAssembly, where tree-sitter is
// unavailable; callers render plain text.
func Highlight(source string) []HighlightSpan { return nil }
