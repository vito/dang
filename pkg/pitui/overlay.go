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
	abs   int
	pct   float64
	isPct bool
	isSet bool
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
//
// Overlays are pure rendering constructs — they composite a component on top
// of the base content. Focus management is the caller's responsibility; use
// [TUI.SetFocus] to direct input to the overlay's component when needed.
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

	// CursorRelative positions the overlay relative to the base content's
	// cursor position. The cursor position is resolved during compositing
	// on the render goroutine, so the caller doesn't need to track it.
	//
	// When set, Col is placed at cursor column + OffsetX. Row placement
	// depends on PreferAbove: if true, the overlay's bottom edge is placed
	// at the row above the cursor; if there isn't enough room above, it
	// flips to below. If false (or unset), the overlay starts below the
	// cursor and flips to above when needed.
	//
	// Width, MaxHeight, MinWidth, and Margin are resolved normally.
	// Anchor is ignored for row/col positioning (PreferAbove and OffsetX
	// determine placement). If the base content has no cursor, the overlay
	// is hidden for that frame.
	CursorRelative bool

	// PreferAbove is used with CursorRelative to place the overlay above
	// the cursor row when there is enough room, flipping to below otherwise.
	PreferAbove bool

	// CursorGroup links cursor-relative overlays so they share the same
	// above/below direction. If any overlay in the group doesn't fit
	// above the cursor, all overlays in the group are placed below.
	// This ensures companion overlays (e.g. a completion menu and its
	// detail panel) stay on the same side of the cursor regardless of
	// their individual heights.
	CursorGroup *CursorGroup
}

// CursorGroup links cursor-relative overlays so they share a single
// above/below decision. Create one with [NewCursorGroup] and assign it
// to the CursorGroup field of each linked overlay's [OverlayOptions].
type CursorGroup struct{}

// NewCursorGroup creates a new cursor group. Pointer identity is used
// to determine group membership.
func NewCursorGroup() *CursorGroup { return &CursorGroup{} }

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
// destroying and recreating the overlay. This avoids allocation churn for
// things like repositioning a completion menu on each keystroke.
func (h *OverlayHandle) SetOptions(opts *OverlayOptions) {
	h.tui.mu.Lock()
	h.entry.options = opts
	h.tui.mu.Unlock()
	h.tui.RequestRender(false)
}

// SetHidden temporarily hides or shows the overlay. Focus is not changed;
// the caller should manage focus explicitly via [TUI.SetFocus].
func (h *OverlayHandle) SetHidden(hidden bool) {
	h.tui.mu.Lock()
	if h.entry.hidden == hidden {
		h.tui.mu.Unlock()
		return
	}
	h.entry.hidden = hidden
	h.tui.mu.Unlock()
	h.tui.RequestRender(false)
}

// IsHidden reports whether the overlay is temporarily hidden.
func (h *OverlayHandle) IsHidden() bool {
	h.tui.mu.Lock()
	defer h.tui.mu.Unlock()
	return h.entry.hidden
}

type overlayEntry struct {
	component Component
	options   *OverlayOptions
	hidden    bool
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
	t.mu.Unlock()
	if !found {
		return
	}
	t.RequestRender(false)
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

// cursorFitsAbove reports whether an overlay of the given height fits above
// the cursor row.
func cursorFitsAbove(cursor *CursorPos, overlayH int) bool {
	return cursor.Row-overlayH >= 0
}

// resolveCursorPosition computes the (row, col) for a cursor-relative overlay
// in content coordinate space. Row is not clamped to content height since the
// compositing code extends the working area as needed.
//
// The above parameter determines whether the overlay is placed above or below
// the cursor. The caller is responsible for the above/below decision (possibly
// influenced by CursorGroup).
//
// Horizontal positioning: if Col is explicitly set, it is used directly
// (ignoring cursor column and OffsetX). This avoids jitter from the cursor
// position and OffsetX being updated on different goroutines. If Col is not
// set, the column defaults to cursor.Col + OffsetX.
func resolveCursorPosition(opts *OverlayOptions, cursor *CursorPos, overlayH int, above bool) (row, col int) {
	if opts.Col.isSet {
		col = opts.Col.abs
	} else {
		col = max(0, cursor.Col+opts.OffsetX)
	}

	if above {
		row = cursor.Row - overlayH // bottom edge at cursor.Row - 1
	} else {
		row = cursor.Row + 1 // top edge at cursor.Row + 1
	}
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
