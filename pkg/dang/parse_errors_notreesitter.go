//go:build !cgo && (js || dang_nowasm)

package dang

// parseSyntaxTree finds no tree: this build has no tree-sitter (GOOS=js, or
// tagged dang_nowasm to leave wazero out), so a parse error names the token
// the parser failed at.
func parseSyntaxTree([]byte) (syntaxNode, func()) {
	return nil, nil
}
