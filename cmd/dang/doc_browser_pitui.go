package main

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/dang/pkg/dang"
	"codeberg.org/vito/tuist"
)

// ── styles (shared across components) ───────────────────────────────────────

var (
	docTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	docActiveTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	docSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	docTextStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("249"))
	docArgNameStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	docArgTypeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	docDimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	docSepStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	docHoverStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("237"))
)

// ── breadcrumb crumb ────────────────────────────────────────────────────────

// breadcrumbCrumb is a small inline component representing a single
// breadcrumb segment. It implements MouseEnabled and Hoverable so
// tuist's positional dispatch handles hover and click automatically.
type breadcrumbCrumb struct {
	tuist.Compo
	label   string
	active  bool // true for the rightmost (current) crumb
	hovered bool
	onClick func()
}

func (b *breadcrumbCrumb) Render(_ tuist.RenderContext) tuist.RenderResult {
	var st lipgloss.Style
	switch {
	case b.active:
		st = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	case b.hovered:
		st = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Underline(true)
	default:
		st = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Underline(true)
	}
	return tuist.RenderResult{Lines: []string{st.Render(b.label)}}
}

func (b *breadcrumbCrumb) HandleMouse(_ tuist.EventContext, ev tuist.MouseEvent) bool {
	switch ev.MouseEvent.(type) {
	case uv.MouseClickEvent:
		if b.onClick != nil {
			b.onClick()
		}
		return true
	}
	return false
}

func (b *breadcrumbCrumb) SetHovered(_ tuist.EventContext, hovered bool) {
	if b.hovered != hovered {
		b.hovered = hovered
		b.Update()
	}
}

// ── column component ────────────────────────────────────────────────────────

// docColumnComp renders a single doc browser column and handles mouse
// interaction (click, scroll, hover) via tuist's positional dispatch.
type docColumnComp struct {
	tuist.Compo
	browser  *docBrowserOverlay
	colIdx   int // index into browser.columns
	isActive bool
	hovered  bool
	hoverRow int // item row under mouse (0-based within visible items), or -1

	// Layout info set during Render for mouse coordinate mapping.
	itemStartRow int // row offset where items begin in this column's output
	itemCount    int // number of rendered items
	scrollOffset int // first visible item index


}

