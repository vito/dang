package pitui

import (
	"fmt"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
)

// InputListenerResult controls how input propagates through listeners.
type InputListenerResult struct {
	// Consume stops propagation entirely.
	Consume bool
	// Data replaces the input data for downstream listeners. Nil means keep
	// the original data.
	Data []byte
}

// InputListener is called before input reaches the focused component.
// Return nil to pass through unchanged.
type InputListener func(data []byte) *InputListenerResult

type inputListenerEntry struct {
	fn  InputListener
	tok any // unique token for removal
}

// TUI is the main renderer. It extends Container with differential rendering
// on the normal scrollback buffer.
type TUI struct {
	Container

	terminal Terminal

	mu sync.Mutex // protects all mutable state below

	previousLines    []string
	previousWidth    int
	focusedComponent Component
	inputListeners   []inputListenerEntry

	renderRequested    bool
	cursorRow          int
	hardwareCursorRow  int
	showHardwareCursor bool
	clearOnShrink      bool
	maxLinesRendered   int
	previousViewportTop int
	fullRedrawCount    int
	stopped            bool

	overlayStack []*overlayEntry
}

// New creates a TUI backed by the given terminal.
func New(term Terminal) *TUI {
	return &TUI{
		terminal: term,
	}
}

// Terminal returns the underlying terminal.
func (t *TUI) Terminal() Terminal { return t.terminal }

// FullRedraws returns the number of full (non-differential) redraws performed.
func (t *TUI) FullRedraws() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fullRedrawCount
}

// SetShowHardwareCursor enables or disables the hardware cursor (for IME).
func (t *TUI) SetShowHardwareCursor(enabled bool) {
	t.mu.Lock()
	if t.showHardwareCursor == enabled {
		t.mu.Unlock()
		return
	}
	t.showHardwareCursor = enabled
	t.mu.Unlock()
	if !enabled {
		t.terminal.HideCursor()
	}
	t.RequestRender(false)
}

// SetClearOnShrink controls whether empty rows are cleared when content
// shrinks. When false (the default), stale rows remain until overwritten,
// which reduces full redraws on slower terminals.
func (t *TUI) SetClearOnShrink(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.clearOnShrink = enabled
}

// SetFocus gives keyboard focus to the given component (or nil).
func (t *TUI) SetFocus(comp Component) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setFocusLocked(comp)
}

func (t *TUI) setFocusLocked(comp Component) {
	if f, ok := t.focusedComponent.(Focusable); ok {
		f.SetFocused(false)
	}
	t.focusedComponent = comp
	if f, ok := comp.(Focusable); ok {
		f.SetFocused(true)
	}
}

// AddInputListener registers a listener that intercepts input before it
// reaches the focused component. Returns a function that removes it.
func (t *TUI) AddInputListener(l InputListener) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Use a unique token to identify this listener for removal.
	type token struct{}
	tok := &token{}
	t.inputListeners = append(t.inputListeners, inputListenerEntry{fn: l, tok: tok})
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		for i, entry := range t.inputListeners {
			if entry.tok == tok {
				t.inputListeners = append(t.inputListeners[:i], t.inputListeners[i+1:]...)
				return
			}
		}
	}
}

// ShowOverlay displays a component as a modal overlay on top of the base
// content.
func (t *TUI) ShowOverlay(comp Component, opts *OverlayOptions) *OverlayHandle {
	t.mu.Lock()
	entry := &overlayEntry{
		component: comp,
		options:   opts,
		preFocus:  t.focusedComponent,
	}
	t.overlayStack = append(t.overlayStack, entry)
	if t.isOverlayVisible(entry) {
		t.setFocusLocked(comp)
	}
	t.mu.Unlock()
	t.terminal.HideCursor()
	t.RequestRender(false)
	return &OverlayHandle{tui: t, entry: entry}
}

