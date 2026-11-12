package dang

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUnexpectedSyntaxError checks the diagnosis of last resort for a parse
// failure: the token the parser failed at. It's what a parse error says
// without tree-sitter (GOOS=js, or -tags dang_nowasm) when no pitfall matches,
// in place of the parser's list of every token it would have taken.
func TestUnexpectedSyntaxError(t *testing.T) {
	for _, tt := range []struct {
		src  string
		want string // line:col: message
	}{
		{"let x = 1 2\n", "1:11: syntax error: unexpected '2'"},
		{"let x = 1 +\n", "2:1: syntax error: unexpected end of file"},
		{"let x = (1 + 2\nprint(x)", "2:1: syntax error: unexpected 'print'"},
		{"let x = 1 ? 2 : 3\n", "1:11: syntax error: unexpected '?'"},
		{"let x = @y\n", "1:9: syntax error: unexpected '@'"},
		{"let x = 1 => 2\n", "1:11: syntax error: unexpected '=>'"},
		{"print(1)\nlet x = [1, 2) \n", "2:14: syntax error: unexpected ')'"},
		{"let x = 0x1F\n", "1:9: syntax error: unexpected '0x1F'"},
		// It fails inside `true`, past what parsed as a type variable.
		{"let p = { ok: true }\n", "1:15: syntax error: unexpected 'true'"},
		// Python's floor division fails at the second slash.
		{"let n = 10\nlet half = n // 2\n", "2:14: syntax error: unexpected '//'"},
	} {
		t.Run(tt.src, func(t *testing.T) {
			_, pegErr := Parse("main.dang", []byte(tt.src))
			require.Error(t, pegErr)
			got, ok := unexpectedSyntaxError(pegErr, "main.dang", []byte(tt.src))
			require.True(t, ok, "not a parse failure: %v", pegErr)
			require.Equal(t, tt.want, fmt.Sprintf("%d:%d: %s", got.Location.Line, got.Location.Column, got.Inner))
		})
	}
}
