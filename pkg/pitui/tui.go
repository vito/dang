package pitui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// InputListener is called with each decoded event before it reaches the
// focused component. Return true to consume the event and stop propagation.
type InputListener func(ctx EventContext, ev uv.Event) bool

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

	// ── Runtime / pprof-level metrics ──

	// HeapAlloc is the current heap allocation in bytes (live objects).
	HeapAlloc uint64

	// HeapObjects is the number of allocated heap objects.
	HeapObjects uint64

	// TotalAlloc is the cumulative bytes allocated (monotonically increasing).
	TotalAlloc uint64

	// Sys is the total memory obtained from the OS.
	Sys uint64

	// Mallocs is the number of heap allocations during this render frame.
	Mallocs uint64

	// Frees is the number of heap frees during this render frame.
	Frees uint64

	// HeapAllocDelta is the net bytes allocated during this render frame
	// (TotalAlloc delta).
	HeapAllocDelta uint64

	// NumGC is the total number of completed GC cycles.
	NumGC uint32

	// GCPauseNs is the most recent GC pause duration in nanoseconds.
	GCPauseNs uint64

	// GCCPUFraction is the fraction of CPU time used by the GC.
	GCCPUFraction float64

	// Goroutines is the number of goroutines at the time of the render.
	Goroutines int

	// StackInuse is bytes used by goroutine stacks.
	StackInuse uint64

	// HeapInuse is bytes in in-use heap spans.
	HeapInuse uint64

	// HeapIdle is bytes in idle (unused) heap spans.
	HeapIdle uint64
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

	// Runtime / memory metrics
	HeapAlloc      uint64  `json:"heap_alloc"`
	HeapObjects    uint64  `json:"heap_objects"`
	TotalAlloc     uint64  `json:"total_alloc"`
	Sys            uint64  `json:"sys"`
	Mallocs        uint64  `json:"mallocs"`
	Frees          uint64  `json:"frees"`
	HeapAllocDelta uint64  `json:"heap_alloc_delta"`
	NumGC          uint32  `json:"num_gc"`
	GCPauseNs      uint64  `json:"gc_pause_ns"`
	GCCPUFraction  float64 `json:"gc_cpu_fraction"`
	Goroutines     int     `json:"goroutines"`
	StackInuse     uint64  `json:"stack_inuse"`
	HeapInuse      uint64  `json:"heap_inuse"`
	HeapIdle       uint64  `json:"heap_idle"`
}

// TUI is the main renderer. It extends Container with differential rendering
// on the normal scrollback buffer.
//
// All component state — including Render(), HandleKeyPress(), and any
// Dispatch callbacks — runs on a single UI goroutine. Components never
// need locks for their own fields.
type TUI struct {
	Container

	terminal Terminal
	decoder  uv.EventDecoder

	// mu protects fields shared between the main goroutine and the UI
	// goroutine: stopped, fullRedrawCount, kittyKeyboard.
	// All rendering and component state is owned by the UI goroutine.
	mu sync.Mutex

	// ── mu-protected state (shared between goroutines) ──

	stopped         bool
	fullRedrawCount int
	kittyKeyboard   bool

	// ── UI-goroutine-only state (no lock needed) ──

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

	overlayStack []*overlayEntry

	mouseRefCount int // number of mounted MouseEnabled components

	debugWriter io.Writer    // if non-nil, render stats are logged here
	writeBuf    bytes.Buffer // reused across frames for terminal output

	// ── Event loop channels ──

	eventCh    chan uv.Event // decoded input events from terminal
	dispatchMu sync.Mutex    // protects dispatchQ
	dispatchQ  []func()      // closures to run on UI goroutine
	dispatchCh chan struct{} // capacity-1 signal: "dispatchQ is non-empty"
	renderCh   chan struct{} // coalesced render requests
	loopDone   chan struct{} // closed when runLoop exits

	// ── Lifecycle ──

	stopCtx    context.Context    // cancelled by Stop() to signal shutdown
	stopCancel context.CancelFunc // cancels stopCtx
}

// New creates a TUI backed by the given terminal.
func New(term Terminal) *TUI {
	t := newTUI(term)
	t.stopCtx, t.stopCancel = context.WithCancel(context.Background())
	t.loopDone = make(chan struct{})
	go t.runLoop()
	return t
}

