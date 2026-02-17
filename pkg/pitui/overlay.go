package pitui

// OverlayAnchor specifies where an overlay is positioned relative to the
// terminal viewport.
type OverlayAnchor int

const (
	AnchorCenter OverlayAnchor = iota
	AnchorTopLeft
	AnchorTopRight
	AnchorBottomLeft
	AnchorBottomRight
	AnchorTopCenter
	AnchorBottomCenter
	AnchorLeftCenter
	AnchorRightCenter
)

// OverlayMargin specifies pixel-free spacing from terminal edges.
type OverlayMargin struct {
	Top, Right, Bottom, Left int
}

// SizeValue represents either an absolute column/row count or a percentage
// of the terminal dimension ("50%").  Use SizeAbs and SizePct helpers.
type SizeValue struct {
	abs     int
	pct     float64
	isPct   bool
	isSet   bool
}

// SizeAbs returns an absolute SizeValue.
func SizeAbs(n int) SizeValue { return SizeValue{abs: n, isSet: true} }

// SizePct returns a percentage SizeValue (0-100).
func SizePct(p float64) SizeValue { return SizeValue{pct: p, isPct: true, isSet: true} }

func (v SizeValue) resolve(ref int) (int, bool) {
	if !v.isSet {
		return 0, false
	}
	if v.isPct {
		return int(float64(ref) * v.pct / 100), true
	}
	return v.abs, true
}

// OverlayOptions configures overlay positioning and sizing.
type OverlayOptions struct {
	Width     SizeValue
	MinWidth  int
	MaxHeight SizeValue

	Anchor  OverlayAnchor
	OffsetX int
	OffsetY int

	Row SizeValue
	Col SizeValue

	Margin OverlayMargin

	// ContentRelative positions the overlay relative to the rendered content
	// bounds rather than the terminal viewport. For example, AnchorBottomLeft
	// with ContentRelative places the overlay at the bottom of the content
	// (not the bottom of the screen), which is useful for menus that should
	// float just above the last content line.
	ContentRelative bool

	// NoFocus, when true, prevents the overlay from stealing focus when
	// shown. Useful for non-modal popups like completion menus.
	NoFocus bool
}

// OverlayHandle controls a displayed overlay.
type OverlayHandle struct {
	tui   *TUI
	entry *overlayEntry
}

// Hide permanently removes the overlay.
func (h *OverlayHandle) Hide() {
	h.tui.removeOverlay(h.entry)
}

// SetOptions replaces the overlay's positioning/sizing options without
// destroying and recreating the overlay. This avoids allocation churn and
// focus management round-trips for things like repositioning a completion
// menu on each keystroke.
func (h *OverlayHandle) SetOptions(opts *OverlayOptions) {
	h.entry.options = opts
}

// SetHidden temporarily hides or shows the overlay.
func (h *OverlayHandle) SetHidden(hidden bool) {
	h.tui.mu.Lock()
	if h.entry.hidden == hidden {
		h.tui.mu.Unlock()
		return
	}
	h.entry.hidden = hidden
	if hidden {
		h.tui.restoreFocusFromOverlayLocked(h.entry)
	} else {
		noFocus := h.entry.options != nil && h.entry.options.NoFocus
		if !noFocus && h.tui.isOverlayVisible(h.entry) {
			h.tui.setFocusLocked(h.entry.component)
		}
	}
	h.tui.mu.Unlock()
	h.tui.RequestRender(false)
}

// IsHidden reports whether the overlay is temporarily hidden.
func (h *OverlayHandle) IsHidden() bool {
	return h.entry.hidden
}

type overlayEntry struct {
	component Component
	options   *OverlayOptions
	preFocus  Component
	hidden    bool
}

func (t *TUI) isOverlayVisible(e *overlayEntry) bool {
	return !e.hidden
}

func (t *TUI) topmostVisibleOverlay() *overlayEntry {
	for i := len(t.overlayStack) - 1; i >= 0; i-- {
		if t.isOverlayVisible(t.overlayStack[i]) {
			return t.overlayStack[i]
		}
	}
	return nil
}

