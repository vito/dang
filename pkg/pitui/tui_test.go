package pitui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gotest.tools/v3/golden"
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

// staticComponent renders fixed lines. Always dirty (re-renders every frame).
type staticComponent struct {
	Compo
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
	}
}

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
	comp.Update()
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
	comp.Update()
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
	comp.Update()
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

	result := tui.Render(RenderContext{Width: 40})
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

	renderSync(tui)

	tui.mu.Lock()
	prev := tui.previousLines
	tui.mu.Unlock()

	require.True(t, len(prev) >= 3, "should have at least 3 lines")
	assert.Contains(t, prev[0], "MENU-A", "first menu line at content row 0")
	assert.Contains(t, prev[1], "MENU-B", "second menu line at content row 1")
	assert.Contains(t, prev[2], "line-2", "last content line untouched")
}

func TestCursorRelativeOverlayPreferAbove(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)

	// Base content with cursor on line 5 (enough room above for a 3-line menu).
	bg := &staticComponent{
		lines:  []string{"line-0", "line-1", "line-2", "line-3", "line-4", "input>"},
		cursor: &CursorPos{Row: 5, Col: 7},
	}
	tui.AddChild(bg)
	tui.stopped = false

	menu := &staticComponent{lines: []string{"MENU-A", "MENU-B", "MENU-C"}}
	tui.ShowOverlay(menu, &OverlayOptions{
		Width:          SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
	})

	renderSync(tui)

	tui.mu.Lock()
	prev := tui.previousLines
	tui.mu.Unlock()

	// Menu should be above cursor (rows 2-4), cursor is at row 5.
	require.True(t, len(prev) >= 6)
	assert.Contains(t, prev[2], "MENU-A")
	assert.Contains(t, prev[3], "MENU-B")
	assert.Contains(t, prev[4], "MENU-C")
	assert.Contains(t, prev[5], "input>") // cursor row untouched
}

func TestCursorRelativeOverlayFlipToBelow(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)

	// Cursor at row 1 — not enough room above for a 3-line menu.
	bg := &staticComponent{
		lines:  []string{"line-0", "input>", "line-2", "line-3"},
		cursor: &CursorPos{Row: 1, Col: 7},
	}
	tui.AddChild(bg)
	tui.stopped = false

	menu := &staticComponent{lines: []string{"MENU-A", "MENU-B", "MENU-C"}}
	tui.ShowOverlay(menu, &OverlayOptions{
		Width:          SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
	})

	renderSync(tui)

	tui.mu.Lock()
	prev := tui.previousLines
	tui.mu.Unlock()

	// Not enough room above (row 1 - 3 = -2), should flip to below cursor (row 2).
	require.True(t, len(prev) >= 5)
	assert.Contains(t, prev[1], "input>") // cursor row untouched
	assert.Contains(t, prev[2], "MENU-A")
	assert.Contains(t, prev[3], "MENU-B")
	assert.Contains(t, prev[4], "MENU-C")
}

func TestCursorRelativeOverlayOffsetX(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)

	bg := &staticComponent{
		lines:  []string{"aaaa", "bbbb", "cccc", "input>"},
		cursor: &CursorPos{Row: 3, Col: 10},
	}
	tui.AddChild(bg)
	tui.stopped = false

	menu := &staticComponent{lines: []string{"HI"}}
	tui.ShowOverlay(menu, &OverlayOptions{
		Width:          SizeAbs(5),
		CursorRelative: true,
		PreferAbove:    true,
		OffsetX:        -3, // 3 columns left of cursor
	})

	renderSync(tui)

	tui.mu.Lock()
	prev := tui.previousLines
	tui.mu.Unlock()

	// Menu at row 2 (above cursor row 3), col = 10 + (-3) = 7.
	require.True(t, len(prev) >= 4)
	stripped := stripANSI(prev[2])
	// "cccc" is 4 chars, then spaces to col 7, then "HI" padded to width 5.
	assert.True(t, len(stripped) >= 12, "overlay should be at col 7: %q", stripped)
	assert.Equal(t, "HI", strings.TrimSpace(stripped[7:12]))
}

