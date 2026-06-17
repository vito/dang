//go:build !cgo

package dang

// Highlight returns no spans without CGo, where tree-sitter is unavailable;
// callers render plain text.
func Highlight(source string) []HighlightSpan { return nil }