func (c *docColumnComp) Render(ctx tuist.RenderContext) tuist.RenderResult {
	w := ctx.Width
	col := c.browser.columns[c.colIdx]
	isFiltering := c.browser.filtering && c.isActive

	var lines []string

	// Title
	t := col.title
	if c.isActive {
		lines = append(lines, docActiveTitle.Render(truncate(t, w)))
	} else {
		lines = append(lines, docTitleStyle.Render(truncate(t, w)))
	}
	lines = append(lines, docSepStyle.Render(strings.Repeat("─", w)))

	vis := col.visible()
	filterLineH := 0
	if len(col.items) > 0 && (isFiltering || col.filter != "") {
		filterLineH = 1
		filterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
		filterText := "/" + col.filter
		if isFiltering {
			filterText += "_"
		}
		countText := docDimStyle.Render(fmt.Sprintf(" %d/%d", len(vis), len(col.items)))
		countW := lipgloss.Width(countText)
		filterDisp := filterStyle.Render(truncate(filterText, w-countW))
		dispW := lipgloss.Width(filterDisp)
		gap := max(w-dispW-countW, 0)
		lines = append(lines, filterDisp+strings.Repeat(" ", gap)+countText)
	}

	c.itemStartRow = 2 + filterLineH // title + separator + optional filter
	c.scrollOffset = col.offset

	listH := ctx.Height - 2 // minus title and separator
	if listH < 1 {
		listH = 5
	}
	itemListH := listH - filterLineH
	itemCount := 0

	if len(col.items) > 0 {
		end := min(col.offset+itemListH, len(vis))
		for i := col.offset; i < end; i++ {
			item := vis[i]
			label := item.name
			if len(item.args) > 0 {
				label += "(...)"
			}

			tag := item.kind.label()
			tagStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(item.kind.color())).Render(tag)
			tagW := lipgloss.Width(tagStyled)

			maxLabel := max(w-3-tagW, 4)
			label = truncate(label, maxLabel)

			prefix := "  "
			if i == col.index {
				prefix = "▸ "
			}
			leftPart := prefix + label
			leftW := lipgloss.Width(leftPart)
			gap := max(w-leftW-tagW, 1)

			isHovered := c.hovered && c.hoverRow == (i-col.offset)

			if i == col.index && c.isActive {
				leftStyled := docSelectedStyle.Render(prefix+label) + strings.Repeat(" ", gap) + tagStyled
				lines = append(lines, leftStyled)
			} else if isHovered {
				leftStyled := docHoverStyle.Render(prefix + label + strings.Repeat(" ", gap) + tag)
				lines = append(lines, leftStyled)
			} else {
				raw := leftPart + strings.Repeat(" ", gap) + tagStyled
				lines = append(lines, raw)
			}
			itemCount++
		}
	} else if c.colIdx > 0 {
		prevCol := &c.browser.columns[c.colIdx-1]
		if item, ok := prevCol.selectedItem(); ok {
			detailContent := renderDocDetail(item, w, docTextStyle, docArgNameStyle, docArgTypeStyle, docDimStyle)
			contentH := listH
			dOffset := min(col.detailOffset, len(detailContent))
			end := min(dOffset+contentH, len(detailContent))
			lines = append(lines, detailContent[dOffset:end]...)
		}
	}
	c.itemCount = itemCount

	// Pad to full height and full width so that zone markers (added by
	// RenderChild/markLines) span the entire column rectangle.
	for len(lines) < listH+2 {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = padRight(line, w)
	}

	return tuist.RenderResult{Lines: lines}
}

func (c *docColumnComp) HandleMouse(_ tuist.EventContext, ev tuist.MouseEvent) bool {
	col := &c.browser.columns[c.colIdx]

	// Map zone-relative row to item index.
	itemRow := ev.Row - c.itemStartRow
	isOnItem := itemRow >= 0 && itemRow < c.itemCount

	switch e := ev.MouseEvent.(type) {
	case uv.MouseMotionEvent:
		_ = e
		newHover := -1
		if isOnItem {
			newHover = itemRow
		}
		if c.hoverRow != newHover {
			c.hoverRow = newHover
			c.Update()
		}
		return true

	case uv.MouseClickEvent:
		if isOnItem {
			absItem := c.scrollOffset + itemRow
			vis := col.visible()
			if absItem < len(vis) {
				c.browser.activeCol = c.colIdx
				col.index = absItem
				c.browser.clampScroll(col)
				c.browser.expandSelection()
				c.browser.Update()
			}
		}
		return true

	case uv.MouseWheelEvent:
		m := ev.Mouse()
		vis := col.visible()
		if len(col.items) > 0 {
			switch m.Button {
			case uv.MouseWheelUp:
				if col.index > 0 {
					col.index--
					c.browser.activeCol = c.colIdx
					c.browser.clampScroll(col)
					c.browser.expandSelection()
					c.browser.Update()
				}
			case uv.MouseWheelDown:
				if col.index < len(vis)-1 {
					col.index++
					c.browser.activeCol = c.colIdx
					c.browser.clampScroll(col)
					c.browser.expandSelection()
					c.browser.Update()
				}
			}
		} else {
			// Detail pane — scroll detail content.
			switch m.Button {
			case uv.MouseWheelUp:
				if col.detailOffset > 0 {
					col.detailOffset--
					c.browser.Update()
				}
			case uv.MouseWheelDown:
				col.detailOffset++
				c.browser.Update()
			}
		}
		return true
	}

	return false
}

