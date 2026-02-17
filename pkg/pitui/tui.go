package pitui

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
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

// RenderStats captures performance metrics for a single render cycle.
type RenderStats struct {
	// RenderTime is how long it took to generate the rendered content
	// (calling Component.Render on the tree, excluding overlay compositing).
	RenderTime time.Duration

	// CompositeTime is how long overlay compositing took (column-level
	// string surgery). Zero when there are no overlays.
	CompositeTime time.Duration

	// DiffTime is how long the differential update computation took.
	DiffTime time.Duration

	// WriteTime is how long it took to write the escape sequences to
	// the terminal.
	WriteTime time.Duration

	// TotalTime is the wall-clock duration of the entire doRender call.
	TotalTime time.Duration

	// TotalLines is the total number of lines in the rendered output.
	TotalLines int

	// LinesRepainted is the number of lines that were actually written
	// to the terminal (changed lines).
	LinesRepainted int

	// CacheHits is the number of lines that matched the previous frame
	// and were skipped.
	CacheHits int

	// FullRedraw is true when the entire screen was repainted (no diff).
	FullRedraw bool

	// OverlayCount is the number of active overlays composited.
	OverlayCount int

	// BytesWritten is the number of bytes sent to the terminal (escape
	// sequences + content). Large values indicate potential slowness over
	// SSH or on slow terminals.
	BytesWritten int

	// ComponentDirty reports whether the component tree signaled that its
	// content changed (via the Dirty flag). When false but lines still
	// differ, something bypassed the dirty tracking.
	ComponentDirty bool

	// FirstChangedLine is the first line index that differed from the
	// previous frame, or -1 if nothing changed.
	FirstChangedLine int

	// LastChangedLine is the last line index that differed from the
	// previous frame, or -1 if nothing changed.
	LastChangedLine int

	// ScrollLines is how many lines the viewport scrolled this frame.
	ScrollLines int
}

// renderStatsJSON is the JSONL record written by the debug writer.
type renderStatsJSON struct {
	Ts              int64 `json:"ts"`
	TotalUs         int64 `json:"total_us"`
	RenderUs        int64 `json:"render_us"`
	CompositeUs     int64 `json:"composite_us"`
	DiffUs          int64 `json:"diff_us"`
	WriteUs         int64 `json:"write_us"`
	TotalLines      int   `json:"total_lines"`
	LinesRepainted  int   `json:"lines_repainted"`
	CacheHits       int   `json:"cache_hits"`
	FullRedraw      bool  `json:"full_redraw"`
	OverlayCount    int   `json:"overlay_count"`
	BytesWritten    int   `json:"bytes_written"`
	ComponentDirty  bool  `json:"component_dirty"`
	FirstChanged    int   `json:"first_changed"`
	LastChanged     int   `json:"last_changed"`
	ScrollLines     int   `json:"scroll_lines"`
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

	cursorRow          int
	hardwareCursorRow  int
	showHardwareCursor bool
	clearOnShrink      bool
	maxLinesRendered   int
	previousViewportTop int
	fullRedrawCount    int
	stopped            bool

	overlayStack []*overlayEntry

	renderCh    chan struct{} // serialized render requests
	debugWriter io.Writer    // if non-nil, render stats are logged here
}

// New creates a TUI backed by the given terminal.
func New(term Terminal) *TUI {
	t := newTUI(term)
	go t.renderLoop()
	return t
}

// newTUI creates a TUI without starting the render loop. Used by tests
// that call doRender synchronously.
func newTUI(term Terminal) *TUI {
	return &TUI{
		terminal: term,
		renderCh: make(chan struct{}, 1),
	}
}

// renderLoop processes render requests serially on a dedicated goroutine.
func (t *TUI) renderLoop() {
	for range t.renderCh {
		t.doRender()
	}
}

// Terminal returns the underlying terminal.
func (t *TUI) Terminal() Terminal { return t.terminal }