func TestCursorRelativeOverlayMaxHeightNotClampedToContent(t *testing.T) {
	// Regression test: cursor-relative overlays should resolve MaxHeight
	// against the terminal height, not the content height. Previously,
	// a 6-line content area would clamp MaxHeight to 6 lines even though
	// the terminal had 24 rows.
	term := newMockTerminal(40, 24)
	tui := newTUI(term)

	// Only 3 lines of content, but the overlay needs more room.
	bg := &staticComponent{
		lines:  []string{"line-0", "line-1", "input>"},
		cursor: &CursorPos{Row: 2, Col: 7},
	}
	tui.AddChild(bg)
	tui.stopped = false

	// Overlay with 10 lines and MaxHeight of 12 (fits in terminal, doesn't
	// fit in content height of 3).
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, fmt.Sprintf("item-%d", i))
	}
	menu := &staticComponent{lines: lines}
	tui.ShowOverlay(menu, &OverlayOptions{
		Width:          SizeAbs(15),
		MaxHeight:      SizeAbs(12),
		CursorRelative: true,
		PreferAbove:    false,
	})

	renderSync(tui)

	tui.mu.Lock()
	prev := tui.previousLines
	tui.mu.Unlock()

	// All 10 items should be visible (below cursor at row 3).
	found := 0
	for _, line := range prev {
		for i := 0; i < 10; i++ {
			if strings.Contains(line, fmt.Sprintf("item-%d", i)) {
				found++
				break
			}
		}
	}
	assert.Equal(t, 10, found, "all 10 menu items should be visible, got %d in %d lines", found, len(prev))
}

func TestCursorRelativeOverlayCursorGroupBothFitAbove(t *testing.T) {
	term := newMockTerminal(60, 20)
	tui := newTUI(term)

	// 8 lines of content, cursor at row 7 — enough room above for both.
	var bgLines []string
	for i := 0; i < 7; i++ {
		bgLines = append(bgLines, fmt.Sprintf("line-%d", i))
	}
	bgLines = append(bgLines, "input>")
	bg := &staticComponent{
		lines:  bgLines,
		cursor: &CursorPos{Row: 7, Col: 7},
	}
	tui.AddChild(bg)
	tui.stopped = false

	group := NewCursorGroup()

	// Menu (5 lines) — fits above cursor (7 - 5 = 2 >= 0).
	menu := &staticComponent{lines: []string{"M-0", "M-1", "M-2", "M-3", "M-4"}}
	tui.ShowOverlay(menu, &OverlayOptions{
		Width:          SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
		CursorGroup:    group,
	})

	// Detail (2 lines) — also fits above.
	detail := &staticComponent{lines: []string{"D-0", "D-1"}}
	tui.ShowOverlay(detail, &OverlayOptions{
		Width:          SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
		OffsetX:        12,
		CursorGroup:    group,
	})

	renderSync(tui)

	tui.mu.Lock()
	prev := tui.previousLines
	tui.mu.Unlock()

	// Both should be above cursor (row 7).
	menuRow := -1
	detailRow := -1
	for i, line := range prev {
		if strings.Contains(line, "M-4") {
			menuRow = i // last menu line
		}
		if strings.Contains(line, "D-1") {
			detailRow = i // last detail line
		}
	}
	require.NotEqual(t, -1, menuRow, "menu should be visible")
	require.NotEqual(t, -1, detailRow, "detail should be visible")
	assert.True(t, menuRow < 7, "menu should be above cursor row 7")
	assert.True(t, detailRow < 7, "detail should be above cursor row 7")
}

func TestCursorRelativeOverlayCursorGroupFlipAll(t *testing.T) {
	// When one member of a CursorGroup doesn't fit above, all go below.
	term := newMockTerminal(60, 20)
	tui := newTUI(term)

	// 4 lines of content, cursor at row 3.
	// Menu (2 lines) fits above (3 - 2 = 1 >= 0).
	// Detail (5 lines) does NOT fit above (3 - 5 = -2 < 0).
	// Both should go below because they share a CursorGroup.
	bg := &staticComponent{
		lines:  []string{"line-0", "line-1", "line-2", "input>"},
		cursor: &CursorPos{Row: 3, Col: 7},
	}
	tui.AddChild(bg)
	tui.stopped = false

	group := NewCursorGroup()

	menu := &staticComponent{lines: []string{"M-0", "M-1"}}
	tui.ShowOverlay(menu, &OverlayOptions{
		Width:          SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
		CursorGroup:    group,
	})

	detail := &staticComponent{lines: []string{"D-0", "D-1", "D-2", "D-3", "D-4"}}
	tui.ShowOverlay(detail, &OverlayOptions{
		Width:          SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
		OffsetX:        12,
		CursorGroup:    group,
	})

	renderSync(tui)

	tui.mu.Lock()
	prev := tui.previousLines
	tui.mu.Unlock()

	menuRow := -1
	detailRow := -1
	for i, line := range prev {
		if strings.Contains(line, "M-0") {
			menuRow = i
		}
		if strings.Contains(line, "D-0") {
			detailRow = i
		}
	}
	require.NotEqual(t, -1, menuRow, "menu should be visible")
	require.NotEqual(t, -1, detailRow, "detail should be visible")
	assert.True(t, menuRow > 3, "menu should be below cursor (row 3), got row %d", menuRow)
	assert.True(t, detailRow > 3, "detail should be below cursor (row 3), got row %d", detailRow)
}

