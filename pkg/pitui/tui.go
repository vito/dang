package pitui

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// InputListener is called with each decoded event before it reaches the
// focused component. Return true to consume the event and stop propagation.
type InputListener func(ev uv.Event) bool

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

	// FullRedrawReason describes why a full redraw was triggered.
	FullRedrawReason string

	// OverlayCount is the number of active overlays composited.
	OverlayCount int

	// BytesWritten is the number of bytes sent to the terminal (escape
	// sequences + content). Large values indicate potential slowness over
	// SSH or on slow terminals.
	BytesWritten int

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
	Ts             int64           `json:"ts"`
	TotalUs        int64           `json:"total_us"`
	RenderUs       int64           `json:"render_us"`
	CompositeUs    int64           `json:"composite_us"`
	DiffUs         int64           `json:"diff_us"`
	WriteUs        int64           `json:"write_us"`
	TotalLines     int             `json:"total_lines"`
	LinesRepainted int             `json:"lines_repainted"`
	CacheHits      int             `json:"cache_hits"`
	FullRedraw     bool            `json:"full_redraw"`
	FullRedrawWhy  string          `json:"full_redraw_why,omitempty"`
	OverlayCount   int             `json:"overlay_count"`
	BytesWritten   int             `json:"bytes_written"`
	FirstChanged   int             `json:"first_changed"`
	LastChanged    int             `json:"last_changed"`
	ScrollLines    int             `json:"scroll_lines"`
	Components     []ComponentStat `json:"components,omitempty"`
}

// TUI is the main renderer. It extends Container with differential rendering
// on the normal scrollback buffer.
type TUI struct {
	Container

	terminal Terminal
	decoder  uv.EventDecoder

	mu sync.Mutex // protects all mutable state below

	previousLines    []string
	previousWidth    int
	focusedComponent Component
	inputListeners   []inputListenerEntry

	cursorRow           int
	hardwareCursorRow   int
	showHardwareCursor  bool
	clearOnShrink       bool
	maxLinesRendered    int
	previousViewportTop int
	fullRedrawCount     int
	stopped             bool

	overlayStack []*overlayEntry

	kittyKeyboard bool // terminal confirmed Kitty keyboard protocol support

	renderCh    chan struct{} // serialized render requests
	renderDone  chan struct{} // closed when renderLoop exits
	debugWriter io.Writer     // if non-nil, render stats are logged here
}

// New creates a TUI backed by the given terminal.
func New(term Terminal) *TUI {
	t := newTUI(term)
	t.renderDone = make(chan struct{})
	go t.renderLoop()
	return t
}

// newTUI creates a TUI without starting the render loop. Used by tests
// that call doRender synchronously.
func newTUI(term Terminal) *TUI {
	t := &TUI{
		terminal: term,
		renderCh: make(chan struct{}, 1),
	}
	// Wire upward propagation: when any child calls Update(), the root
	// Compo's requestRender triggers TUI.RequestRender.
	t.Container.requestRender = func() {
		t.RequestRender(false)
	}
	return t
}

// renderLoop processes render requests serially on a dedicated goroutine.
func (t *TUI) renderLoop() {
	defer func() {
		if t.renderDone != nil {
			close(t.renderDone)
		}
	}()
	for range t.renderCh {
		t.doRender()
	}
}

// Terminal returns the underlying terminal.
func (t *TUI) Terminal() Terminal { return t.terminal }

// HasKittyKeyboard reports whether the terminal confirmed support for the
// Kitty keyboard protocol (disambiguate escape codes). This is determined
// by the response to the RequestKittyKeyboard query sent during Start().
// Returns false until the response is received.
func (t *TUI) HasKittyKeyboard() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.kittyKeyboard
}

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

// ShowOverlay displays a component as an overlay on top of the base content.
// Focus is not changed; use [TUI.SetFocus] to direct input to the overlay's
// component when needed.
func (t *TUI) ShowOverlay(comp Component, opts *OverlayOptions) *OverlayHandle {
	t.mu.Lock()
	entry := &overlayEntry{
		component: comp,
		options:   opts,
	}
	t.overlayStack = append(t.overlayStack, entry)
	t.mu.Unlock()
	t.RequestRender(false)
	return &OverlayHandle{tui: t, entry: entry}
}