// HideOverlay removes the topmost overlay and restores previous focus.
func (t *TUI) HideOverlay() {
	t.mu.Lock()
	if len(t.overlayStack) == 0 {
		t.mu.Unlock()
		return
	}
	entry := t.overlayStack[len(t.overlayStack)-1]
	t.overlayStack = t.overlayStack[:len(t.overlayStack)-1]
	top := t.topmostVisibleOverlay()
	if top != nil {
		t.setFocusLocked(top.component)
	} else {
		t.setFocusLocked(entry.preFocus)
	}
	noOverlays := len(t.overlayStack) == 0
	t.mu.Unlock()
	if noOverlays {
		t.terminal.HideCursor()
	}
	t.RequestRender(false)
}

// HasOverlay reports whether any overlay is currently visible.
func (t *TUI) HasOverlay() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, o := range t.overlayStack {
		if t.isOverlayVisible(o) {
			return true
		}
	}
	return false
}

// Start begins the TUI event loop.
func (t *TUI) Start() error {
	t.mu.Lock()
	t.stopped = false
	t.mu.Unlock()
	err := t.terminal.Start(
		func(data []byte) { t.handleInput(data) },
		func() { t.RequestRender(false) },
	)
	if err != nil {
		return err
	}
	t.terminal.HideCursor()
	t.RequestRender(false)
	return nil
}

// Stop ends the TUI event loop and restores the terminal.
func (t *TUI) Stop() {
	t.mu.Lock()
	t.stopped = true
	prev := t.previousLines
	hcr := t.hardwareCursorRow
	t.mu.Unlock()

	// Move cursor past content so the shell prompt appears below.
	if len(prev) > 0 {
		target := len(prev)
		diff := target - hcr
		if diff > 0 {
			t.terminal.WriteString(fmt.Sprintf("\x1b[%dB", diff))
		} else if diff < 0 {
			t.terminal.WriteString(fmt.Sprintf("\x1b[%dA", -diff))
		}
		t.terminal.WriteString("\r\n")
	}

	t.terminal.ShowCursor()
	t.terminal.Stop()
}

// RequestRender schedules a render on the next iteration. If force is true,
// all cached state is discarded and a full repaint occurs.
func (t *TUI) RequestRender(force bool) {
	t.mu.Lock()
	if force {
		t.previousLines = nil
		t.previousWidth = -1
		t.cursorRow = 0
		t.hardwareCursorRow = 0
		t.maxLinesRendered = 0
		t.previousViewportTop = 0
	}
	if t.renderRequested {
		t.mu.Unlock()
		return
	}
	t.renderRequested = true
	t.mu.Unlock()

	// Render synchronously. In a real application you may want to coalesce
	// via a channel or timer; for now this matches the TS version's
	// process.nextTick behaviour closely enough.
	go func() {
		t.mu.Lock()
		t.renderRequested = false
		t.mu.Unlock()
		t.doRender()
	}()
}

// Invalidate clears cached rendering state of all components (including
// overlays).
func (t *TUI) Invalidate() {
	t.Container.Invalidate()
	t.mu.Lock()
	for _, o := range t.overlayStack {
		o.component.Invalidate()
	}
	t.mu.Unlock()
}

// ---------- input handling --------------------------------------------------

func (t *TUI) handleInput(data []byte) {
	t.mu.Lock()
	listeners := make([]inputListenerEntry, len(t.inputListeners))
	copy(listeners, t.inputListeners)
	t.mu.Unlock()

	current := data
	for _, entry := range listeners {
		r := entry.fn(current)
		if r != nil {
			if r.Consume {
				return
			}
			if r.Data != nil {
				current = r.Data
			}
		}
	}
	if len(current) == 0 {
		return
	}

	t.mu.Lock()
	comp := t.focusedComponent
	t.mu.Unlock()

	if ic, ok := comp.(Interactive); ok {
		ic.HandleInput(current)
		t.RequestRender(false)
	}
}

// ---------- differential rendering ------------------------------------------