func TestCursorRelativeOverlayNoCursor(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)

	// Base content with NO cursor.
	bg := &staticComponent{lines: []string{"line-0", "line-1"}}
	tui.AddChild(bg)
	tui.stopped = false

	menu := &staticComponent{lines: []string{"MENU-A"}}
	tui.ShowOverlay(menu, &OverlayOptions{
		Width:          SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
	})

	renderSync(tui)

	tui.mu.Lock()
	prev := tui.previousLines
	tui.mu.Unlock()

	// No cursor — overlay should be skipped.
	for _, line := range prev {
		assert.NotContains(t, line, "MENU-A", "cursor-relative overlay should be hidden when no cursor")
	}
}

func TestOverlayDoesNotStealFocus(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)
	tui.stopped = false

	// Create a main component and give it focus.
	main := &staticComponent{lines: []string{"main"}}
	tui.AddChild(main)
	tui.SetFocus(main)

	// Show overlay — focus should remain on main.
	overlay := &staticComponent{lines: []string{"popup"}}
	tui.ShowOverlay(overlay, &OverlayOptions{
		Width:  SizeAbs(10),
		Anchor: AnchorCenter,
	})

	tui.mu.Lock()
	focused := tui.focusedComponent
	tui.mu.Unlock()
	assert.Equal(t, main, focused)
}