func (c *docColumnComp) SetHovered(_ tuist.EventContext, hovered bool) {
	if c.hovered != hovered {
		c.hovered = hovered
		if !hovered {
			c.hoverRow = -1
		}
		c.Update()
	}
}

// ── doc browser overlay ─────────────────────────────────────────────────────

// docBrowserOverlay wraps the doc browser data model as a tuist component.
// It is Interactive (handles keyboard input) but NOT MouseEnabled — mouse
// interaction is handled by individual breadcrumb and column sub-components
// via tuist's positional dispatch.
type docBrowserOverlay struct {
	tuist.Compo
	columns    []docColumn
	activeCol  int
	filtering  bool
	onExit     func()
	lastHeight int // cached from most recent Render; used by key handlers

	// Sub-components for mouse interaction. Managed during Render.
	crumbs  []*breadcrumbCrumb
	colComps []*docColumnComp
}

func newDocBrowserOverlay(typeEnv dang.Env) *docBrowserOverlay {
	root := buildColumn("(root)", "Top-level scope", typeEnv)
	db := &docBrowserOverlay{
		columns: []docColumn{root},
	}
	db.expandSelection()
	return db
}

func (d *docBrowserOverlay) HandleKeyPress(_ tuist.EventContext, ev uv.KeyPressEvent) bool {
	defer d.Update()
	key := uv.Key(ev)
	if d.filtering {
		d.handleFilterKey(key)
	} else {
		d.handleKey(key)
	}
	return true
}

func (d *docBrowserOverlay) handleKey(key uv.Key) {
	switch {
	case key.Text == "q" || key.Code == uv.KeyEscape:
		if d.onExit != nil {
			d.onExit()
		}
	case key.Text == "/":
		col := &d.columns[d.activeCol]
		if len(col.items) > 0 {
			d.filtering = true
		}
	case key.Code == uv.KeyLeft || key.Text == "h":
		if d.activeCol > 0 {
			d.columns[d.activeCol].filter = ""
			d.columns[d.activeCol].applyFilter()
			d.filtering = false
			d.activeCol--
			d.expandSelection()
		}
	case key.Code == uv.KeyRight || key.Text == "l" || key.Code == uv.KeyEnter:
		for i := d.activeCol + 1; i < len(d.columns); i++ {
			if len(d.columns[i].items) > 0 {
				d.activeCol = i
				d.expandSelection()
				break
			}
		}
	case key.Code == uv.KeyUp || key.Text == "k":
		col := &d.columns[d.activeCol]
		if col.index > 0 {
			col.index--
			d.clampScroll(col)
			d.expandSelection()
		}
	case key.Code == uv.KeyDown || key.Text == "j":
		col := &d.columns[d.activeCol]
		vis := col.visible()
		if col.index < len(vis)-1 {
			col.index++
			d.clampScroll(col)
			d.expandSelection()
		}
	case key.Code == uv.KeyTab:
		start := d.activeCol
		for {
			d.activeCol = (d.activeCol + 1) % len(d.columns)
			if len(d.columns[d.activeCol].items) > 0 || d.activeCol == start {
				break
			}
		}
	}
}

func (d *docBrowserOverlay) handleFilterKey(key uv.Key) {
	switch key.Code {
	case uv.KeyEscape:
		col := &d.columns[d.activeCol]
		col.filter = ""
		col.applyFilter()
		d.filtering = false
		d.expandSelection()
	case uv.KeyEnter:
		d.filtering = false
	case uv.KeyBackspace:
		col := &d.columns[d.activeCol]
		if len(col.filter) > 0 {
			col.filter = col.filter[:len(col.filter)-1]
			col.applyFilter()
			d.expandSelection()
		} else {
			d.filtering = false
		}
	case uv.KeyUp:
		col := &d.columns[d.activeCol]
		if col.index > 0 {
			col.index--
			d.clampScroll(col)
			d.expandSelection()
		}
	case uv.KeyDown:
		col := &d.columns[d.activeCol]
		vis := col.visible()
		if col.index < len(vis)-1 {
			col.index++
			d.clampScroll(col)
			d.expandSelection()
		}
	default:
		if key.Text != "" {
			col := &d.columns[d.activeCol]
			col.filter += key.Text
			col.applyFilter()
			d.expandSelection()
		}
	}
}

