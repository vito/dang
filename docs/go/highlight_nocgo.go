//go:build !cgo

package dangdocs

// Without cgo there are no tree-sitter grammars: classifyCode reports every
// language as unhighlightable and renderCode emits plain code (in the same
// wrapper, with stdlib links intact).
func classifyCode(language, source string) []string {
	return nil
}