func TestSlotComponent(t *testing.T) {
	a := &staticComponent{lines: []string{"child-a"}}
	b := &staticComponent{lines: []string{"child-b-1", "child-b-2"}}

	slot := NewSlot(a)

	// Initial render — child has no cache yet, so it renders.
	r := renderComponent(slot, RenderContext{Width: 40})
	assert.Equal(t, []string{"child-a"}, r.Lines)

	// Second render — child is clean (nobody called Update), cached.
	r = renderComponent(slot, RenderContext{Width: 40})
	assert.Equal(t, []string{"child-a"}, r.Lines)

	// Swap child — Slot.Set marks dirty.
	slot.Set(b)
	r = renderComponent(slot, RenderContext{Width: 40})
	assert.Equal(t, []string{"child-b-1", "child-b-2"}, r.Lines)
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

func TestOverlayMaxHeightPassedToComponent(t *testing.T) {
	term := newMockTerminal(80, 24)
	tui := newTUI(term)
	bg := &staticComponent{lines: []string{
		"content 0", "content 1", "content 2", "content 3", "content 4",
		"content 5", "content 6", "content 7", "content 8", "content 9",
	}}
	tui.AddChild(bg)
	tui.stopped = false

	// Component that records the Height it received.
	var gotHeight int
	overlay := &callbackComponent{render: func(ctx RenderContext) RenderResult {
		gotHeight = ctx.Height
		lines := []string{"line 0", "line 1", "line 2", "line 3", "line 4"}
		return RenderResult{Lines: lines}
	}}
	tui.ShowOverlay(overlay, &OverlayOptions{
		Width:     SizeAbs(20),
		MaxHeight: SizeAbs(8),
		Anchor:    AnchorTopRight,
		Margin:    OverlayMargin{Top: 1, Right: 1},
	})

	renderSync(tui)

	assert.Equal(t, 8, gotHeight, "MaxHeight should be passed as ctx.Height")
}

// callbackComponent calls a render function.
type callbackComponent struct {
	Compo
	render func(RenderContext) RenderResult
}

func (c *callbackComponent) Render(ctx RenderContext) RenderResult {
	return c.render(ctx)
}

// borderedOverlay renders a lipgloss-bordered box that respects ctx.Height.
type borderedOverlay struct {
	Compo
	title string
	lines []string
}

func (b *borderedOverlay) Render(ctx RenderContext) RenderResult {
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63"))

	innerW := max(10, ctx.Width-2)

	inner := append([]string{b.title}, b.lines...)

	// Respect height budget: reserve 2 lines for top/bottom border.
	if ctx.Height > 0 && len(inner) > ctx.Height-2 {
		maxInner := ctx.Height - 2
		if maxInner > 1 {
			inner = inner[:maxInner-1]
			inner = append(inner, "...")
		} else if maxInner > 0 {
			inner = inner[:maxInner]
		}
	}

	box := borderStyle.Width(innerW).Render(strings.Join(inner, "\n"))
	return RenderResult{
		Lines: strings.Split(box, "\n"),
	}
}

// snapshotRenderedLines renders the TUI and returns the previousLines joined
// with newlines, with ANSI stripped. Each line is padded to terminal width
// using visible-width accounting (correct for multi-byte UTF-8).
func snapshotRenderedLines(tui *TUI, term *mockTerminal) string {
	renderSync(tui)

	tui.mu.Lock()
	prev := tui.previousLines
	tui.mu.Unlock()

	w := term.Columns()
	var sb strings.Builder
	for i, line := range prev {
		stripped := stripANSI(line)
		vw := VisibleWidth(stripped)
		if vw < w {
			stripped += strings.Repeat(" ", w-vw)
		} else if vw > w {
			stripped = Truncate(stripped, w, "")
		}
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(stripped)
	}
	sb.WriteByte('\n')
	return sb.String()
}

func TestOverlayBorderedBoxWithMaxHeight(t *testing.T) {
	term := newMockTerminal(60, 20)
	tui := newTUI(term)

	// Background content.
	var bgLines []string
	for i := 0; i < 10; i++ {
		bgLines = append(bgLines, fmt.Sprintf("content line %d", i))
	}
	tui.AddChild(&staticComponent{lines: bgLines})
	tui.stopped = false

	// Bordered overlay with more content than MaxHeight allows.
	var detailLines []string
	for i := 0; i < 20; i++ {
		detailLines = append(detailLines, fmt.Sprintf("detail %d", i))
	}
	overlay := &borderedOverlay{
		title: "MyFunction",
		lines: detailLines,
	}
	tui.ShowOverlay(overlay, &OverlayOptions{
		Width:     SizeAbs(30),
		MaxHeight: SizeAbs(12),
		Anchor:    AnchorTopRight,
		Margin:    OverlayMargin{Top: 1, Right: 1},
	})

	snap := snapshotRenderedLines(tui, term)
	golden.Assert(t, snap, "overlay_bordered_max_height.golden")
}

func TestOverlayBorderedBoxFitsNaturally(t *testing.T) {
	term := newMockTerminal(60, 20)
	tui := newTUI(term)

	var bgLines []string
	for i := 0; i < 10; i++ {
		bgLines = append(bgLines, fmt.Sprintf("content line %d", i))
	}
	tui.AddChild(&staticComponent{lines: bgLines})
	tui.stopped = false

	// Bordered overlay that fits within MaxHeight without truncation.
	overlay := &borderedOverlay{
		title: "SmallFunc",
		lines: []string{"returns String!", "", "A short description."},
	}
	tui.ShowOverlay(overlay, &OverlayOptions{
		Width:     SizeAbs(30),
		MaxHeight: SizeAbs(12),
		Anchor:    AnchorTopRight,
		Margin:    OverlayMargin{Top: 1, Right: 1},
	})

	snap := snapshotRenderedLines(tui, term)
	golden.Assert(t, snap, "overlay_bordered_fits.golden")
}

func TestOverlayLastLineNotDropped(t *testing.T) {
	// Regression test: the last line of an overlay was silently dropped
	// during compositing, causing bottom borders to disappear.
	term := newMockTerminal(60, 20)
	tui := newTUI(term)

	// Short background — fewer content lines than the terminal height.
	tui.AddChild(&staticComponent{lines: []string{
		"line 0", "line 1", "line 2",
	}})
	tui.stopped = false

	// Overlay that returns exactly 5 lines.
	overlay := &staticComponent{lines: []string{
		"TOP-BORDER",
		"content-a",
		"content-b",
		"content-c",
		"BOTTOM-BORDER",
	}}
	tui.ShowOverlay(overlay, &OverlayOptions{
		Width:   SizeAbs(20),
		Anchor:  AnchorTopRight,
		Margin:  OverlayMargin{Top: 1, Right: 1},
	})

	snap := snapshotRenderedLines(tui, term)
	golden.Assert(t, snap, "overlay_last_line.golden")
}

func TestOverlayLastLineWithScrolling(t *testing.T) {
	// Same test but with content that fills the terminal, forcing viewport
	// calculations.
	term := newMockTerminal(60, 12)
	tui := newTUI(term)

	var bgLines []string
	for i := 0; i < 15; i++ {
		bgLines = append(bgLines, fmt.Sprintf("bg line %d", i))
	}
	tui.AddChild(&staticComponent{lines: bgLines})
	tui.stopped = false

	overlay := &staticComponent{lines: []string{
		"TOP-BORDER",
		"content-a",
		"content-b",
		"content-c",
		"BOTTOM-BORDER",
	}}
	tui.ShowOverlay(overlay, &OverlayOptions{
		Width:   SizeAbs(20),
		Anchor:  AnchorTopRight,
		Margin:  OverlayMargin{Top: 1, Right: 1},
	})

	snap := snapshotRenderedLines(tui, term)
	golden.Assert(t, snap, "overlay_last_line_scrolling.golden")
}

func TestOverlayTwoOverlaysLastLine(t *testing.T) {
	// Two overlays simultaneously (like completion menu + detail bubble).
	term := newMockTerminal(60, 20)
	tui := newTUI(term)

	tui.AddChild(&staticComponent{lines: []string{
		"line 0", "line 1", "line 2", "line 3", "line 4",
	}})
	tui.stopped = false

	// Completion menu overlay (content-relative, above input).
	menu := &staticComponent{lines: []string{"menu-a", "menu-b", "menu-c"}}
	tui.ShowOverlay(menu, &OverlayOptions{
		Width:           SizeAbs(15),
		Anchor:          AnchorBottomLeft,
		ContentRelative: true,
		OffsetY:         -1,
	})

	// Detail bubble overlay (viewport-relative, top-right).
	detail := &staticComponent{lines: []string{
		"TOP-BORDER",
		"content-a",
		"content-b",
		"BOTTOM-BORDER",
	}}
	tui.ShowOverlay(detail, &OverlayOptions{
		Width:   SizeAbs(20),
		Anchor:  AnchorTopRight,
		Margin:  OverlayMargin{Top: 1, Right: 1},
	})

	snap := snapshotRenderedLines(tui, term)
	golden.Assert(t, snap, "overlay_two_overlays.golden")
}

func TestOverlayAtBottomOfViewport(t *testing.T) {
	// Overlay positioned near the bottom of the viewport when content
	// causes scrolling. The bottom border could get clipped if the overlay
	// extends past the working area.
	term := newMockTerminal(60, 10)
	tui := newTUI(term)

	// Content that exceeds terminal height.
	var bgLines []string
	for i := 0; i < 8; i++ {
		bgLines = append(bgLines, fmt.Sprintf("bg line %d", i))
	}
	tui.AddChild(&staticComponent{lines: bgLines})
	tui.stopped = false

	// Tall overlay anchored at the top — should extend most of the viewport.
	overlay := &staticComponent{lines: []string{
		"╭───────────────╮",
		"│ line 1        │",
		"│ line 2        │",
		"│ line 3        │",
		"│ line 4        │",
		"│ line 5        │",
		"│ line 6        │",
		"│ line 7        │",
		"╰───────────────╯",
	}}
	tui.ShowOverlay(overlay, &OverlayOptions{
		Width:   SizeAbs(18),
		Anchor:  AnchorTopRight,
		Margin:  OverlayMargin{Top: 0, Right: 1},
	})

	snap := snapshotRenderedLines(tui, term)
	golden.Assert(t, snap, "overlay_at_bottom_viewport.golden")
}

func TestOverlayTallerThanViewport(t *testing.T) {
	// Overlay is taller than the terminal. The overlay system must clamp
	// and the last visible line should still appear.
	term := newMockTerminal(60, 8)
	tui := newTUI(term)

	tui.AddChild(&staticComponent{lines: []string{
		"bg 0", "bg 1", "bg 2", "bg 3",
	}})
	tui.stopped = false

	// 12-line overlay in an 8-row terminal.
	overlay := &staticComponent{lines: []string{
		"╭───────────────╮",
		"│ line 1        │",
		"│ line 2        │",
		"│ line 3        │",
		"│ line 4        │",
		"│ line 5        │",
		"│ line 6        │",
		"│ line 7        │",
		"│ line 8        │",
		"│ line 9        │",
		"│ line 10       │",
		"╰───────────────╯",
	}}
	tui.ShowOverlay(overlay, &OverlayOptions{
		Width:   SizeAbs(18),
		Anchor:  AnchorTopLeft,
	})

	snap := snapshotRenderedLines(tui, term)
	golden.Assert(t, snap, "overlay_taller_than_viewport.golden")
}

func TestOverlayBorderedBoxWidthMismatch(t *testing.T) {
	// Reproduces the real detail bubble bug: content is prepared for
	// ctx.Width-2 columns, but lipgloss Width(n) means TOTAL width = n
	// (including borders), so the inner area is actually n-2. Long content
	// lines get wrapped by lipgloss, adding extra height, which causes
	// the overlay truncation to chop the bottom border.
	term := newMockTerminal(80, 24)
	tui := newTUI(term)

	var bgLines []string
	for i := 0; i < 15; i++ {
		bgLines = append(bgLines, fmt.Sprintf("content line %d", i))
	}
	tui.AddChild(&staticComponent{lines: bgLines})
	tui.stopped = false

	// This mimics the detailBubble.Render pattern. lipgloss Width(n) is the
	// TOTAL width including borders, so content must be wrapped to n-2.
	overlay := &callbackComponent{render: func(ctx RenderContext) RenderResult {
		borderStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63"))

		contentW := max(8, ctx.Width-2)

		var lines []string
		lines = append(lines, "Title")
		for i := 0; i < 8; i++ {
			// Use wordWrap-style content that fits contentW.
			line := fmt.Sprintf("detail line %d with extra text padding here", i)
			if len(line) > contentW {
				line = line[:contentW]
			}
			lines = append(lines, line)
		}

		// Truncate inner content to fit height budget (2 for borders).
		if ctx.Height > 0 && len(lines) > ctx.Height-2 {
			maxInner := ctx.Height - 2
			if maxInner > 1 {
				lines = lines[:maxInner-1]
				lines = append(lines, "...")
			}
		}

		inner := strings.Join(lines, "\n")
		box := borderStyle.Width(ctx.Width).Render(inner)
		return RenderResult{Lines: strings.Split(box, "\n")}
	}}

	tui.ShowOverlay(overlay, &OverlayOptions{
		Width:     SizeAbs(35),
		MaxHeight: SizeAbs(14),
		Anchor:    AnchorTopRight,
		Margin:    OverlayMargin{Top: 1, Right: 1},
	})

	snap := snapshotRenderedLines(tui, term)
	golden.Assert(t, snap, "overlay_bordered_width_mismatch.golden")
}

// compoComponent embeds Compo for automatic caching. Call Update() to
// mark dirty. Tracks render call count.
type compoComponent struct {
	Compo
	lines       []string
	renderCount int
}

func (c *compoComponent) Render(ctx RenderContext) RenderResult {
	c.renderCount++
	return RenderResult{Lines: c.lines}
}

func TestCompoSkipsRenderWhenClean(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)

	// Two children with Compo: one finalized, one changing.
	finalized := &compoComponent{lines: []string{"old line 1", "old line 2"}}
	finalized.Update() // dirty for first render
	active := &staticComponent{lines: []string{"input> "}}
	tui.AddChild(finalized)
	tui.AddChild(active)
	tui.stopped = false

	// First render — finalized is dirty, renders.
	renderSync(tui)
	assert.Equal(t, 1, finalized.renderCount)

	// Second render — finalized is clean (nobody called Update).
	// Render should be SKIPPED entirely (renderCount stays 1).
	term.reset()
	renderSync(tui)
	assert.Equal(t, 1, finalized.renderCount, "clean Compo should skip Render")

	// The output should still contain finalized's content (from cache).
	tui.mu.Lock()
	prev := tui.previousLines
	tui.mu.Unlock()
	assert.True(t, len(prev) >= 3)
	assert.Contains(t, stripANSI(prev[0]), "old line 1")
	assert.Contains(t, stripANSI(prev[1]), "old line 2")
	assert.Contains(t, stripANSI(prev[2]), "input> ")
}

