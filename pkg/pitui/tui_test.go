package pitui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTerminal records writes and simulates a fixed-size terminal.
type mockTerminal struct {
	cols, rows int
	written    strings.Builder
	onInput    func([]byte)
	onResize   func()
}

func newMockTerminal(cols, rows int) *mockTerminal {
	return &mockTerminal{cols: cols, rows: rows}
}

func (m *mockTerminal) Start(onInput func([]byte), onResize func()) error {
	m.onInput = onInput
	m.onResize = onResize
	return nil
}
func (m *mockTerminal) Stop()               {}
func (m *mockTerminal) Write(p []byte)      { m.written.Write(p) }
func (m *mockTerminal) WriteString(s string) { m.written.WriteString(s) }
func (m *mockTerminal) Columns() int        { return m.cols }
func (m *mockTerminal) Rows() int           { return m.rows }
func (m *mockTerminal) HideCursor()         { m.written.WriteString("\x1b[?25l") }
func (m *mockTerminal) ShowCursor()         { m.written.WriteString("\x1b[?25h") }

func (m *mockTerminal) reset() { m.written.Reset() }

// staticComponent renders fixed lines.
type staticComponent struct {
	lines []string
}

func (s *staticComponent) Render(width int) []string {
	out := make([]string, len(s.lines))
	for i, l := range s.lines {
		if VisibleWidth(l) > width {
			out[i] = Truncate(l, width, "")
		} else {
			out[i] = l
		}
	}
	return out
}
func (s *staticComponent) Invalidate() {}

// renderSync calls doRender directly (bypasses goroutine scheduling).
func renderSync(t *TUI) {
	t.mu.Lock()
	t.renderRequested = false
	t.mu.Unlock()
	t.doRender()
}

func TestFirstRender(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)
	tui.AddChild(&staticComponent{lines: []string{"hello", "world"}})

	// Simulate start without goroutines.
	tui.stopped = false
	term.reset()

	renderSync(tui)

	out := term.written.String()
	assert.Contains(t, out, "hello")
	assert.Contains(t, out, "world")
	// Should use synchronized output.
	assert.Contains(t, out, "\x1b[?2026h")
	assert.Contains(t, out, "\x1b[?2026l")
}

func TestDifferentialRender(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)
	comp := &staticComponent{lines: []string{"line1", "line2", "line3"}}
	tui.AddChild(comp)
	tui.stopped = false

	// First render.
	renderSync(tui)
	assert.Equal(t, 1, tui.FullRedraws())

	// Change only the second line.
	comp.lines[1] = "LINE2"
	term.reset()
	renderSync(tui)

	out := term.written.String()
	// Should NOT be a full redraw (no clear scrollback sequence).
	assert.NotContains(t, out, "\x1b[3J")
	// Should contain the updated line.
	assert.Contains(t, out, "LINE2")
	// Should NOT re-render unchanged lines.
	assert.NotContains(t, out, "line1")
	assert.NotContains(t, out, "line3")
	// Still only 1 full redraw.
	assert.Equal(t, 1, tui.FullRedraws())
}

func TestAppendLines(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)
	comp := &staticComponent{lines: []string{"a"}}
	tui.AddChild(comp)
	tui.stopped = false

	renderSync(tui)
	term.reset()

	// Append new lines.
	comp.lines = []string{"a", "b", "c"}
	renderSync(tui)

	out := term.written.String()
	assert.Contains(t, out, "b")
	assert.Contains(t, out, "c")
	// "a" is unchanged, should not appear.
	// (It will appear as part of ANSI sequences but not as a standalone line.)
	assert.NotContains(t, out, "\x1b[2Ka"+segmentReset) // not rewritten
}

func TestWidthChangeTriggersFullRedraw(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)
	tui.AddChild(&staticComponent{lines: []string{"hello"}})
	tui.stopped = false

	renderSync(tui)
	assert.Equal(t, 1, tui.FullRedraws())

	// Simulate resize.
	term.cols = 60
	term.reset()
	renderSync(tui)
	assert.Equal(t, 2, tui.FullRedraws())
}