func (t *TUI) doRender() {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	width := t.terminal.Columns()
	height := t.terminal.Rows()
	prevLines := t.previousLines
	prevWidth := t.previousWidth
	hardwareCursorRow := t.hardwareCursorRow
	maxLinesRendered := t.maxLinesRendered
	prevViewportTop := t.previousViewportTop
	clearOnShrink := t.clearOnShrink
	overlays := make([]*overlayEntry, len(t.overlayStack))
	copy(overlays, t.overlayStack)
	t.mu.Unlock()

	// Render all components.
	newLines := t.Container.Render(width)

	// Composite overlays.
	if len(overlays) > 0 {
		newLines = t.compositeOverlays(newLines, overlays, width, height, maxLinesRendered)
	}

	// Extract cursor position before adding resets.
	cursorPos := extractCursorPosition(newLines, height)

	// Append reset to each line.
	for i := range newLines {
		newLines[i] += segmentReset
	}

	viewportTop := max(0, maxLinesRendered-height)

	// Helper: line diff from hardware cursor to a target row, accounting for
	// viewport scrolling.
	computeLineDiff := func(targetRow int) int {
		currentScreen := hardwareCursorRow - prevViewportTop
		targetScreen := targetRow - viewportTop
		return targetScreen - currentScreen
	}

	widthChanged := prevWidth != 0 && prevWidth != width

	// --- full render helper ---
	fullRender := func(clear bool) {
		t.mu.Lock()
		t.fullRedrawCount++
		t.mu.Unlock()

		var buf strings.Builder
		buf.WriteString("\x1b[?2026h") // begin synchronized output
		if clear {
			buf.WriteString("\x1b[3J\x1b[2J\x1b[H") // clear scrollback, screen, home
		}
		for i, line := range newLines {
			if i > 0 {
				buf.WriteString("\r\n")
			}
			buf.WriteString(line)
		}
		buf.WriteString("\x1b[?2026l") // end synchronized output
		t.terminal.WriteString(buf.String())

		cr := max(0, len(newLines)-1)
		ml := maxLinesRendered
		if clear {
			ml = len(newLines)
		} else {
			ml = max(ml, len(newLines))
		}
		vt := max(0, ml-height)

		t.mu.Lock()
		t.cursorRow = cr
		t.hardwareCursorRow = cr
		t.maxLinesRendered = ml
		t.previousViewportTop = vt
		t.mu.Unlock()

		t.positionHardwareCursor(cursorPos, len(newLines))

		t.mu.Lock()
		t.previousLines = newLines
		t.previousWidth = width
		t.mu.Unlock()
	}

	// First render.
	if len(prevLines) == 0 && !widthChanged {
		fullRender(false)
		return
	}

	// Width changed.
	if widthChanged {
		fullRender(true)
		return
	}

	// Content shrunk below working area (no overlays).
	if clearOnShrink && len(newLines) < maxLinesRendered && len(overlays) == 0 {
		fullRender(true)
		return
	}

	// --- diff ---
	firstChanged := -1
	lastChanged := -1
	maxLen := max(len(newLines), len(prevLines))
	for i := 0; i < maxLen; i++ {
		oldLine := ""
		if i < len(prevLines) {
			oldLine = prevLines[i]
		}
		newLine := ""
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}

	appendedLines := len(newLines) > len(prevLines)
	if appendedLines {
		if firstChanged == -1 {
			firstChanged = len(prevLines)
		}
		lastChanged = len(newLines) - 1
	}
	appendStart := appendedLines && firstChanged == len(prevLines) && firstChanged > 0

	// No changes.
	if firstChanged == -1 {
		t.positionHardwareCursor(cursorPos, len(newLines))
		t.mu.Lock()
		t.previousViewportTop = max(0, t.maxLinesRendered-height)
		t.mu.Unlock()
		return
	}

	// All changes in deleted tail.
	if firstChanged >= len(newLines) {
		if len(prevLines) > len(newLines) {
			var buf strings.Builder
			buf.WriteString("\x1b[?2026h")
			targetRow := max(0, len(newLines)-1)
			diff := computeLineDiff(targetRow)
			if diff > 0 {
				fmt.Fprintf(&buf, "\x1b[%dB", diff)
			} else if diff < 0 {
				fmt.Fprintf(&buf, "\x1b[%dA", -diff)
			}
			buf.WriteString("\r")
			extra := len(prevLines) - len(newLines)
			if extra > height {
				fullRender(true)
				return
			}
			if extra > 0 {
				buf.WriteString("\x1b[1B")
			}
			for i := 0; i < extra; i++ {
				buf.WriteString("\r\x1b[2K")
				if i < extra-1 {
					buf.WriteString("\x1b[1B")
				}
			}
			if extra > 0 {
				fmt.Fprintf(&buf, "\x1b[%dA", extra)
			}
			buf.WriteString("\x1b[?2026l")
			t.terminal.WriteString(buf.String())

			t.mu.Lock()
			t.cursorRow = targetRow
			t.hardwareCursorRow = targetRow
			t.mu.Unlock()
		}
		t.positionHardwareCursor(cursorPos, len(newLines))
		t.mu.Lock()
		t.previousLines = newLines
		t.previousWidth = width
		t.previousViewportTop = max(0, t.maxLinesRendered-height)
		t.mu.Unlock()
		return
	}

	// First change above previous viewport -> full redraw.
	prevContentViewportTop := max(0, len(prevLines)-height)
	if firstChanged < prevContentViewportTop {
		fullRender(true)
		return
	}

	// --- differential update ---
	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")

	prevViewportBottom := prevViewportTop + height - 1
	moveTargetRow := firstChanged
	if appendStart {
		moveTargetRow = firstChanged - 1
	}

	if moveTargetRow > prevViewportBottom {
		currentScreen := max(0, min(height-1, hardwareCursorRow-prevViewportTop))
		moveToBottom := height - 1 - currentScreen
		if moveToBottom > 0 {
			fmt.Fprintf(&buf, "\x1b[%dB", moveToBottom)
		}
		scroll := moveTargetRow - prevViewportBottom
		for i := 0; i < scroll; i++ {
			buf.WriteString("\r\n")
		}
		prevViewportTop += scroll
		viewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}

	diff := computeLineDiff(moveTargetRow)
	if diff > 0 {
		fmt.Fprintf(&buf, "\x1b[%dB", diff)
	} else if diff < 0 {
		fmt.Fprintf(&buf, "\x1b[%dA", -diff)
	}

	if appendStart {
		buf.WriteString("\r\n")
	} else {
		buf.WriteString("\r")
	}

	renderEnd := min(lastChanged, len(newLines)-1)
	for i := firstChanged; i <= renderEnd; i++ {
		if i > firstChanged {
			buf.WriteString("\r\n")
		}
		buf.WriteString("\x1b[2K")
		buf.WriteString(newLines[i])
	}

	finalCursorRow := renderEnd

	// Clear deleted trailing lines.
	if len(prevLines) > len(newLines) {
		if renderEnd < len(newLines)-1 {
			moveDown := len(newLines) - 1 - renderEnd
			fmt.Fprintf(&buf, "\x1b[%dB", moveDown)
			finalCursorRow = len(newLines) - 1
		}
		extra := len(prevLines) - len(newLines)
		for i := 0; i < extra; i++ {
			buf.WriteString("\r\n\x1b[2K")
		}
		fmt.Fprintf(&buf, "\x1b[%dA", extra)
	}

	buf.WriteString("\x1b[?2026l")
	t.terminal.WriteString(buf.String())

	cr := max(0, len(newLines)-1)
	ml := max(maxLinesRendered, len(newLines))

	t.mu.Lock()
	t.cursorRow = cr
	t.hardwareCursorRow = finalCursorRow
	t.maxLinesRendered = ml
	t.previousViewportTop = max(0, ml-height)
	t.mu.Unlock()

	t.positionHardwareCursor(cursorPos, len(newLines))

	t.mu.Lock()
	t.previousLines = newLines
	t.previousWidth = width
	t.mu.Unlock()
}