func (d *docBrowserOverlay) Render(ctx tuist.RenderContext) tuist.RenderResult {
	width := ctx.Width
	height := ctx.Height
	if height == 0 && ctx.ScreenHeight > 0 {
		height = ctx.ScreenHeight
	}
	if width < 20 {
		return tuist.RenderResult{Lines: []string{"(too narrow)"}}
	}

	if height > 0 {
		d.lastHeight = height
	}
	listH := max(height-4, 5)

	sep := docSepStyle.Render(" │ ")

	visStart, visEnd := d.visibleRange()
	numVis := max(visEnd-visStart, 1)
	sepW := 3 * (numVis - 1)
	colW := max((width-sepW)/numVis, 15)
	lastColW := max(width-sepW-colW*(numVis-1), colW)

	// ── Sync column components ──────────────────────────────────────────

	d.syncColComps(visStart, visEnd)
	for ci, cc := range d.colComps {
		cc.colIdx = visStart + ci
		cc.isActive = cc.colIdx == d.activeCol
		cc.Update()
	}

	// ── Render columns via RenderChild (auto-marks with zone markers) ───

	var colRendered [][]string
	for ci, cc := range d.colComps {
		w := colW
		if ci == len(d.colComps)-1 {
			w = lastColW
		}
		r := d.RenderChild(cc, tuist.RenderContext{Width: w, Height: listH + 2, ScreenHeight: height})
		colRendered = append(colRendered, r.Lines)
	}

	// ── Stitch columns side by side ─────────────────────────────────────

	totalLines := listH + 2
	var rows []string
	for i := range totalLines {
		var parts []string
		for _, cl := range colRendered {
			parts = append(parts, getLine(cl, i))
		}
		rows = append(rows, strings.Join(parts, sep))
	}

	// ── Breadcrumbs ─────────────────────────────────────────────────────

	d.syncCrumbs()
	var crumbParts []string
	for i, c := range d.crumbs {
		if i > 0 {
			crumbParts = append(crumbParts, docDimStyle.Render(" › "))
		}
		c.active = i == d.activeCol
		c.label = d.columns[i].title
		c.Update()
		r := d.RenderChild(c, tuist.RenderContext{Width: width})
		text := ""
		if len(r.Lines) > 0 {
			text = r.Lines[0]
		}
		crumbParts = append(crumbParts, text)
	}
	breadcrumb := strings.Join(crumbParts, "")

	// ── Assemble final output ───────────────────────────────────────────

	help := docDimStyle.Render("Up/Down/hjkl navigate | Click/scroll | / filter | Tab cycle | q/Esc exit")

	var result []string
	result = append(result, breadcrumb)
	result = append(result, rows...)
	result = append(result, help)

	for i, line := range result {
		if tuist.VisibleWidth(line) > width {
			result[i] = tuist.Truncate(line, width, "")
		}
	}

	return tuist.RenderResult{Lines: result}
}

// ── sub-component sync ──────────────────────────────────────────────────────

// syncCrumbs ensures d.crumbs has exactly activeCol+1 entries.
func (d *docBrowserOverlay) syncCrumbs() {
	need := d.activeCol + 1
	for len(d.crumbs) > need {
		d.crumbs = d.crumbs[:len(d.crumbs)-1]
	}
	for len(d.crumbs) < need {
		idx := len(d.crumbs)
		c := &breadcrumbCrumb{}
		c.onClick = func() {
			if idx < d.activeCol {
				d.activeCol = idx
				d.expandSelection()
				d.Update()
			}
		}
		d.crumbs = append(d.crumbs, c)
	}
}