func (t *TUI) removeOverlay(entry *overlayEntry) {
	t.mu.Lock()
	found := false
	for i, e := range t.overlayStack {
		if e == entry {
			t.overlayStack = append(t.overlayStack[:i], t.overlayStack[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		t.mu.Unlock()
		return
	}
	t.restoreFocusFromOverlayLocked(entry)
	noOverlays := len(t.overlayStack) == 0
	t.mu.Unlock()
	if noOverlays {
		t.terminal.HideCursor()
	}
	t.RequestRender(false)
}

// restoreFocusFromOverlayLocked updates focus when an overlay loses
// visibility (hidden or removed). If the overlay had focus, focus moves to
// the next visible overlay or falls back to the overlay's preFocus.
// Caller must hold t.mu.
func (t *TUI) restoreFocusFromOverlayLocked(entry *overlayEntry) {
	if t.focusedComponent != entry.component {
		return
	}
	top := t.topmostVisibleOverlay()
	if top != nil {
		t.setFocusLocked(top.component)
	} else {
		t.setFocusLocked(entry.preFocus)
	}
}

// resolveOverlayLayout determines the width, row, col, and maxHeight for an
// overlay given its options and the current terminal dimensions.
func (t *TUI) resolveOverlayLayout(opts *OverlayOptions, overlayHeight, termW, termH int) (width, row, col int, maxH int, maxHSet bool) {
	if opts == nil {
		opts = &OverlayOptions{}
	}

	mTop := max(0, opts.Margin.Top)
	mRight := max(0, opts.Margin.Right)
	mBottom := max(0, opts.Margin.Bottom)
	mLeft := max(0, opts.Margin.Left)

	availW := max(1, termW-mLeft-mRight)
	availH := max(1, termH-mTop-mBottom)

	// Width.
	if w, ok := opts.Width.resolve(termW); ok {
		width = w
	} else {
		width = min(80, availW)
	}
	if opts.MinWidth > 0 && width < opts.MinWidth {
		width = opts.MinWidth
	}
	width = clamp(width, 1, availW)

	// MaxHeight.
	if mh, ok := opts.MaxHeight.resolve(termH); ok {
		maxH = clamp(mh, 1, availH)
		maxHSet = true
	}

	effectiveH := overlayHeight
	if maxHSet && effectiveH > maxH {
		effectiveH = maxH
	}

	// Row.
	if opts.Row.isSet {
		if opts.Row.isPct {
			maxRow := max(0, availH-effectiveH)
			row = mTop + int(float64(maxRow)*opts.Row.pct/100)
		} else {
			row = opts.Row.abs
		}
	} else {
		row = anchorRow(opts.Anchor, effectiveH, availH, mTop)
	}

	// Col.
	if opts.Col.isSet {
		if opts.Col.isPct {
			maxCol := max(0, availW-width)
			col = mLeft + int(float64(maxCol)*opts.Col.pct/100)
		} else {
			col = opts.Col.abs
		}
	} else {
		col = anchorCol(opts.Anchor, width, availW, mLeft)
	}

	row += opts.OffsetY
	col += opts.OffsetX

	// Clamp to terminal bounds.
	row = clamp(row, mTop, termH-mBottom-effectiveH)
	col = clamp(col, mLeft, termW-mRight-width)

	return
}

func anchorRow(a OverlayAnchor, h, availH, mTop int) int {
	switch a {
	case AnchorTopLeft, AnchorTopCenter, AnchorTopRight:
		return mTop
	case AnchorBottomLeft, AnchorBottomCenter, AnchorBottomRight:
		return mTop + availH - h
	default: // center variants
		return mTop + (availH-h)/2
	}
}

func anchorCol(a OverlayAnchor, w, availW, mLeft int) int {
	switch a {
	case AnchorTopLeft, AnchorLeftCenter, AnchorBottomLeft:
		return mLeft
	case AnchorTopRight, AnchorRightCenter, AnchorBottomRight:
		return mLeft + availW - w
	default: // center variants
		return mLeft + (availW-w)/2
	}
}