// newTUI creates a TUI without starting the event loop. Used by tests
// that call doRender synchronously.
func newTUI(term Terminal) *TUI {
	t := &TUI{
		terminal:   term,
		eventCh:    make(chan uv.Event, 64),
		dispatchCh: make(chan struct{}, 1),
		renderCh:   make(chan struct{}, 1),
	}
	// Wire upward propagation: when any child calls Update(), the root
	// Compo's requestRender triggers TUI.RequestRender.
	t.requestRender = func() {
		t.RequestRender(false)
	}
	// Mount the TUI's own Container so children added via AddChild
	// are automatically mounted (receiving lifecycle hooks).
	t.Compo.tui = t
	t.Compo.self = &t.Container
	t.Container.Compo.mountCtx = context.Background()
	// mountCancel is nil for the root — it's never dismounted.
	return t
}

// runLoop is the unified event loop. It processes input events, dispatched
// closures, and render requests — all on a single goroutine. This is the
// "UI goroutine": all component state (Render, HandleKeyPress, Dispatch
// callbacks) executes here, so components never need locks.
func (t *TUI) runLoop() {
	defer close(t.loopDone)
	for {
		// Wait for any signal.
		select {
		case <-t.stopCtx.Done():
			return
		case ev := <-t.eventCh:
			t.dispatchEvent(ev)
		case <-t.dispatchCh:
			t.drainDispatchQ()
		case <-t.renderCh:
			// fall through to drain + render
		}

		// Drain all pending events and dispatches before rendering.
		// This coalesces rapid input and multiple dispatches into one frame.
	drain:
		for {
			select {
			case <-t.stopCtx.Done():
				return
			case ev := <-t.eventCh:
				t.dispatchEvent(ev)
			case <-t.dispatchCh:
				t.drainDispatchQ()
			default:
				break drain
			}
		}

		t.mu.Lock()
		stopped := t.stopped
		t.mu.Unlock()
		if !stopped {
			t.doRender()
		}
	}
}

// drainDispatchQ runs all queued dispatch functions in order.
// Called only on the UI goroutine.
func (t *TUI) drainDispatchQ() {
	t.dispatchMu.Lock()
	q := t.dispatchQ
	t.dispatchQ = nil
	t.dispatchMu.Unlock()
	for _, fn := range q {
		fn()
	}
}

// Terminal returns the underlying terminal.
func (t *TUI) Terminal() Terminal { return t.terminal }

// HasKittyKeyboard reports whether the terminal confirmed support for the
// Kitty keyboard protocol (disambiguate escape codes). This is determined
// by the response to the RequestKittyKeyboard query sent during Start().
// Returns false until the response is received.
//
// Safe to call from any goroutine.
func (t *TUI) HasKittyKeyboard() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.kittyKeyboard
}

// SetDebugWriter enables render performance logging. Each render cycle
// writes a single stats line to w. Pass nil to disable.
//
// Safe to call from any goroutine; takes effect on next render.
func (t *TUI) SetDebugWriter(w io.Writer) {
	t.Dispatch(func() {
		t.debugWriter = w
	})
}

// FullRedraws returns the number of full (non-differential) redraws performed.
//
// Safe to call from any goroutine.
func (t *TUI) FullRedraws() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fullRedrawCount
}

// SetShowHardwareCursor enables or disables the hardware cursor (for IME).
func (t *TUI) SetShowHardwareCursor(enabled bool) {
	t.Dispatch(func() {
		if t.showHardwareCursor == enabled {
			return
		}
		t.showHardwareCursor = enabled
		if !enabled {
			t.terminal.HideCursor()
		}
	})
}

// EnableMouse increments the mouse reference count and, if transitioning
// from 0 to 1, enables terminal mouse reporting. Must be called on the
// UI goroutine. Components normally don't call this directly — the
// framework calls it automatically when a MouseEnabled component is mounted.
func (t *TUI) EnableMouse() {
	t.mouseRefCount++
	if t.mouseRefCount == 1 {
		t.terminal.WriteString(escMouseAllMotionEnable)
		t.terminal.WriteString(escMouseSGREnable)
	}
}

// DisableMouse decrements the mouse reference count and, if transitioning
// to 0, disables terminal mouse reporting. Must be called on the UI
// goroutine.
func (t *TUI) DisableMouse() {
	t.mouseRefCount--
	if t.mouseRefCount <= 0 {
		t.mouseRefCount = 0
		t.terminal.WriteString(escMouseSGRDisable)
		t.terminal.WriteString(escMouseAllMotionDisable)
	}
}

