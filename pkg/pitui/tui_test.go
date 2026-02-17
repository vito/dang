package pitui

import (
	"strings"
	"testing"

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
func (m *mockTerminal) Stop()                {}
func (m *mockTerminal) Write(p []byte)       { m.written.Write(p) }
func (m *mockTerminal) WriteString(s string) { m.written.WriteString(s) }
func (m *mockTerminal) Columns() int         { return m.cols }
func (m *mockTerminal) Rows() int            { return m.rows }
func (m *mockTerminal) HideCursor()          { m.written.WriteString("\x1b[?25l") }
func (m *mockTerminal) ShowCursor()          { m.written.WriteString("\x1b[?25h") }

func (m *mockTerminal) reset() { m.written.Reset() }

// staticComponent renders fixed lines.
type staticComponent struct {
	lines  []string
	cursor *CursorPos
}

func (s *staticComponent) Render(ctx RenderContext) RenderResult {
	out := make([]string, len(s.lines))
	for i, l := range s.lines {
		if VisibleWidth(l) > ctx.Width {
			out[i] = Truncate(l, ctx.Width, "")
		} else {
			out[i] = l
		}
	}
	return RenderResult{
		Lines:  out,
		Cursor: s.cursor,
		Dirty:  true,
	}
}
func (s *staticComponent) Invalidate() {}

// renderSync calls doRender directly. Tests use newTUI (no renderLoop),
// so there's no concurrency to worry about.
func renderSync(t *TUI) {
	t.doRender()
}

func TestFirstRender(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)
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
	tui := newTUI(term)
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
	tui := newTUI(term)
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
	assert.NotContains(t, out, "\x1b[2Ka"+segmentReset) // not rewritten
}

func TestWidthChangeTriggersFullRedraw(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)
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
	tui := newTUI(term)

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
	tui := newTUI(term)
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

func TestStructuralCursorPosition(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)
	tui.showHardwareCursor = true

	comp := &staticComponent{
		lines:  []string{"first line", "cursor here", "last line"},
		cursor: &CursorPos{Row: 1, Col: 3},
	}
	tui.AddChild(comp)
	tui.stopped = false

	renderSync(tui)

	// Verify cursor was positioned (row 1, col 3).
	// The hardware cursor should be at row 1.
	tui.mu.Lock()
	hcr := tui.hardwareCursorRow
	tui.mu.Unlock()
	assert.Equal(t, 1, hcr)
}

func TestContainerPropagatesCursor(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)

	// First child: 2 lines, no cursor.
	c1 := &staticComponent{lines: []string{"a", "b"}}
	// Second child: 1 line, cursor at (0, 5).
	c2 := &staticComponent{
		lines:  []string{"hello world"},
		cursor: &CursorPos{Row: 0, Col: 5},
	}
	tui.AddChild(c1)
	tui.AddChild(c2)
	tui.stopped = false

	result := tui.Container.Render(RenderContext{Width: 40})
	require.NotNil(t, result.Cursor)
	// c2's row 0 is line 2 in the assembled output (after c1's 2 lines).
	assert.Equal(t, 2, result.Cursor.Row)
	assert.Equal(t, 5, result.Cursor.Col)
}

func TestOverlayCompositing(t *testing.T) {
	term := newMockTerminal(20, 5)
	tui := newTUI(term)
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

func TestContentRelativeOverlay(t *testing.T) {
	term := newMockTerminal(30, 10)
	tui := newTUI(term)
	bg := &staticComponent{lines: []string{
		"line-0",
		"line-1",
		"line-2",
	}}
	tui.AddChild(bg)
	tui.stopped = false

	menu := &staticComponent{lines: []string{"MENU-A", "MENU-B"}}
	tui.ShowOverlay(menu, &OverlayOptions{
		Width:           SizeAbs(10),
		Anchor:          AnchorBottomLeft,
		ContentRelative: true,
		OffsetY:         -1, // above the last content line
	})
	// Don't let it steal focus for this test.
	tui.SetFocus(nil)

	renderSync(tui)

	tui.mu.Lock()
	prev := tui.previousLines
	tui.mu.Unlock()

	require.True(t, len(prev) >= 3, "should have at least 3 lines")
	assert.Contains(t, prev[0], "MENU-A", "first menu line at content row 0")
	assert.Contains(t, prev[1], "MENU-B", "second menu line at content row 1")
	assert.Contains(t, prev[2], "line-2", "last content line untouched")
}

func TestNoFocusOverlay(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)
	tui.stopped = false

	// Create a main component and give it focus.
	main := &staticComponent{lines: []string{"main"}}
	tui.AddChild(main)
	tui.SetFocus(main)

	// Show overlay with NoFocus.
	overlay := &staticComponent{lines: []string{"popup"}}
	tui.ShowOverlay(overlay, &OverlayOptions{
		Width:   SizeAbs(10),
		Anchor:  AnchorCenter,
		NoFocus: true,
	})

	// Focus should remain on main.
	tui.mu.Lock()
	focused := tui.focusedComponent
	tui.mu.Unlock()
	assert.Equal(t, main, focused)
}

func TestSlotComponent(t *testing.T) {
	a := &staticComponent{lines: []string{"child-a"}}
	b := &staticComponent{lines: []string{"child-b-1", "child-b-2"}}

	slot := NewSlot(a)

	// Initial render.
	r := slot.Render(RenderContext{Width: 40})
	assert.Equal(t, []string{"child-a"}, r.Lines)
	assert.True(t, r.Dirty)

	// Second render, slot not dirty, child always dirty.
	r = slot.Render(RenderContext{Width: 40})
	assert.Equal(t, []string{"child-a"}, r.Lines)
	assert.True(t, r.Dirty) // child is always dirty

	// Swap child.
	slot.Set(b)
	r = slot.Render(RenderContext{Width: 40})
	assert.Equal(t, []string{"child-b-1", "child-b-2"}, r.Lines)
	assert.True(t, r.Dirty)
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

func TestCompositeLineAt(t *testing.T) {
	base := strings.Repeat(".", 20)
	result := CompositeLineAt(base, "HI", 5, 4, 20)
	w := VisibleWidth(result)
	assert.Equal(t, 20, w, "composited line should be exactly terminal width")
	stripped := stripANSI(result)
	assert.Equal(t, ".....HI  ...........", stripped)
}

func TestCompositeLineAtPreservesSpaces(t *testing.T) {
	base := "hello    world      end"
	result := CompositeLineAt(base, "XX", 9, 2, 30)
	stripped := stripANSI(result)
	assert.Equal(t, "hello    XXrld      end       ", stripped)
	assert.Equal(t, 30, VisibleWidth(result))
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

func TestExpandTabs(t *testing.T) {
	assert.Equal(t, "ok      github.com/foo", ExpandTabs("ok\tgithub.com/foo", 8))
	assert.Equal(t, "a       b", ExpandTabs("a\tb", 8))
	assert.Equal(t, "abcd    e", ExpandTabs("abcd\te", 8))
	assert.Equal(t, "abcdefgh        x", ExpandTabs("abcdefgh\tx", 8))
	assert.Equal(t, "hello world", ExpandTabs("hello world", 8))
}

func TestCompositeWithTabExpandedLine(t *testing.T) {
	base := ExpandTabs("ok\tgithub.com/foo/bar/baz  3.682s", 8)
	result := CompositeLineAt(base, "XY", 10, 4, 40)
	stripped := stripANSI(result)
	assert.Contains(t, stripped, "ok      gi")
	assert.Contains(t, stripped, "XY")
	assert.Equal(t, 40, VisibleWidth(result))
}

func TestForceRender(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)
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
