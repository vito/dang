//go:build cgo

package dang

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// classOf returns the class covering the first rune of needle within source,
// or "" if needle isn't found / isn't styled. Used to assert highlighting
// without pinning exact span boundaries.
func classOf(t *testing.T, source, needle string) string {
	t.Helper()
	idx := runeIndex(source, needle)
	require.GreaterOrEqual(t, idx, 0, "needle %q not found in %q", needle, source)
	for _, sp := range Highlight(source) {
		if idx >= sp.Start && idx < sp.End {
			return sp.Class
		}
	}
	return ""
}

func runeIndex(source, needle string) int {
	runes := []rune(source)
	nr := []rune(needle)
	for i := 0; i+len(nr) <= len(runes); i++ {
		if string(runes[i:i+len(nr)]) == needle {
			return i
		}
	}
	return -1
}

func TestHighlightClasses(t *testing.T) {
	src := `let x = 42
let s = "hello"
# a comment
let c: Container = null`

	assert.Equal(t, ClassKeyword, classOf(t, src, "let"))
	assert.Equal(t, ClassNumber, classOf(t, src, "42"))
	assert.Equal(t, ClassString, classOf(t, src, `"hello"`))
	assert.Equal(t, ClassComment, classOf(t, src, "# a comment"))
	// Capitalized names in type position are types (in expression position
	// they're variables — matching the editors and docs).
	assert.Equal(t, ClassType, classOf(t, src, "Container"))
	assert.Equal(t, ClassOperator, classOf(t, src, "="))
}

func TestHighlightSpansSortedNonOverlapping(t *testing.T) {
	spans := Highlight(`let greeting = "hi"`)
	require.NotEmpty(t, spans)
	prevEnd := 0
	for _, sp := range spans {
		assert.GreaterOrEqual(t, sp.Start, prevEnd, "spans overlap or unsorted: %+v", spans)
		assert.Less(t, sp.Start, sp.End, "empty span")
		assert.NotEmpty(t, sp.Class)
		prevEnd = sp.End
	}
}

func TestHighlightUnicodeRuneOffsets(t *testing.T) {
	// A multibyte rune before a keyword must not shift the keyword's span:
	// offsets are in runes, not bytes.
	src := `"café" let x = 1`
	assert.Equal(t, ClassKeyword, classOf(t, src, "let"))
	assert.Equal(t, ClassString, classOf(t, src, `"café"`))
}

func TestHighlightEmptyAndPlain(t *testing.T) {
	assert.Empty(t, Highlight(""))
}