// SetClearOnShrink controls whether empty rows are cleared when content
// shrinks. When false (the default), stale rows remain until overwritten,
// which reduces full redraws on slower terminals.
func (t *TUI) SetClearOnShrink(enabled bool) {
	t.Dispatch(func() {
		t.clearOnShrink = enabled
	})
}

// SetFocus gives keyboard focus to the given component (or nil).
// Must be called on the UI goroutine (from an event handler or Dispatch).
func (t *TUI) SetFocus(comp Component) {
	if f, ok := t.focusedComponent.(Focusable); ok {
		f.SetFocused(t.eventContextFor(t.focusedComponent), false)
	}
	t.focusedComponent = comp
	if f, ok := comp.(Focusable); ok {
		f.SetFocused(t.eventContextFor(comp), true)
	}
}

// eventContextFor constructs an EventContext for the given component,
// using its mount context if available.
func (t *TUI) eventContextFor(comp Component) EventContext {
	ctx := context.Background()
	if comp != nil {
		cp := comp.compo()
		if cp.mountCtx != nil {
			ctx = cp.mountCtx
		}
	}
	return EventContext{
		Context: ctx,
		tui:     t,
		source:  comp,
	}
}

// AddInputListener registers a listener that intercepts input before it
// reaches the focused component. Returns a function that removes it.
// Must be called on the UI goroutine (from an event handler or Dispatch).
func (t *TUI) AddInputListener(l InputListener) func() {
	type token struct{}
	tok := &token{}
	t.inputListeners = append(t.inputListeners, inputListenerEntry{fn: l, tok: tok})
	return func() {
		t.Dispatch(func() {
			for i, entry := range t.inputListeners {
				if entry.tok == tok {
					t.inputListeners = append(t.inputListeners[:i], t.inputListeners[i+1:]...)
					return
				}
			}
		})
	}
}

// ShowOverlay displays a component as an overlay on top of the base content.
// Focus is not changed; use [TUI.SetFocus] to direct input to the overlay's
// component when needed.
// Must be called on the UI goroutine (from an event handler or Dispatch).
func (t *TUI) ShowOverlay(comp Component, opts *OverlayOptions) *OverlayHandle {
	entry := &overlayEntry{
		component: comp,
		options:   opts,
	}
	t.overlayStack = append(t.overlayStack, entry)
	t.RequestRender(false)
	return &OverlayHandle{tui: t, entry: entry}
}

// HasOverlay reports whether any overlay is currently visible.
// Must be called on the UI goroutine (from an event handler or Dispatch).
func (t *TUI) HasOverlay() bool {
	for _, o := range t.overlayStack {
		if !o.hidden {
			return true
		}
	}
	return false
}

// Dispatch schedules a function to run on the UI goroutine. Use this to
// mutate component state from background goroutines (e.g. after async I/O).
// The function runs before the next render, serialized with all input
// handling and other dispatched functions.
//
// Safe to call from any goroutine.
func (t *TUI) Dispatch(fn func()) {
	t.dispatchMu.Lock()
	t.dispatchQ = append(t.dispatchQ, fn)
	t.dispatchMu.Unlock()
	// Signal the event loop that work is available.
	select {
	case t.dispatchCh <- struct{}{}:
	default: // already signaled
	}
	// Also signal a render so the event loop wakes up.
	t.RequestRender(false)
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
	// Signal the event loop to stop and wait for it to finish any
	// in-progress work.
	t.stopCancel()
	if t.loopDone != nil {
		<-t.loopDone
	}

	prev := t.previousLines
	hcr := t.hardwareCursorRow

	// Disable mouse tracking before restoring terminal.
	if t.mouseRefCount > 0 {
		t.terminal.WriteString(escMouseSGRDisable)
		t.terminal.WriteString(escMouseAllMotionDisable)
		t.mouseRefCount = 0
	}

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
//
// Safe to call from any goroutine.
func (t *TUI) RequestRender(repaint bool) {
	if repaint {
		// Repaint resets are dispatched to the UI goroutine to avoid races.
		t.Dispatch(func() {
			t.previousLines = nil
			t.previousWidth = -1
			t.cursorRow = 0
			t.hardwareCursorRow = 0
			t.maxLinesRendered = 0
			t.previousViewportTop = 0
		})
	}

	// Non-blocking send to coalesce multiple rapid requests.
	select {
	case t.renderCh <- struct{}{}:
	default:
	}
}

// ---------- input handling --------------------------------------------------

func (t *TUI) handleInput(data []byte) {
	// Decode events on the input goroutine (decoder is stateless enough)
	// and post them to the event channel for the UI goroutine to process.
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
		select {
		case t.eventCh <- ev:
		default:
			// Channel full — drop event rather than block the input reader.
			// This should be rare with a reasonably sized buffer.
		}
	}
}