func TestContainerDirtyPropagation(t *testing.T) {
	c := &Container{}
	c1 := &compoComponent{lines: []string{"a"}}
	c1.Update()
	c2 := &compoComponent{lines: []string{"b"}}
	c2.Update()
	c.AddChild(c1)
	c.AddChild(c2)

	// First render — children are dirty, both render.
	ctx := RenderContext{Width: 40}
	r := renderComponent(c, ctx)
	assert.Equal(t, []string{"a", "b"}, r.Lines)
	assert.Equal(t, 1, c1.renderCount)
	assert.Equal(t, 1, c2.renderCount)

	// Second render — children clean, Container cached at root level.
	r = renderComponent(c, ctx)
	assert.Equal(t, []string{"a", "b"}, r.Lines)
	assert.Equal(t, 1, c1.renderCount, "clean child should not re-render")
	assert.Equal(t, 1, c2.renderCount, "clean child should not re-render")

	// Mark one child dirty — Container re-renders.
	c1.Update()
	r = renderComponent(c, ctx)
	assert.Equal(t, []string{"a", "b"}, r.Lines)
	assert.Equal(t, 2, c1.renderCount, "dirty child should re-render")
	assert.Equal(t, 1, c2.renderCount, "clean child should still be cached")
}


func TestCompoCachedChildNoRepaint(t *testing.T) {
	// Verify that a cached Compo child's line range is not repainted
	// when only other children change.
	term := newMockTerminal(40, 10)
	tui := newTUI(term)

	clean := &compoComponent{lines: []string{"stable-1", "stable-2"}}
	clean.Update()
	changing := &compoComponent{lines: []string{"v1"}}
	changing.Update()
	tui.AddChild(clean)
	tui.AddChild(changing)
	tui.stopped = false

	// First render (full).
	renderSync(tui)

	// Now only changing child is updated.
	changing.lines = []string{"v2"}
	changing.Update()
	term.reset()
	renderSync(tui)

	out := term.written.String()
	// The changing child should be repainted.
	assert.Contains(t, out, "v2")
	// The clean child should NOT be repainted.
	assert.NotContains(t, out, "stable-1")
	assert.NotContains(t, out, "stable-2")
}