// HasOverlay reports whether any overlay is currently visible.
func (t *TUI) HasOverlay() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, o := range t.overlayStack {
		if !o.hidden {
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
	t.mu.Unlock()

	// Close the render channel and wait for the render loop to finish
	// any in-progress render. This ensures no cursor positioning from
	// a concurrent doRender can race with our cleanup below.
	close(t.renderCh)
	if t.renderDone != nil {
		<-t.renderDone
	}

	t.mu.Lock()
	prev := t.previousLines
	hcr := t.hardwareCursorRow
	t.mu.Unlock()

	// Move cursor past content so the shell prompt appears below.
	if len(prev) > 0 {
		target := len(prev)
		t.terminal.WriteString(cursorVertical(target - hcr))
		t.terminal.WriteString("\r\n")
	}

	// Ensure cursor is at column 0 for clean shell handoff.
	t.terminal.WriteString("\r")
	t.terminal.ShowCursor()
	t.terminal.Stop()
}

// RequestRender schedules a render on the next iteration. If repaint is
// true, all cached state is discarded and a full repaint occurs.
func (t *TUI) RequestRender(repaint bool) {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	if repaint {
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

// ---------- input handling --------------------------------------------------

func (t *TUI) handleInput(data []byte) {
	buf := data
	for len(buf) > 0 {
		n, ev := t.decoder.Decode(buf)
		if n == 0 {
			break
		}
		buf = buf[n:]
		if ev == nil {
			continue
		}
		t.dispatchEvent(ev)
	}
}

func (t *TUI) dispatchEvent(ev uv.Event) {
	t.mu.Lock()
	listeners := make([]inputListenerEntry, len(t.inputListeners))
	copy(listeners, t.inputListeners)
	t.mu.Unlock()

	for _, entry := range listeners {
		if entry.fn(ev) {
			return
		}
	}

	t.mu.Lock()
	comp := t.focusedComponent
	t.mu.Unlock()

	switch e := ev.(type) {
	case uv.KeyboardEnhancementsEvent:
		t.mu.Lock()
		t.kittyKeyboard = e.SupportsKeyDisambiguation()
		t.mu.Unlock()
		return
	case uv.KeyPressEvent:
		if ic, ok := comp.(Interactive); ok {
			ic.HandleKeyPress(e)
			t.RequestRender(false)
		}
	case uv.PasteEvent:
		if p, ok := comp.(Pasteable); ok {
			p.HandlePaste(e)
			t.RequestRender(false)
		}
	}
}

// ---------- escape sequences ------------------------------------------------
//
// Named constants for ANSI/DEC escape sequences used during rendering.
// Using constants makes the intent clear and avoids scattered string literals.

const (
	// Synchronized output (DEC private mode 2026). Tells the terminal to
	// buffer writes and flush atomically, preventing flicker.
	escSyncBegin = "\x1b[?2026h"
	escSyncEnd   = "\x1b[?2026l"

	// Screen / scrollback clearing.
	escClearScrollback = "\x1b[3J" // erase scrollback buffer
	escClearScreen     = "\x1b[2J" // erase visible screen
	escCursorHome      = "\x1b[H"  // move cursor to (1,1)

	// Line-level operations.
	escClearLine = "\x1b[2K" // erase entire current line
)

// cursorUp returns an escape sequence moving the cursor up n rows.
// Returns "" if n <= 0.
func cursorUp(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%dA", n)
}

// cursorDown returns an escape sequence moving the cursor down n rows.
// Returns "" if n <= 0.
func cursorDown(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%dB", n)
}

// cursorColumn returns an escape sequence moving the cursor to column col
// (1-indexed, as the terminal expects).
func cursorColumn(col int) string {
	return fmt.Sprintf("\x1b[%dG", col)
}

// cursorVertical writes cursor-up or cursor-down for a signed delta.
func cursorVertical(delta int) string {
	if delta > 0 {
		return cursorDown(delta)
	}
	return cursorUp(-delta)
}

// ---------- differential rendering ------------------------------------------

// renderSnapshot holds a read-only copy of TUI state captured under the lock
// at the start of a render frame.
type renderSnapshot struct {
	width             int
	height            int
	prevLines         []string
	prevWidth         int
	hardwareCursorRow int
	maxLinesRendered  int
	prevViewportTop   int
	clearOnShrink     bool
	debugWriter       io.Writer
	overlays          []overlayEntry
}

// widthChanged reports whether the terminal width changed since the last frame.
func (s *renderSnapshot) widthChanged() bool {
	return s.prevWidth != 0 && s.prevWidth != s.width
}

// viewportTop returns the first visible row given the working area height.
func (s *renderSnapshot) viewportTop() int {
	return max(0, s.maxLinesRendered-s.height)
}

// lineDelta returns the cursor movement (in rows) needed to move from the
// hardware cursor's current screen position to targetRow's screen position,
// accounting for viewport scrolling.
func (s *renderSnapshot) lineDelta(targetRow, viewportTop int) int {
	currentScreen := s.hardwareCursorRow - s.prevViewportTop
	targetScreen := targetRow - viewportTop
	return targetScreen - currentScreen
}

// diffResult holds the output of diffLines.
type diffResult struct {
	firstChanged int  // first line that differs, or -1
	lastChanged  int  // last line that differs, or -1
	appendStart  bool // true if changes are purely appended lines starting right after prevLines
}

// diffLines compares old and new line slices and returns the range of changed
// lines. Pure function — no side effects.
func diffLines(prev, next []string) diffResult {
	firstChanged := -1
	lastChanged := -1
	n := max(len(next), len(prev))
	for i := 0; i < n; i++ {
		var oldLine, newLine string
		if i < len(prev) {
			oldLine = prev[i]
		}
		if i < len(next) {
			newLine = next[i]
		}
		if oldLine != newLine {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}

	appended := len(next) > len(prev)
	if appended {
		if firstChanged == -1 {
			firstChanged = len(prev)
		}
		lastChanged = len(next) - 1
	}
	appendStart := appended && firstChanged == len(prev) && firstChanged > 0

	return diffResult{
		firstChanged: firstChanged,
		lastChanged:  lastChanged,
		appendStart:  appendStart,
	}
}

func (t *TUI) doRender() {
	totalStart := time.Now()

	snap := t.snapshotForRender()
	if snap == nil {
		return // stopped
	}

	var stats RenderStats
	stats.OverlayCount = len(snap.overlays)

	// Render all components.
	newLines, cursorPos, compStats := t.renderFrame(snap, &stats)

	// Choose rendering strategy and write to terminal.
	t.applyFrame(snap, newLines, cursorPos, compStats, &stats, totalStart)
}

// snapshotForRender captures a consistent copy of all mutable state needed
// for rendering. Returns nil if the TUI is stopped.
func (t *TUI) snapshotForRender() *renderSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return nil
	}
	snap := &renderSnapshot{
		width:             t.terminal.Columns(),
		height:            t.terminal.Rows(),
		prevLines:         t.previousLines,
		prevWidth:         t.previousWidth,
		hardwareCursorRow: t.hardwareCursorRow,
		maxLinesRendered:  t.maxLinesRendered,
		prevViewportTop:   t.previousViewportTop,
		clearOnShrink:     t.clearOnShrink,
		debugWriter:       t.debugWriter,
		overlays:          make([]overlayEntry, len(t.overlayStack)),
	}
	for i, e := range t.overlayStack {
		snap.overlays[i] = *e
	}
	return snap
}

// renderFrame renders the component tree and composites overlays, producing
// the new set of output lines with reset sequences appended.
func (t *TUI) renderFrame(snap *renderSnapshot, stats *RenderStats) ([]string, *CursorPos, []ComponentStat) {
	renderStart := time.Now()
	ctx := RenderContext{Width: snap.width}
	var compStats []ComponentStat
	if snap.debugWriter != nil {
		ctx.componentStats = &compStats
	}
	baseResult := renderComponent(&t.Container, ctx)
	cursorPos := baseResult.Cursor
	stats.RenderTime = time.Since(renderStart)

	// Copy lines so we don't mutate cached RenderResult slices.
	newLines := make([]string, len(baseResult.Lines))
	copy(newLines, baseResult.Lines)

	// Composite overlays.
	if len(snap.overlays) > 0 {
		compositeStart := time.Now()
		newLines, cursorPos = t.compositeOverlays(
			newLines, cursorPos, snap.overlays,
			snap.width, snap.height, snap.maxLinesRendered,
		)
		stats.CompositeTime = time.Since(compositeStart)
	}

	// Append reset to each line to prevent style bleed.
	for i := range newLines {
		newLines[i] += segmentReset
	}
	stats.TotalLines = len(newLines)

	return newLines, cursorPos, compStats
}

// applyFrame decides the rendering strategy (full redraw vs differential
// update) and writes the result to the terminal.
func (t *TUI) applyFrame(snap *renderSnapshot, newLines []string, cursorPos *CursorPos, compStats []ComponentStat, stats *RenderStats, totalStart time.Time) {
	emitStats := func() {
		t.emitDebugStats(snap.debugWriter, stats, compStats, totalStart)
	}

	// Full redraw needed?
	if reason, clear := t.needsFullRedraw(snap, newLines); reason != "" {
		stats.FullRedrawReason = reason
		t.writeFullRedraw(snap, newLines, cursorPos, stats, clear)
		emitStats()
		return
	}

	// Compute diff.
	diffStart := time.Now()
	dr := diffLines(snap.prevLines, newLines)
	viewportTop := snap.viewportTop()

	// No changes — just reposition cursor.
	if dr.firstChanged == -1 {
		stats.DiffTime = time.Since(diffStart)
		stats.CacheHits = len(newLines)
		stats.FirstChangedLine = -1
		stats.LastChangedLine = -1
		t.positionHardwareCursor(cursorPos, len(newLines))
		t.mu.Lock()
		t.previousViewportTop = max(0, t.maxLinesRendered-snap.height)
		t.mu.Unlock()
		emitStats()
		return
	}

	// All changes in deleted tail.
	if dr.firstChanged >= len(newLines) {
		t.writeTailShrink(snap, newLines, cursorPos, stats, &dr, diffStart, viewportTop)
		emitStats()
		return
	}

	// First change above previous viewport → full redraw.
	if dr.firstChanged < snap.prevViewportTop {
		stats.FullRedrawReason = fmt.Sprintf(
			"above_viewport:first=%d,vpTop=%d,prevLines=%d,newLines=%d,height=%d",
			dr.firstChanged, snap.prevViewportTop, len(snap.prevLines), len(newLines), snap.height,
		)
		t.writeFullRedraw(snap, newLines, cursorPos, stats, true)
		emitStats()
		return
	}

	// Differential update.
	t.writeDiffUpdate(snap, newLines, cursorPos, stats, &dr, diffStart, viewportTop)
	emitStats()
}

// needsFullRedraw returns (reason, clearScreen) if a full redraw is required,
// or ("", false) if differential rendering can proceed.
func (t *TUI) needsFullRedraw(snap *renderSnapshot, newLines []string) (string, bool) {
	if len(snap.prevLines) == 0 && !snap.widthChanged() {
		return "first_render", false
	}
	if snap.widthChanged() {
		return "width_changed", true
	}
	if snap.clearOnShrink && len(newLines) < snap.maxLinesRendered && len(snap.overlays) == 0 {
		return "clear_on_shrink", true
	}
	return "", false
}

// writeFullRedraw writes all lines to the terminal, optionally clearing the
// screen first. Updates TUI state and positions the cursor.
func (t *TUI) writeFullRedraw(snap *renderSnapshot, newLines []string, cursorPos *CursorPos, stats *RenderStats, clear bool) {
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
	buf.WriteString(escSyncBegin)
	if clear {
		buf.WriteString(escClearScrollback)
		buf.WriteString(escClearScreen)
		buf.WriteString(escCursorHome)
	}
	for i, line := range newLines {
		if i > 0 {
			buf.WriteString("\r\n")
		}
		if !clear {
			buf.WriteString(escClearLine)
		}
		buf.WriteString(line)
	}
	buf.WriteString(escSyncEnd)
	stats.DiffTime = time.Since(diffStart)
	stats.BytesWritten = buf.Len()

	writeStart := time.Now()
	t.terminal.WriteString(buf.String())
	stats.WriteTime = time.Since(writeStart)

	cr := max(0, len(newLines)-1)
	ml := snap.maxLinesRendered
	if clear {
		ml = len(newLines)
	} else {
		ml = max(ml, len(newLines))
	}

	t.mu.Lock()
	t.cursorRow = cr
	t.hardwareCursorRow = cr
	t.maxLinesRendered = ml
	t.previousViewportTop = max(0, ml-snap.height)
	t.mu.Unlock()

	t.positionHardwareCursor(cursorPos, len(newLines))

	t.mu.Lock()
	t.previousLines = newLines
	t.previousWidth = snap.width
	t.mu.Unlock()
}

// writeTailShrink handles the case where content was only removed from the
// end (no visible lines changed, just fewer of them).
func (t *TUI) writeTailShrink(snap *renderSnapshot, newLines []string, cursorPos *CursorPos, stats *RenderStats, dr *diffResult, diffStart time.Time, viewportTop int) {
	stats.CacheHits = len(newLines)
	stats.LinesRepainted = 0
	stats.FirstChangedLine = dr.firstChanged
	stats.LastChangedLine = dr.lastChanged

	if len(snap.prevLines) > len(newLines) {
		targetRow := max(0, len(newLines)-1)
		delta := snap.lineDelta(targetRow, viewportTop)
		extra := len(snap.prevLines) - len(newLines)

		if extra > snap.height {
			stats.FullRedrawReason = fmt.Sprintf(
				"tail_shrink_too_large:extra=%d,height=%d", extra, snap.height,
			)
			t.writeFullRedraw(snap, newLines, cursorPos, stats, true)
			return
		}

		var buf strings.Builder
		buf.WriteString(escSyncBegin)
		buf.WriteString(cursorVertical(delta))
		buf.WriteString("\r")
		if extra > 0 {
			buf.WriteString(cursorDown(1))
		}
		for i := 0; i < extra; i++ {
			buf.WriteString("\r")
			buf.WriteString(escClearLine)
			if i < extra-1 {
				buf.WriteString(cursorDown(1))
			}
		}
		if extra > 0 {
			buf.WriteString(cursorUp(extra))
		}
		buf.WriteString(escSyncEnd)
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
	t.previousWidth = snap.width
	t.previousViewportTop = max(0, t.maxLinesRendered-snap.height)
	t.mu.Unlock()
}

// writeDiffUpdate writes only the changed lines to the terminal, scrolling
// the viewport as needed.
func (t *TUI) writeDiffUpdate(snap *renderSnapshot, newLines []string, cursorPos *CursorPos, stats *RenderStats, dr *diffResult, diffStart time.Time, viewportTop int) {
	var buf strings.Builder
	buf.WriteString(escSyncBegin)

	hardwareCursorRow := snap.hardwareCursorRow
	prevViewportTop := snap.prevViewportTop
	prevViewportBottom := prevViewportTop + snap.height - 1

	moveTargetRow := dr.firstChanged
	if dr.appendStart {
		moveTargetRow = dr.firstChanged - 1
	}

	// Scroll viewport down if the first change is below the visible area.
	if moveTargetRow > prevViewportBottom {
		currentScreen := max(0, min(snap.height-1, hardwareCursorRow-prevViewportTop))
		moveToBottom := snap.height - 1 - currentScreen
		buf.WriteString(cursorDown(moveToBottom))
		scroll := moveTargetRow - prevViewportBottom
		stats.ScrollLines = scroll
		for i := 0; i < scroll; i++ {
			buf.WriteString("\r\n")
		}
		prevViewportTop += scroll
		viewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}

	delta := snap.lineDelta(moveTargetRow, viewportTop)
	// Override with updated local values after scrolling.
	currentScreen := hardwareCursorRow - prevViewportTop
	targetScreen := moveTargetRow - viewportTop
	delta = targetScreen - currentScreen
	buf.WriteString(cursorVertical(delta))

	if dr.appendStart {
		buf.WriteString("\r\n")
	} else {
		buf.WriteString("\r")
	}

	renderEnd := min(dr.lastChanged, len(newLines)-1)
	for i := dr.firstChanged; i <= renderEnd; i++ {
		if i > dr.firstChanged {
			buf.WriteString("\r\n")
		}
		buf.WriteString(escClearLine)
		buf.WriteString(newLines[i])
	}

	finalCursorRow := renderEnd

	// Clear deleted trailing lines.
	if len(snap.prevLines) > len(newLines) {
		if renderEnd < len(newLines)-1 {
			moveDown := len(newLines) - 1 - renderEnd
			buf.WriteString(cursorDown(moveDown))
			finalCursorRow = len(newLines) - 1
		}
		extra := len(snap.prevLines) - len(newLines)
		for i := 0; i < extra; i++ {
			buf.WriteString("\r\n")
			buf.WriteString(escClearLine)
		}
		buf.WriteString(cursorUp(extra))
	}

	buf.WriteString(escSyncEnd)
	stats.DiffTime = time.Since(diffStart)

	// Compute repainted/cached stats.
	stats.LinesRepainted = renderEnd - dr.firstChanged + 1
	stats.CacheHits = len(newLines) - stats.LinesRepainted
	stats.FirstChangedLine = dr.firstChanged
	stats.LastChangedLine = dr.lastChanged
	stats.BytesWritten = buf.Len()

	writeStart := time.Now()
	t.terminal.WriteString(buf.String())
	stats.WriteTime = time.Since(writeStart)

	cr := max(0, len(newLines)-1)
	ml := max(snap.maxLinesRendered, len(newLines))

	t.mu.Lock()
	t.cursorRow = cr
	t.hardwareCursorRow = finalCursorRow
	t.maxLinesRendered = ml
	t.previousViewportTop = max(0, ml-snap.height)
	t.mu.Unlock()

	t.positionHardwareCursor(cursorPos, len(newLines))

	t.mu.Lock()
	t.previousLines = newLines
	t.previousWidth = snap.width
	t.mu.Unlock()
}

// emitDebugStats writes render stats as a JSONL record if a debug writer
// is configured.
func (t *TUI) emitDebugStats(w io.Writer, stats *RenderStats, compStats []ComponentStat, totalStart time.Time) {
	if w == nil {
		return
	}
	stats.TotalTime = time.Since(totalStart)
	rec := renderStatsJSON{
		Ts:             time.Now().UnixMilli(),
		TotalUs:        stats.TotalTime.Microseconds(),
		RenderUs:       stats.RenderTime.Microseconds(),
		CompositeUs:    stats.CompositeTime.Microseconds(),
		DiffUs:         stats.DiffTime.Microseconds(),
		WriteUs:        stats.WriteTime.Microseconds(),
		TotalLines:     stats.TotalLines,
		LinesRepainted: stats.LinesRepainted,
		CacheHits:      stats.CacheHits,
		FullRedraw:     stats.FullRedraw,
		FullRedrawWhy:  stats.FullRedrawReason,
		OverlayCount:   stats.OverlayCount,
		BytesWritten:   stats.BytesWritten,
		FirstChanged:   stats.FirstChangedLine,
		LastChanged:    stats.LastChangedLine,
		ScrollLines:    stats.ScrollLines,
		Components:     compStats,
	}
	data, _ := json.Marshal(rec)
	data = append(data, '\n')
	w.Write(data) //nolint:errcheck
}

// ---------- overlay compositing ---------------------------------------------

func (t *TUI) compositeOverlays(lines []string, baseCursor *CursorPos, overlays []overlayEntry, termW, termH, maxLinesRendered int) ([]string, *CursorPos) {
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
	// Pre-rendered overlay data, used for cursor-relative two-pass layout.
	type preRendered struct {
		entry  *overlayEntry
		opts   *OverlayOptions
		w      int
		lines  []string
		cursor *CursorPos
	}

	// Pass 1: render all overlays and determine CursorGroup directions.
	// Cursor groups need all member heights before deciding above/below.
	var pre []preRendered
	groupAbove := make(map[*CursorGroup]bool) // true = all members fit above
	groupSeen := make(map[*CursorGroup]bool)  // whether we've seen this group

	for i := range overlays {
		e := &overlays[i]
		if e.hidden {
			continue
		}
		opts := e.options

		// Resolve width and maxHeight.
		// ContentRelative overlays use contentH as reference height.
		// CursorRelative overlays use termH because they can extend
		// past content bounds.
		cr := opts != nil && opts.ContentRelative
		refH := termH
		if cr {
			refH = contentH
		}
		w, _, _, maxH, maxHSet := t.resolveOverlayLayout(opts, 0, termW, refH)
		renderH := termH
		if maxHSet {
			renderH = maxH
		}
		oResult := renderComponent(e.component, RenderContext{Width: w, Height: renderH})
		oLines := oResult.Lines
		if maxHSet && len(oLines) > maxH {
			oLines = oLines[:maxH]
		}

		pre = append(pre, preRendered{
			entry:  e,
			opts:   opts,
			w:      w,
			lines:  oLines,
			cursor: oResult.Cursor,
		})

		// Track CursorGroup: if any member doesn't fit above, the whole
		// group goes below.
		if opts != nil && opts.CursorRelative && cursor != nil && opts.CursorGroup != nil {
			g := opts.CursorGroup
			if !groupSeen[g] {
				groupSeen[g] = true
				groupAbove[g] = true // optimistic: all fit above
			}
			if !cursorFitsAbove(cursor, len(oLines)) {
				groupAbove[g] = false
			}
		}
	}

	// Pass 2: compute positions and build the composite.
	var items []rendered
	minNeeded := len(result)

	for _, p := range pre {
		opts := p.opts
		oLines := p.lines

		var row, col int
		cr := opts != nil && opts.ContentRelative
		if opts != nil && opts.CursorRelative {
			if cursor == nil {
				continue // no cursor — skip this overlay
			}
			// Determine above/below: use group decision if in a group,
			// otherwise decide individually.
			above := opts.PreferAbove && cursorFitsAbove(cursor, len(oLines))
			if opts.CursorGroup != nil {
				if ga, ok := groupAbove[opts.CursorGroup]; ok {
					above = opts.PreferAbove && ga
				}
			}
			row, col = resolveCursorPosition(opts, cursor, len(oLines), above)
			cr = true // composited in content space
		} else {
			refH := termH
			if cr {
				refH = contentH
			}
			var maxH int
			var maxHSet bool
			_, row, col, maxH, maxHSet = t.resolveOverlayLayout(opts, len(oLines), termW, refH)
			if maxHSet && len(oLines) > maxH {
				oLines = oLines[:maxH]
				_, row, col, _, _ = t.resolveOverlayLayout(opts, len(oLines), termW, refH)
			}
		}
		items = append(items, rendered{
			lines:           oLines,
			row:             row,
			col:             col,
			w:               p.w,
			contentRelative: cr,
			cursor:          p.cursor,
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

	seq := cursorVertical(targetRow-hcr) + cursorColumn(targetCol+1)
	if seq != "" {
		t.terminal.WriteString(seq)
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