func (t *TUI) dispatchEvent(ev uv.Event) {
	// Construct EventContext for the focused component.
	comp := t.focusedComponent
	ctx := t.eventContextFor(comp)

	for _, entry := range t.inputListeners {
		if entry.fn(ctx, ev) {
			return
		}
	}

	switch e := ev.(type) {
	case uv.KeyboardEnhancementsEvent:
		// Update under lock for HasKittyKeyboard() which may be called
		// from any goroutine.
		t.mu.Lock()
		t.kittyKeyboard = e.SupportsKeyDisambiguation()
		t.mu.Unlock()
		return
	case uv.KeyPressEvent:
		t.bubbleKeyPress(comp, ctx, e)
	case uv.PasteEvent:
		t.bubblePaste(comp, ctx, e)
	case uv.MouseEvent:
		t.bubbleMouse(comp, ctx, e)
	}
}

// bubbleKeyPress delivers a key event to the focused component and, if
// not consumed, walks up the parent chain giving each Interactive
// ancestor a chance to handle it.
func (t *TUI) bubbleKeyPress(comp Component, ctx EventContext, ev uv.KeyPressEvent) {
	if comp == nil {
		return
	}

	// Focused component gets first shot.
	if ic, ok := comp.(Interactive); ok {
		if ic.HandleKeyPress(ctx, ev) {
			return
		}
	}

	// Bubble up through parents.
	cp := comp.compo().parent
	for cp != nil {
		if cp.self != nil {
			if ic, ok := cp.self.(Interactive); ok {
				if ic.HandleKeyPress(ctx, ev) {
					return
				}
			}
		}
		cp = cp.parent
	}
}

// bubbleMouse delivers a mouse event to the focused component and, if
// not consumed, walks up the parent chain giving each MouseEnabled
// ancestor a chance to handle it.
func (t *TUI) bubbleMouse(comp Component, ctx EventContext, ev uv.MouseEvent) {
	if comp == nil {
		return
	}

	// Focused component gets first shot.
	if mc, ok := comp.(MouseEnabled); ok {
		if mc.HandleMouse(ctx, ev) {
			return
		}
	}

	// Bubble up through parents.
	cp := comp.compo().parent
	for cp != nil {
		if cp.self != nil {
			if mc, ok := cp.self.(MouseEnabled); ok {
				if mc.HandleMouse(ctx, ev) {
					return
				}
			}
		}
		cp = cp.parent
	}
}