func TestUpdatePropagatesAndRequestsRender(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := newTUI(term)
	tui.stopped = false

	child := &compoComponent{lines: []string{"hello"}}
	child.Update()
	tui.AddChild(child)
	renderSync(tui)

	// Drain the render channel.
	select {
	case <-tui.renderCh:
	default:
	}

	// Update the child — should propagate and request render.
	child.lines = []string{"world"}
	child.Update()

	// The render channel should have a pending request.
	select {
	case <-tui.renderCh:
		// good
	default:
		t.Fatal("Update() should have triggered RequestRender via propagation")
	}
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

func TestFirstRenderClearsExistingContent(t *testing.T) {
	// Regression test: when a component is removed leaving 0 children,
	// the next render (treated as "first render") must clear each line
	// before writing so that leftover terminal content from the previous
	// render doesn't bleed through.
	term := newMockTerminal(80, 10)
	tui := newTUI(term)
	tui.stopped = false

	// First render with a long line.
	long := &staticComponent{lines: []string{"Loading Dagger module from /home/user/project..."}}
	long.Update()
	tui.AddChild(long)
	renderSync(tui)

	// Remove all children → empty render.
	tui.RemoveChild(long)
	renderSync(tui)

	// Add new content that is shorter than the old line.
	short := &staticComponent{lines: []string{"Welcome v0.1"}}
	short.Update()
	tui.AddChild(short)
	term.reset()
	renderSync(tui)

	out := term.written.String()
	// The render must include a line-clear escape before the new content
	// so the old longer text doesn't bleed through.
	assert.Contains(t, out, "\x1b[2K", "first render after empty should clear lines")
	assert.Contains(t, out, "Welcome v0.1")
}

func TestConcurrentUpdateNotLost(t *testing.T) {
	// Regression test: if Update() is called on a component while its
	// Render() is in progress (e.g. from a streaming goroutine),
	// the dirty flag must not be lost. Previously, renderComponent
	// used a boolean flag and called Store(false) AFTER Render(),
	// which could overwrite a concurrent Update()'s Store(true).
	// The generation counter approach eliminates this: renderComponent
	// records the generation it checked, and any concurrent Update()
	// increments past it.
	term := newMockTerminal(40, 10)
	tui := newTUI(term)
	tui.stopped = false

	// A component whose Render calls Update() on itself to simulate
	// a concurrent update during rendering. On first render it returns
	// the current (stale) value but marks itself dirty with a new value.
	sneaky := &updateDuringRenderComponent{value: "before"}
	sneaky.Update()
	tui.AddChild(sneaky)

	// First render: Render() returns "before", then sets value="after"
	// and calls Update(). The generation counter advances past what
	// renderComponent recorded, so the component stays dirty.
	renderSync(tui)

	// The component should still be dirty after the first render
	// because Update() was called during Render().
	cp := sneaky.compo()
	assert.NotEqual(t, cp.generation.Load(), cp.renderedGen,
		"Update() during Render() must not be lost")

	// Second render should pick up the new value.
	term.reset()
	renderSync(tui)
	out := term.written.String()
	assert.Contains(t, out, "after",
		"second render should reflect the update made during first render")
}

// updateDuringRenderComponent calls Update() on itself during Render
// to simulate a concurrent update from another goroutine. On first
// render it snapshots the current value, then mutates and calls Update().
type updateDuringRenderComponent struct {
	Compo
	value    string
	rendered bool
}

func (u *updateDuringRenderComponent) Render(ctx RenderContext) RenderResult {
	snapshot := u.value
	if !u.rendered {
		u.rendered = true
		u.value = "after"
		u.Update() // simulate concurrent update
	}
	return RenderResult{Lines: []string{snapshot}}
}

func TestSubwordLeft(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		cursor int
		want   int
	}{
		{"ident", "container", 9, 0},
		{"dot chain", "container.withExec", 18, 10},
		{"after dot", "container.", 10, 9},
		{"after paren", "container.withExec(", 19, 18},
		{"after brackets", `["echo"]`, 8, 6},
		{"after closing bracket", `["echo"]`, 7, 6},
		{"after space", "foo bar", 7, 4},
		{"multiple spaces", "foo   bar", 9, 6},
		{"symbols run", "foo..", 5, 3},
		{"empty", "", 0, 0},
		{"at start", "hello", 0, 0},
		{"mixed", "a.b(c)", 6, 5},
		{"mixed mid", "a.b(c)", 5, 4},
		{"mixed paren", "a.b(c)", 4, 3},
		{"mixed dot", "a.b(c)", 2, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ti := NewTextInput("> ")
			ti.value = []rune(tt.input)
			ti.cursor = tt.cursor
			got := ti.subwordLeft()
			assert.Equal(t, tt.want, got, "subwordLeft(%q, cursor=%d)", tt.input, tt.cursor)
		})
	}
}

