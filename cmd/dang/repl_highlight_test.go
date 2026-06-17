//go:build cgo

package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vito/dang/v2/pkg/dang"
)

func TestHighlightCodeStylesAndPreservesText(t *testing.T) {
	code := `let greeting = "hi"`
	out := highlightCode(code)

	// Styling was added but visible text is unchanged (width-preserving).
	assert.NotEqual(t, code, out, "expected ANSI styling")
	assert.Contains(t, out, "\x1b[", "expected ANSI escape codes")
	assert.Equal(t, code, ansi.Strip(out), "styling must preserve the visible text")

	// The keyword "let" is wrapped in the keyword color, the string in the
	// string color — exactly as the class styles render them.
	assert.Contains(t, out, classStyles[dang.ClassKeyword].Render("let"))
	assert.Contains(t, out, classStyles[dang.ClassString].Render(`"hi"`))
}

func TestHighlightCodeMultilineSplitsSafely(t *testing.T) {
	// A style must never bleed across a line break, so each display line can be
	// rendered independently. Every line, when stripped, is plain source.
	code := "let x = 1\nlet y = 2"
	out := highlightCode(code)
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "let x = 1", ansi.Strip(lines[0]))
	assert.Equal(t, "let y = 2", ansi.Strip(lines[1]))
}

func TestHighlightSpansWiring(t *testing.T) {
	spans := highlightSpans(`let x = 1`)
	require.NotEmpty(t, spans)
	for _, sp := range spans {
		assert.Less(t, sp.Start, sp.End)
		require.NotNil(t, sp.Style)
		// Style is width-preserving.
		styled := sp.Style("x")
		assert.Equal(t, "x", ansi.Strip(styled))
	}
}

func TestHighlightSkipsReplCommands(t *testing.T) {
	assert.Nil(t, highlightSpans(":help"))
	assert.Equal(t, ":type foo", highlightCode(":type foo"))
}