// ---------- overlay compositing ---------------------------------------------

func (t *TUI) compositeOverlays(lines []string, overlays []*overlayEntry, termW, termH, maxLinesRendered int) []string {
	result := make([]string, len(lines))
	copy(result, lines)

	type rendered struct {
		content string // joined overlay lines
		row     int
		col     int
	}
	var items []rendered
	minNeeded := len(result)

	for _, e := range overlays {
		if !t.isOverlayVisible(e) {
			continue
		}
		w, _, _, _, _ := t.resolveOverlayLayout(e.options, 0, termW, termH)
		oLines := e.component.Render(w)
		_, _, _, maxH, maxHSet := t.resolveOverlayLayout(e.options, len(oLines), termW, termH)
		if maxHSet && len(oLines) > maxH {
			oLines = oLines[:maxH]
		}
		_, row, col, _, _ := t.resolveOverlayLayout(e.options, len(oLines), termW, termH)
		items = append(items, rendered{
			content: strings.Join(oLines, "\n"),
			row:     row,
			col:     col,
		})
		if row+len(oLines) > minNeeded {
			minNeeded = row + len(oLines)
		}
	}

	workingH := max(maxLinesRendered, minNeeded)
	for len(result) < workingH {
		result = append(result, "")
	}

	viewportStart := max(0, workingH-termH)

	// Use lipgloss Compositor for layer-based compositing.
	baseContent := strings.Join(result, "\n")
	baseLyr := lipgloss.NewLayer(baseContent)

	var overlayLyrs []*lipgloss.Layer
	for i, item := range items {
		lyr := lipgloss.NewLayer(item.content).
			X(item.col).
			Y(viewportStart + item.row).
			Z(i + 1)
		overlayLyrs = append(overlayLyrs, lyr)
	}

	allLayers := append([]*lipgloss.Layer{baseLyr}, overlayLyrs...)
	comp := lipgloss.NewCompositor(allLayers...)
	composited := comp.Render()

	// Split back into lines.
	outLines := strings.Split(composited, "\n")

	// Width guard: truncate any line that exceeds terminal width.
	for i, line := range outLines {
		if VisibleWidth(line) > termW {
			outLines[i] = Truncate(line, termW, "")
		}
	}

	return outLines
}