// bubblePaste delivers a paste event with the same bubbling logic as
// key presses.
func (t *TUI) bubblePaste(comp Component, ctx EventContext, ev uv.PasteEvent) {
	if comp == nil {
		return
	}

	if p, ok := comp.(Pasteable); ok {
		if p.HandlePaste(ctx, ev) {
			return
		}
	}

	// Bubble up through parents.
	cp := comp.compo().parent
	for cp != nil {
		if cp.self != nil {
			if p, ok := cp.self.(Pasteable); ok {
				if p.HandlePaste(ctx, ev) {
					return
				}
			}
		}
		cp = cp.parent
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

	// Mouse tracking (SGR extended mode with all-motion tracking).
	// These enable/disable mouse event reporting from the terminal.
	escMouseAllMotionEnable  = "\x1b[?1003h" // any-event (motion + buttons)
	escMouseAllMotionDisable = "\x1b[?1003l"
	escMouseSGREnable        = "\x1b[?1006h" // SGR extended encoding
	escMouseSGRDisable       = "\x1b[?1006l"
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
	for i := range n {
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

	// Snapshot memory stats at frame start for per-frame delta tracking.
	var memBefore runtime.MemStats
	if t.debugWriter != nil {
		runtime.ReadMemStats(&memBefore)
	}

	// Capture terminal dimensions once per frame. These come from the
	// terminal (protected by its own lock, updated on SIGWINCH) and
	// should not change mid-frame.
	width := t.terminal.Columns()
	height := t.terminal.Rows()

	var stats RenderStats
	stats.OverlayCount = len(t.overlayStack)

	// Render all components.
	newLines, cursorPos, compStats := t.renderFrame(width, height, &stats)

	// Choose rendering strategy and write to terminal.
	t.applyFrame(width, height, newLines, cursorPos, compStats, &stats, totalStart, &memBefore)
}

// renderFrame renders the component tree and composites overlays, producing
// the new set of output lines with reset sequences appended.
func (t *TUI) renderFrame(width, height int, stats *RenderStats) ([]string, *CursorPos, []ComponentStat) {
	renderStart := time.Now()
	ctx := RenderContext{Width: width, ScreenHeight: height}
	var compStats []ComponentStat
	if t.debugWriter != nil {
		ctx.componentStats = &compStats
	}
	baseResult := renderComponent(&t.Container, ctx)
	cursorPos := baseResult.Cursor
	stats.RenderTime = time.Since(renderStart)

	var newLines []string
	// Composite overlays (needs a mutable copy of the lines slice).
	if len(t.overlayStack) > 0 {
		newLines = make([]string, len(baseResult.Lines))
		copy(newLines, baseResult.Lines)
		compositeStart := time.Now()
		newLines, cursorPos = t.compositeOverlays(
			newLines, cursorPos, t.overlayStack,
			width, height, t.maxLinesRendered,
		)
		stats.CompositeTime = time.Since(compositeStart)
	} else {
		// No overlays — safe to use the cached slice directly.
		newLines = baseResult.Lines
	}

	stats.TotalLines = len(newLines)

	return newLines, cursorPos, compStats
}

// applyFrame decides the rendering strategy (full redraw vs differential
// update) and writes the result to the terminal.
func (t *TUI) applyFrame(width, height int, newLines []string, cursorPos *CursorPos, compStats []ComponentStat, stats *RenderStats, totalStart time.Time, memBefore *runtime.MemStats) {
	emitStats := func() {
		t.emitDebugStats(t.debugWriter, stats, compStats, totalStart, memBefore)
	}

	widthChanged := t.previousWidth != 0 && t.previousWidth != width

	// Full redraw needed?
	if reason, clear := t.needsFullRedraw(widthChanged, newLines); reason != "" {
		stats.FullRedrawReason = reason
		t.writeFullRedraw(width, height, newLines, cursorPos, stats, clear)
		emitStats()
		return
	}

	// Compute diff.
	diffStart := time.Now()
	dr := diffLines(t.previousLines, newLines)
	viewportTop := max(0, t.maxLinesRendered-height)

	// No changes — just reposition cursor.
	if dr.firstChanged == -1 {
		stats.DiffTime = time.Since(diffStart)
		stats.CacheHits = len(newLines)
		stats.FirstChangedLine = -1
		stats.LastChangedLine = -1
		t.positionHardwareCursor(cursorPos, len(newLines))
		t.previousViewportTop = max(0, t.maxLinesRendered-height)
		// Always update previousLines so that Container's double-buffered
		// line slices stay in sync. Without this, a no-change frame leaves
		// previousLines pointing at a stale buffer that Container will
		// overwrite on a subsequent frame, corrupting the diff baseline.
		t.previousLines = newLines
		emitStats()
		return
	}

	// All changes in deleted tail.
	if dr.firstChanged >= len(newLines) {
		t.writeTailShrink(width, height, newLines, cursorPos, stats, &dr, diffStart, viewportTop)
		emitStats()
		return
	}

	// First change above previous viewport → full redraw.
	if dr.firstChanged < t.previousViewportTop {
		stats.FullRedrawReason = fmt.Sprintf(
			"above_viewport:first=%d,vpTop=%d,prevLines=%d,newLines=%d,height=%d",
			dr.firstChanged, t.previousViewportTop, len(t.previousLines), len(newLines), height,
		)
		t.writeFullRedraw(width, height, newLines, cursorPos, stats, true)
		emitStats()
		return
	}

	// Differential update.
	t.writeDiffUpdate(width, height, newLines, cursorPos, stats, &dr, diffStart, viewportTop)
	emitStats()
}

// needsFullRedraw returns (reason, clearScreen) if a full redraw is required,
// or ("", false) if differential rendering can proceed.
func (t *TUI) needsFullRedraw(widthChanged bool, newLines []string) (string, bool) {
	if len(t.previousLines) == 0 && !widthChanged {
		return "first_render", false
	}
	if widthChanged {
		return "width_changed", true
	}
	if t.clearOnShrink && len(newLines) < t.maxLinesRendered && len(t.overlayStack) == 0 {
		return "clear_on_shrink", true
	}
	return "", false
}

// writeFullRedraw writes all lines to the terminal, optionally clearing the
// screen first. Updates TUI state and positions the cursor.
func (t *TUI) writeFullRedraw(width, height int, newLines []string, cursorPos *CursorPos, stats *RenderStats, clear bool) {
	t.mu.Lock()
	t.fullRedrawCount++
	t.mu.Unlock()

	stats.FullRedraw = true
	stats.LinesRepainted = len(newLines)
	stats.CacheHits = 0
	stats.FirstChangedLine = 0
	stats.LastChangedLine = max(0, len(newLines)-1)

	diffStart := time.Now()
	buf := &t.writeBuf
	buf.Reset()
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
		buf.WriteString(segmentReset)
	}
	buf.WriteString(escSyncEnd)
	stats.DiffTime = time.Since(diffStart)
	stats.BytesWritten = buf.Len()

	writeStart := time.Now()
	t.terminal.Write(buf.Bytes())
	stats.WriteTime = time.Since(writeStart)

	cr := max(0, len(newLines)-1)
	ml := t.maxLinesRendered
	if clear {
		ml = len(newLines)
	} else {
		ml = max(ml, len(newLines))
	}

	t.cursorRow = cr
	t.hardwareCursorRow = cr
	t.maxLinesRendered = ml
	t.previousViewportTop = max(0, ml-height)

	t.positionHardwareCursor(cursorPos, len(newLines))

	t.previousLines = newLines
	t.previousWidth = width
}

// writeTailShrink handles the case where content was only removed from the
// end (no visible lines changed, just fewer of them).
func (t *TUI) writeTailShrink(width, height int, newLines []string, cursorPos *CursorPos, stats *RenderStats, dr *diffResult, diffStart time.Time, viewportTop int) {
	stats.CacheHits = len(newLines)
	stats.LinesRepainted = 0
	stats.FirstChangedLine = dr.firstChanged
	stats.LastChangedLine = dr.lastChanged

	if len(t.previousLines) > len(newLines) {
		targetRow := max(0, len(newLines)-1)
		currentScreen := t.hardwareCursorRow - t.previousViewportTop
		targetScreen := targetRow - viewportTop
		delta := targetScreen - currentScreen
		extra := len(t.previousLines) - len(newLines)

		if extra > height {
			stats.FullRedrawReason = fmt.Sprintf(
				"tail_shrink_too_large:extra=%d,height=%d", extra, height,
			)
			t.writeFullRedraw(width, height, newLines, cursorPos, stats, true)
			return
		}

		buf := &t.writeBuf
		buf.Reset()
		buf.WriteString(escSyncBegin)
		buf.WriteString(cursorVertical(delta))
		buf.WriteString("\r")
		if extra > 0 {
			buf.WriteString(cursorDown(1))
		}
		for i := range extra {
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
		t.terminal.Write(buf.Bytes())
		stats.WriteTime = time.Since(writeStart)

		t.cursorRow = targetRow
		t.hardwareCursorRow = targetRow
	} else {
		stats.DiffTime = time.Since(diffStart)
	}

	t.positionHardwareCursor(cursorPos, len(newLines))
	t.previousLines = newLines
	t.previousWidth = width
	t.previousViewportTop = max(0, t.maxLinesRendered-height)
}

// writeDiffUpdate writes only the changed lines to the terminal, scrolling
// the viewport as needed.
func (t *TUI) writeDiffUpdate(width, height int, newLines []string, cursorPos *CursorPos, stats *RenderStats, dr *diffResult, diffStart time.Time, viewportTop int) {
	buf := &t.writeBuf
	buf.Reset()
	buf.WriteString(escSyncBegin)

	hardwareCursorRow := t.hardwareCursorRow
	prevViewportTop := t.previousViewportTop
	prevViewportBottom := prevViewportTop + height - 1

	moveTargetRow := dr.firstChanged
	if dr.appendStart {
		moveTargetRow = dr.firstChanged - 1
	}

	// Scroll viewport down if the first change is below the visible area.
	if moveTargetRow > prevViewportBottom {
		currentScreen := max(0, min(height-1, hardwareCursorRow-prevViewportTop))
		moveToBottom := height - 1 - currentScreen
		buf.WriteString(cursorDown(moveToBottom))
		scroll := moveTargetRow - prevViewportBottom
		stats.ScrollLines = scroll
		for range scroll {
			buf.WriteString("\r\n")
		}
		prevViewportTop += scroll
		viewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}

	// Compute cursor delta using local values (may have been adjusted by scrolling above).
	currentScreen := hardwareCursorRow - prevViewportTop
	targetScreen := moveTargetRow - viewportTop
	delta := targetScreen - currentScreen
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
		buf.WriteString(segmentReset)
	}

	finalCursorRow := renderEnd

	// Clear deleted trailing lines.
	if len(t.previousLines) > len(newLines) {
		if renderEnd < len(newLines)-1 {
			moveDown := len(newLines) - 1 - renderEnd
			buf.WriteString(cursorDown(moveDown))
			finalCursorRow = len(newLines) - 1
		}
		extra := len(t.previousLines) - len(newLines)
		for range extra {
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
	t.terminal.Write(buf.Bytes())
	stats.WriteTime = time.Since(writeStart)

	cr := max(0, len(newLines)-1)
	ml := max(t.maxLinesRendered, len(newLines))

	t.cursorRow = cr
	t.hardwareCursorRow = finalCursorRow
	t.maxLinesRendered = ml
	t.previousViewportTop = max(0, ml-height)

	t.positionHardwareCursor(cursorPos, len(newLines))

	t.previousLines = newLines
	t.previousWidth = width
}

// emitDebugStats writes render stats as a JSONL record if a debug writer
// is configured.
func (t *TUI) emitDebugStats(w io.Writer, stats *RenderStats, compStats []ComponentStat, totalStart time.Time, memBefore *runtime.MemStats) {
	if w == nil {
		return
	}
	stats.TotalTime = time.Since(totalStart)

	// Read memory stats at frame end.
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	// Populate runtime metrics.
	stats.HeapAlloc = memAfter.HeapAlloc
	stats.HeapObjects = memAfter.HeapObjects
	stats.TotalAlloc = memAfter.TotalAlloc
	stats.Sys = memAfter.Sys
	stats.HeapInuse = memAfter.HeapInuse
	stats.HeapIdle = memAfter.HeapIdle
	stats.StackInuse = memAfter.StackInuse
	stats.NumGC = memAfter.NumGC
	stats.GCCPUFraction = memAfter.GCCPUFraction
	stats.Goroutines = runtime.NumGoroutine()

	// Per-frame deltas: allocations and frees during this render cycle.
	if memBefore != nil {
		stats.Mallocs = memAfter.Mallocs - memBefore.Mallocs
		stats.Frees = memAfter.Frees - memBefore.Frees
		stats.HeapAllocDelta = memAfter.TotalAlloc - memBefore.TotalAlloc
	}

	// Most recent GC pause.
	if memAfter.NumGC > 0 {
		// PauseNs is a circular buffer of recent GC pauses indexed by NumGC.
		stats.GCPauseNs = memAfter.PauseNs[(memAfter.NumGC+255)%256]
	}

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

		HeapAlloc:      stats.HeapAlloc,
		HeapObjects:    stats.HeapObjects,
		TotalAlloc:     stats.TotalAlloc,
		Sys:            stats.Sys,
		Mallocs:        stats.Mallocs,
		Frees:          stats.Frees,
		HeapAllocDelta: stats.HeapAllocDelta,
		NumGC:          stats.NumGC,
		GCPauseNs:      stats.GCPauseNs,
		GCCPUFraction:  stats.GCCPUFraction,
		Goroutines:     stats.Goroutines,
		StackInuse:     stats.StackInuse,
		HeapInuse:      stats.HeapInuse,
		HeapIdle:       stats.HeapIdle,
	}
	data, _ := json.Marshal(rec)
	data = append(data, '\n')
	w.Write(data) //nolint:errcheck
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

	for _, e := range overlays {
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
		oResult := renderComponent(e.component, RenderContext{Width: w, Height: renderH, ScreenHeight: termH})
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
			focused := t.focusedComponent
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

	hcr := t.hardwareCursorRow
	show := t.showHardwareCursor

	seq := cursorVertical(targetRow-hcr) + cursorColumn(targetCol+1)
	if seq != "" {
		t.terminal.WriteString(seq)
	}

	t.hardwareCursorRow = targetRow

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