func TestOffscreenChangeTriggersFullRedraw(t *testing.T) {
	term := newMockTerminal(40, 5) // only 5 rows visible
	tui := New(term)

	// Create enough content to scroll.
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = strings.Repeat("x", 10)
	}
	comp := &staticComponent{lines: lines}
	tui.AddChild(comp)
	tui.stopped = false

	renderSync(tui)
	assert.Equal(t, 1, tui.FullRedraws())

	// Change a line that is above the viewport (line 0 is off-screen when
	// we have 20 lines and 5 rows).
	comp.lines[0] = "CHANGED"
	term.reset()
	renderSync(tui)

	// Should trigger a full redraw because the change is above viewport.
	assert.Equal(t, 2, tui.FullRedraws())
	out := term.written.String()
	assert.Contains(t, out, "\x1b[3J") // scrollback cleared
}

func TestNoChangeNoOutput(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)
	tui.AddChild(&staticComponent{lines: []string{"stable"}})
	tui.stopped = false

	renderSync(tui)
	term.reset()

	// Render again with no changes.
	renderSync(tui)

	out := term.written.String()
	// Should only have cursor positioning (hide cursor), no content writes.
	assert.NotContains(t, out, "stable")
	assert.NotContains(t, out, "\x1b[2K") // no line clears
}

func TestCursorMarkerExtraction(t *testing.T) {
	lines := []string{
		"first line",
		"cur" + CursorMarker + "sor here",
		"last line",
	}

	pos := extractCursorPosition(lines, 10)
	require.NotNil(t, pos)
	assert.Equal(t, 1, pos.row)
	assert.Equal(t, 3, pos.col) // "cur" = 3 columns
	// Marker should be stripped.
	assert.Equal(t, "cursor here", lines[1])
}

func TestOverlayCompositing(t *testing.T) {
	term := newMockTerminal(20, 5)
	tui := New(term)
	bg := &staticComponent{lines: []string{
		strings.Repeat(".", 20),
		strings.Repeat(".", 20),
		strings.Repeat(".", 20),
		strings.Repeat(".", 20),
		strings.Repeat(".", 20),
	}}
	tui.AddChild(bg)
	tui.stopped = false

	overlay := &staticComponent{lines: []string{"OVERLAY"}}
	tui.ShowOverlay(overlay, &OverlayOptions{
		Width:  SizeAbs(10),
		Anchor: AnchorCenter,
	})

	renderSync(tui)

	// The overlay should be composited into the rendered output.
	// We can verify by checking previousLines.
	tui.mu.Lock()
	prev := tui.previousLines
	tui.mu.Unlock()

	found := false
	for _, line := range prev {
		if strings.Contains(line, "OVERLAY") {
			found = true
			break
		}
	}
	assert.True(t, found, "overlay content should appear in rendered output")
}

func TestVisibleWidth(t *testing.T) {
	assert.Equal(t, 5, VisibleWidth("hello"))
	assert.Equal(t, 5, VisibleWidth("\x1b[31mhello\x1b[0m"))
	assert.Equal(t, 0, VisibleWidth(""))
}

func TestSliceByColumn(t *testing.T) {
	// Plain text.
	assert.Equal(t, "ell", SliceByColumn("hello", 1, 3))
	assert.Equal(t, "hel", SliceByColumn("hello", 0, 3))

	// With ANSI codes.
	colored := "\x1b[31mhello\x1b[0m"
	slice := SliceByColumn(colored, 1, 3)
	stripped := strings.ReplaceAll(slice, "\x1b[31m", "")
	stripped = strings.ReplaceAll(stripped, "\x1b[0m", "")
	assert.Equal(t, "ell", stripped)
}

func TestLipglossCompositing(t *testing.T) {
	// Test that lipgloss Compositor correctly overlays content.
	base := strings.Repeat(".", 20)
	overlay := "HI"

	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(overlay).X(5).Y(0).Z(1),
	)
	result := comp.Render()
	stripped := stripANSI(result)
	// "HI" at column 5 overwrites two dots.
	assert.Equal(t, ".....HI.............", stripped)
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			_, n := parseEscape(s[i:])
			if n > 0 {
				i += n
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func TestForceRender(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)
	tui.AddChild(&staticComponent{lines: []string{"content"}})
	tui.stopped = false

	renderSync(tui)
	assert.Equal(t, 1, tui.FullRedraws())

	// Force re-render should do a full redraw even with no changes.
	tui.mu.Lock()
	tui.previousLines = nil
	tui.previousWidth = -1
	tui.cursorRow = 0
	tui.hardwareCursorRow = 0
	tui.maxLinesRendered = 0
	tui.previousViewportTop = 0
	tui.mu.Unlock()

	term.reset()
	renderSync(tui)
	assert.Equal(t, 2, tui.FullRedraws())
}