// SetDebugWriter enables render performance logging. Each render cycle
// writes a single stats line to w. Pass nil to disable.
func (t *TUI) SetDebugWriter(w io.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.debugWriter = w
}

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
// content. If NoFocus is set in opts, focus is not changed.
func (t *TUI) ShowOverlay(comp Component, opts *OverlayOptions) *OverlayHandle {
	t.mu.Lock()
	entry := &overlayEntry{
		component: comp,
		options:   opts,
		preFocus:  t.focusedComponent,
	}
	t.overlayStack = append(t.overlayStack, entry)
	noFocus := opts != nil && opts.NoFocus
	if !noFocus && t.isOverlayVisible(entry) {
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
	t.restoreFocusFromOverlayLocked(entry)
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

	// Ensure cursor is at column 0 for clean shell handoff.
	t.terminal.WriteString("\r")
	t.terminal.ShowCursor()
	t.terminal.Stop()
	close(t.renderCh)
}

// RequestRender schedules a render on the next iteration. If force is true,
// all cached state is discarded and a full repaint occurs.
func (t *TUI) RequestRender(force bool) {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	if force {
		t.previousLines = nil
		t.previousWidth = -1
		t.cursorRow = 0
		t.hardwareCursorRow = 0
		t.maxLinesRendered = 0
		t.previousViewportTop = 0
	}
	t.mu.Unlock()

	// Non-blocking send to coalesce multiple rapid requests.
	select {
	case t.renderCh <- struct{}{}:
	default:
	}
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
	totalStart := time.Now()

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
	debugW := t.debugWriter
	overlays := make([]*overlayEntry, len(t.overlayStack))
	copy(overlays, t.overlayStack)
	t.mu.Unlock()

	var stats RenderStats
	stats.OverlayCount = len(overlays)

	// Render all components.
	renderStart := time.Now()
	baseResult := t.Render(RenderContext{Width: width})
	newLines := baseResult.Lines
	cursorPos := baseResult.Cursor
	stats.RenderTime = time.Since(renderStart)
	stats.ComponentDirty = baseResult.Dirty

	// Composite overlays.
	if len(overlays) > 0 {
		compositeStart := time.Now()
		newLines, cursorPos = t.compositeOverlays(newLines, cursorPos, overlays, width, height, maxLinesRendered)
		stats.CompositeTime = time.Since(compositeStart)
	}

	// Append reset to each line.
	for i := range newLines {
		newLines[i] += segmentReset
	}
	stats.TotalLines = len(newLines)

	viewportTop := max(0, maxLinesRendered-height)

	// Helper: line diff from hardware cursor to a target row, accounting for
	// viewport scrolling.
	computeLineDiff := func(targetRow int) int {
		currentScreen := hardwareCursorRow - prevViewportTop
		targetScreen := targetRow - viewportTop
		return targetScreen - currentScreen
	}

	widthChanged := prevWidth != 0 && prevWidth != width

	// emitStats writes the debug stats as JSONL if a debug writer is configured.
	emitStats := func() {
		if debugW == nil {
			return
		}
		stats.TotalTime = time.Since(totalStart)
		rec := renderStatsJSON{
			Ts:              time.Now().UnixMilli(),
			TotalUs:         stats.TotalTime.Microseconds(),
			RenderUs:        stats.RenderTime.Microseconds(),
			CompositeUs:     stats.CompositeTime.Microseconds(),
			DiffUs:          stats.DiffTime.Microseconds(),
			WriteUs:         stats.WriteTime.Microseconds(),
			TotalLines:      stats.TotalLines,
			LinesRepainted:  stats.LinesRepainted,
			CacheHits:       stats.CacheHits,
			FullRedraw:      stats.FullRedraw,
			OverlayCount:    stats.OverlayCount,
			BytesWritten:    stats.BytesWritten,
			ComponentDirty:  stats.ComponentDirty,
			FirstChanged:    stats.FirstChangedLine,
			LastChanged:     stats.LastChangedLine,
			ScrollLines:     stats.ScrollLines,
		}
		data, _ := json.Marshal(rec)
		data = append(data, '\n')
		debugW.Write(data) //nolint:errcheck
	}

	// --- full render helper ---
	fullRender := func(clear bool) {
		t.mu.Lock()
		t.fullRedrawCount++
		t.mu.Unlock()

		stats.FullRedraw = true
		stats.LinesRepainted = len(newLines)
		stats.CacheHits = 0
		stats.FirstChangedLine = 0
		stats.LastChangedLine = max(0, len(newLines)-1)

		diffStart := time.Now()
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
		stats.DiffTime = time.Since(diffStart)
		stats.BytesWritten = buf.Len()

		writeStart := time.Now()
		t.terminal.WriteString(buf.String())
		stats.WriteTime = time.Since(writeStart)

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

		emitStats()
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
	diffStart := time.Now()

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
		stats.DiffTime = time.Since(diffStart)
		stats.CacheHits = len(newLines)
		stats.LinesRepainted = 0
		stats.FirstChangedLine = -1
		stats.LastChangedLine = -1
		t.positionHardwareCursor(cursorPos, len(newLines))
		t.mu.Lock()
		t.previousViewportTop = max(0, t.maxLinesRendered-height)
		t.mu.Unlock()
		emitStats()
		return
	}

	// All changes in deleted tail.
	if firstChanged >= len(newLines) {
		stats.CacheHits = len(newLines)
		stats.LinesRepainted = 0
		stats.FirstChangedLine = firstChanged
		stats.LastChangedLine = lastChanged
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
			stats.DiffTime = time.Since(diffStart)
			stats.BytesWritten = buf.Len()

			writeStart := time.Now()
			t.terminal.WriteString(buf.String())
			stats.WriteTime = time.Since(writeStart)

			t.mu.Lock()
			t.cursorRow = targetRow
			t.hardwareCursorRow = targetRow
			t.mu.Unlock()
		} else {
			stats.DiffTime = time.Since(diffStart)
		}
		t.positionHardwareCursor(cursorPos, len(newLines))
		t.mu.Lock()
		t.previousLines = newLines
		t.previousWidth = width
		t.previousViewportTop = max(0, t.maxLinesRendered-height)
		t.mu.Unlock()
		emitStats()
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
		stats.ScrollLines = scroll
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
	stats.DiffTime = time.Since(diffStart)

	// Compute repainted/cached stats.
	stats.LinesRepainted = renderEnd - firstChanged + 1
	stats.CacheHits = len(newLines) - stats.LinesRepainted
	stats.FirstChangedLine = firstChanged
	stats.LastChangedLine = lastChanged
	stats.BytesWritten = buf.Len()

	writeStart := time.Now()
	t.terminal.WriteString(buf.String())
	stats.WriteTime = time.Since(writeStart)

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

	emitStats()
}

// ---------- overlay compositing ---------------------------------------------

func (t *TUI) compositeOverlays(lines []string, baseCursor *CursorPos, overlays []*overlayEntry, termW, termH, maxLinesRendered int) ([]string, *CursorPos) {
	contentH := len(lines)
	result := make([]string, contentH)
	copy(result, lines)
	cursor := baseCursor

	type rendered struct {
		lines           []string
		row             int
		col             int
		w               int
		contentRelative bool
		cursor          *CursorPos
	}
	var items []rendered
	minNeeded := len(result)

	for _, e := range overlays {
		if !t.isOverlayVisible(e) {
			continue
		}
		cr := e.options != nil && e.options.ContentRelative
		refH := termH
		if cr {
			refH = contentH
		}
		// First pass: resolve width and maxHeight (height-independent).
		w, _, _, maxH, maxHSet := t.resolveOverlayLayout(e.options, 0, termW, refH)
		renderH := termH
		if maxHSet {
			renderH = maxH
		}
		oResult := e.component.Render(RenderContext{Width: w, Height: renderH})
		oLines := oResult.Lines
		// Second pass: resolve placement with actual height; clamp if needed.
		var row, col int
		_, row, col, maxH, maxHSet = t.resolveOverlayLayout(e.options, len(oLines), termW, refH)
		if maxHSet && len(oLines) > maxH {
			oLines = oLines[:maxH]
			// Row may shift if we clamped height (e.g. bottom-anchored).
			_, row, col, _, _ = t.resolveOverlayLayout(e.options, len(oLines), termW, refH)
		}
		items = append(items, rendered{
			lines:           oLines,
			row:             row,
			col:             col,
			w:               w,
			contentRelative: cr,
			cursor:          oResult.Cursor,
		})
		if row+len(oLines) > minNeeded {
			minNeeded = row + len(oLines)
		}
	}

	// Overlays (both viewport-relative and content-relative) can extend the working height.
	workingH := max(maxLinesRendered, minNeeded)
	for len(result) < workingH {
		result = append(result, "")
	}

	viewportStart := max(0, workingH-termH)

	for _, item := range items {
		for i, ol := range item.lines {
			var idx int
			if item.contentRelative {
				idx = item.row + i
			} else {
				idx = viewportStart + item.row + i
			}
			if idx >= 0 && idx < len(result) {
				result[idx] = CompositeLineAt(result[idx], ol, item.col, item.w, termW)
			}
		}
		// If the overlay's component has focus and provides a cursor,
		// translate it to the composited coordinate space.
		if item.cursor != nil {
			t.mu.Lock()
			focused := t.focusedComponent
			t.mu.Unlock()
			// Check if this overlay has focus by comparing its component
			// We check via the overlay stack entries.
			for _, e := range overlays {
				if e.component == focused {
					// This is the focused overlay; use its cursor.
					var baseRow int
					if item.contentRelative {
						baseRow = item.row
					} else {
						baseRow = viewportStart + item.row
					}
					cursor = &CursorPos{
						Row: baseRow + item.cursor.Row,
						Col: item.col + item.cursor.Col,
					}
					break
				}
			}
		}
	}

	return result, cursor
}

// ---------- cursor ----------------------------------------------------------

func (t *TUI) positionHardwareCursor(pos *CursorPos, totalLines int) {
	if pos == nil || totalLines <= 0 {
		t.terminal.HideCursor()
		return
	}

	targetRow := clamp(pos.Row, 0, totalLines-1)
	targetCol := max(0, pos.Col)

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