// ---------- cursor ----------------------------------------------------------

type cursorPosition struct {
	row, col int
}

func extractCursorPosition(lines []string, height int) *cursorPosition {
	viewportTop := max(0, len(lines)-height)
	for row := len(lines) - 1; row >= viewportTop; row-- {
		idx := strings.Index(lines[row], CursorMarker)
		if idx < 0 {
			continue
		}
		before := lines[row][:idx]
		col := VisibleWidth(before)
		// Strip marker.
		lines[row] = lines[row][:idx] + lines[row][idx+len(CursorMarker):]
		return &cursorPosition{row: row, col: col}
	}
	return nil
}

func (t *TUI) positionHardwareCursor(pos *cursorPosition, totalLines int) {
	if pos == nil || totalLines <= 0 {
		t.terminal.HideCursor()
		return
	}

	targetRow := clamp(pos.row, 0, totalLines-1)
	targetCol := max(0, pos.col)

	t.mu.Lock()
	hcr := t.hardwareCursorRow
	show := t.showHardwareCursor
	t.mu.Unlock()

	rowDelta := targetRow - hcr
	var buf strings.Builder
	if rowDelta > 0 {
		fmt.Fprintf(&buf, "\x1b[%dB", rowDelta)
	} else if rowDelta < 0 {
		fmt.Fprintf(&buf, "\x1b[%dA", -rowDelta)
	}
	fmt.Fprintf(&buf, "\x1b[%dG", targetCol+1)

	if buf.Len() > 0 {
		t.terminal.WriteString(buf.String())
	}

	t.mu.Lock()
	t.hardwareCursorRow = targetRow
	t.mu.Unlock()

	if show {
		t.terminal.ShowCursor()
	} else {
		t.terminal.HideCursor()
	}
}

// ---------- helpers ---------------------------------------------------------

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