// syncColComps ensures d.colComps has exactly visEnd-visStart entries.
func (d *docBrowserOverlay) syncColComps(visStart, visEnd int) {
	need := visEnd - visStart
	for len(d.colComps) > need {
		d.colComps = d.colComps[:len(d.colComps)-1]
	}
	for len(d.colComps) < need {
		cc := &docColumnComp{browser: d, hoverRow: -1}
		d.colComps = append(d.colComps, cc)
	}
}

// ── detail rendering ────────────────────────────────────────────────────────

// renderDocDetail renders structured documentation for a docItem. Shared by
// the doc browser detail column and the completion detail bubble.
func renderDocDetail(item docItem, w int, docStyle, argNameStyle, argTypeStyle, dimSt lipgloss.Style) []string {
	var lines []string

	if item.typeStr != "" {
		lines = append(lines, argTypeStyle.Render(truncate(item.typeStr, w)))
		lines = append(lines, "")
	}

	if item.doc != "" {
		wrapped := wordWrap(item.doc, w)
		for line := range strings.SplitSeq(wrapped, "\n") {
			lines = append(lines, docStyle.Render(line))
		}
		lines = append(lines, "")
	}

	if len(item.args) > 0 {
		lines = append(lines, "Arguments:")
		for _, arg := range item.args {
			lines = append(lines, fmt.Sprintf("  %s %s",
				argNameStyle.Render(arg.name+":"),
				argTypeStyle.Render(arg.typeStr),
			))
			if arg.doc != "" {
				wrapped := wordWrap(arg.doc, w-4)
				for line := range strings.SplitSeq(wrapped, "\n") {
					lines = append(lines, "    "+dimSt.Render(line))
				}
			}
		}
	}

	if len(item.blockArgs) > 0 {
		lines = append(lines, "")
		blockHeader := "Block:"
		if item.blockRet != "" {
			blockHeader = fmt.Sprintf("Block -> %s:", argTypeStyle.Render(item.blockRet))
		}
		lines = append(lines, blockHeader)
		for _, arg := range item.blockArgs {
			lines = append(lines, fmt.Sprintf("  %s %s",
				argNameStyle.Render(arg.name+":"),
				argTypeStyle.Render(arg.typeStr),
			))
			if arg.doc != "" {
				wrapped := wordWrap(arg.doc, w-4)
				for line := range strings.SplitSeq(wrapped, "\n") {
					lines = append(lines, "    "+dimSt.Render(line))
				}
			}
		}
	}

	if len(lines) <= 1 && item.doc == "" && len(item.args) == 0 && len(item.blockArgs) == 0 {
		lines = append(lines, dimSt.Render("(no documentation)"))
	}

	return lines
}

// ── navigation ──────────────────────────────────────────────────────────────

func (d *docBrowserOverlay) expandSelection() {
	d.columns = d.columns[:d.activeCol+1]
	col := &d.columns[d.activeCol]
	item, ok := col.selectedItem()
	if !ok {
		return
	}

	detail := buildDetailColumn(item)
	d.columns = append(d.columns, detail)

	if item.retEnv != nil {
		members := buildColumn(item.name+" -> "+item.retEnv.Name(), item.retEnv.GetModuleDocString(), item.retEnv)
		if len(members.items) > 0 {
			d.columns = append(d.columns, members)
		}
	}
}

func (d *docBrowserOverlay) clampScroll(col *docColumn) {
	h := d.listHeight()
	if col.index < col.offset {
		col.offset = col.index
	}
	if col.index >= col.offset+h {
		col.offset = col.index - h + 1
	}
}

func (d *docBrowserOverlay) listHeight() int {
	return max(d.lastHeight-4, 5)
}

func (d *docBrowserOverlay) visibleRange() (int, int) {
	maxCols := 3
	total := len(d.columns)
	if total <= maxCols {
		return 0, total
	}
	start := max(d.activeCol-1, 0)
	end := start + maxCols
	if end > total {
		end = total
		start = max(end-maxCols, 0)
	}
	return start, end
}