func TestCachedLinesNotMutatedBySegmentReset(t *testing.T) {
	// Regression test: doRender appends segmentReset to each line.
	// If it mutates the cached RenderResult's Lines slice, subsequent
	// frames see double-reset strings that never match, causing
	// spurious full redraws.
	term := newMockTerminal(40, 10)
	tui := newTUI(term)

	cached := &compoComponent{lines: []string{"stable"}}
	cached.Update()
	changing := &compoComponent{lines: []string{"v1"}}
	changing.Update()
	tui.AddChild(cached)
	tui.AddChild(changing)
	tui.stopped = false

	// First render.
	renderSync(tui)
	assert.Equal(t, 1, tui.FullRedraws())

	// Change only the second component. The first is cached.
	changing.lines = []string{"v2"}
	changing.Update()
	term.reset()
	renderSync(tui)

	// Should NOT be a full redraw — cached component's line 0
	// should be identical across frames.
	assert.Equal(t, 1, tui.FullRedraws(), "cached line should not accumulate segmentReset")
	out := term.written.String()
	assert.Contains(t, out, "v2")
	assert.NotContains(t, out, "stable") // cached line not repainted

	// Third render — same pattern, still no full redraw.
	changing.lines = []string{"v3"}
	changing.Update()
	term.reset()
	renderSync(tui)
	assert.Equal(t, 1, tui.FullRedraws(), "still no full redraw on third frame")
}

func TestHasKittyKeyboard(t *testing.T) {
	term := newMockTerminal(80, 24)
	tui := newTUI(term)
	tui.stopped = false

	// Before any response, HasKittyKeyboard is false.
	assert.False(t, tui.HasKittyKeyboard())

	// Simulate a KeyboardEnhancementsEvent with disambiguate flag.
	tui.dispatchEvent(uv.KeyboardEnhancementsEvent{Flags: 1}) // KittyDisambiguateEscapeCodes = 1
	assert.True(t, tui.HasKittyKeyboard())

	// Zero flags means no support.
	tui.dispatchEvent(uv.KeyboardEnhancementsEvent{Flags: 0})
	assert.False(t, tui.HasKittyKeyboard())
}
