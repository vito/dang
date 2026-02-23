package main

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/pitui"
)

// docBrowserOverlay wraps the existing doc browser data model as a pitui
// overlay component.
type docBrowserOverlay struct {
	pitui.Compo
	columns    []docColumn
	activeCol  int
	filtering  bool
	onExit     func()
	lastHeight int // cached from most recent Render; used by key handlers

	// Mouse state — populated during Render for hit-testing in HandleMouse.
	layout docBrowserLayout // layout from most recent Render
	hoverCol  int           // column index under mouse, or -1
	hoverItem int           // item index (within visible()) under mouse, or -1
}

// docBrowserLayout captures the spatial layout from the most recent render
// so HandleMouse can map terminal coordinates to items.
type docBrowserLayout struct {
	visStart int // first visible column index
	visEnd   int // one past last visible column index
	colW     int // width of each column (except possibly the last)
	lastColW int // width of the last visible column
	sepW     int // width of each separator (" │ " = 3)
	listH    int // height of the item list area

	// Per visible column: the Y offset within the doc browser's output
	// where items start (after title, separator, and optional filter).
	colItemStartY []int
	// Per visible column: the X offset where the column starts.
	colStartX []int
	// Per visible column: number of visible items rendered.
	colItemCount []int
	// Per visible column: scroll offset.
	colOffset []int
	// Per visible column: the column index in d.columns.
	colIndex []int
}

func newDocBrowserOverlay(typeEnv dang.Env) *docBrowserOverlay {
	root := buildColumn("(root)", "Top-level scope", typeEnv)
	db := &docBrowserOverlay{
		columns:   []docColumn{root},
		hoverCol:  -1,
		hoverItem: -1,
	}
	db.expandSelection()
	return db
}

func (d *docBrowserOverlay) HandleKeyPress(_ pitui.EventContext, ev uv.KeyPressEvent) bool {
	defer d.Update()
	// Clear hover state on keyboard navigation.
	d.hoverCol = -1
	d.hoverItem = -1
	key := uv.Key(ev)
	if d.filtering {
		d.handleFilterKey(key)
	} else {
		d.handleKey(key)
	}
	return true // doc browser consumes all keys when focused
}

// HandleMouse implements pitui.MouseEnabled, enabling terminal mouse capture
// while the doc browser is mounted. Supports hover highlighting and click
// navigation on all column items.
func (d *docBrowserOverlay) HandleMouse(_ pitui.EventContext, ev pitui.MouseEvent) bool {
	m := ev.Mouse()

	col, item := d.hitTest(ev.Col, ev.Row)

	switch ev.MouseEvent.(type) {
	case uv.MouseMotionEvent:
		if col != d.hoverCol || item != d.hoverItem {
			d.hoverCol = col
			d.hoverItem = item
			d.Update()
		}
	case uv.MouseClickEvent:
		if col >= 0 && item >= 0 {
			ci := col
			colIdx := d.layout.colIndex[ci]
			c := &d.columns[colIdx]
			vis := c.visible()
			absItem := d.layout.colOffset[ci] + item
			if absItem < len(vis) {
				// Switch active column and select the clicked item.
				d.activeCol = colIdx
				c.index = absItem
				d.clampScroll(c)
				d.expandSelection()
				d.Update()
			}
		}
	case uv.MouseWheelEvent:
		if col >= 0 {
			colIdx := d.layout.colIndex[col]
			c := &d.columns[colIdx]
			vis := c.visible()
			if len(c.items) > 0 {
				switch m.Button {
				case uv.MouseWheelUp:
					if c.index > 0 {
						c.index--
						d.activeCol = colIdx
						d.clampScroll(c)
						d.expandSelection()
						d.Update()
					}
				case uv.MouseWheelDown:
					if c.index < len(vis)-1 {
						c.index++
						d.activeCol = colIdx
						d.clampScroll(c)
						d.expandSelection()
						d.Update()
					}
				}
			} else {
				// Detail pane — scroll detail content.
				switch m.Button {
				case uv.MouseWheelUp:
					if c.detailOffset > 0 {
						c.detailOffset--
						d.Update()
					}
				case uv.MouseWheelDown:
					c.detailOffset++
					d.Update()
				}
			}
		}
	}

	return true // consume all mouse events when focused
}

// hitTest maps terminal (x, y) coordinates to (visible column index, item
// index within that column's rendered items). Returns (-1, -1) if the
// coordinates don't land on an item.
func (d *docBrowserOverlay) hitTest(x, y int) (col int, item int) {
	lay := &d.layout
	if len(lay.colStartX) == 0 {
		return -1, -1
	}

	// Find which column the X coordinate falls in.
	col = -1
	for ci := range lay.colStartX {
		w := lay.colW
		if ci == len(lay.colStartX)-1 {
			w = lay.lastColW
		}
		if x >= lay.colStartX[ci] && x < lay.colStartX[ci]+w {
			col = ci
			break
		}
	}
	if col < 0 {
		return -1, -1
	}

	// Check Y is within the item area.
	itemStartY := lay.colItemStartY[col]
	itemIdx := y - itemStartY
	if itemIdx < 0 || itemIdx >= lay.colItemCount[col] {
		return col, -1
	}

	return col, itemIdx
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

func (d *docBrowserOverlay) Render(ctx pitui.RenderContext) pitui.RenderResult {
	width := ctx.Width
	height := ctx.Height
	if height == 0 && ctx.ScreenHeight > 0 {
		height = ctx.ScreenHeight
	}
	if width < 20 {
		return pitui.RenderResult{Lines: []string{"(too narrow)"}}
	}

	// Cache height for key handlers (clampScroll, page up/down).
	if height > 0 {
		d.lastHeight = height
	}
	listH := max(height-4, 5)

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	activeTitle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	docTextStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("249"))
	argNameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	argTypeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	dimSt := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	sep := sepStyle.Render(" │ ")

	visStart, visEnd := d.visibleRange()
	numVis := max(visEnd-visStart, 1)
	sepW := 3 * (numVis - 1)
	colW := max((width-sepW)/numVis, 15)
	lastColW := max(width-sepW-colW*(numVis-1), colW)

	hoverStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("237"))

	// Build layout info for mouse hit-testing.
	lay := docBrowserLayout{
		visStart: visStart,
		visEnd:   visEnd,
		colW:     colW,
		lastColW: lastColW,
		sepW:     3,
		listH:    listH,
	}

	var colRendered [][]string
	xOffset := 0
	for ci := visStart; ci < visEnd; ci++ {
		col := d.columns[ci]
		visIdx := ci - visStart // index into colRendered
		w := colW
		if ci == visEnd-1 {
			w = lastColW
		}
		isActive := ci == d.activeCol
		isFiltering := d.filtering && isActive

		lay.colStartX = append(lay.colStartX, xOffset)
		lay.colIndex = append(lay.colIndex, ci)

		var lines []string

		t := col.title
		if isActive {
			lines = append(lines, activeTitle.Render(truncate(t, w)))
		} else {
			lines = append(lines, titleStyle.Render(truncate(t, w)))
		}
		lines = append(lines, sepStyle.Render(strings.Repeat("─", w)))

		vis := col.visible()
		filterLineH := 0
		if len(col.items) > 0 && (isFiltering || col.filter != "") {
			filterLineH = 1
			filterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
			filterText := "/" + col.filter
			if isFiltering {
				filterText += "_"
			}
			countText := dimSt.Render(fmt.Sprintf(" %d/%d", len(vis), len(col.items)))
			countW := lipgloss.Width(countText)
			filterDisp := filterStyle.Render(truncate(filterText, w-countW))
			dispW := lipgloss.Width(filterDisp)
			gap := max(w-dispW-countW, 0)
			lines = append(lines, filterDisp+strings.Repeat(" ", gap)+countText)
		}

		// Items start at: breadcrumb (1) + title (1) + sep (1) + filter (0 or 1) = 3 + filterLineH
		// in the final output. Within colRendered, it's 2 + filterLineH.
		lay.colItemStartY = append(lay.colItemStartY, 1+2+filterLineH) // +1 for breadcrumb row
		lay.colOffset = append(lay.colOffset, col.offset)

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

				// Determine if this item is hovered.
				isHovered := d.hoverCol == visIdx && d.hoverItem == (i-col.offset)

				if i == col.index && isActive {
					leftStyled := selectedStyle.Render(prefix+label) + strings.Repeat(" ", gap) + tagStyled
					lines = append(lines, leftStyled)
				} else if isHovered {
					leftStyled := hoverStyle.Render(prefix+label+strings.Repeat(" ", gap)+tag)
					lines = append(lines, leftStyled)
				} else {
					raw := leftPart + strings.Repeat(" ", gap) + tagStyled
					lines = append(lines, raw)
				}
				itemCount++
			}
		} else if ci > 0 {
			prevCol := &d.columns[ci-1]
			if item, ok := prevCol.selectedItem(); ok {
				detailContent := d.renderDetailContent(item, w, docTextStyle, argNameStyle, argTypeStyle, dimSt)
				contentH := listH
				dOffset := min(col.detailOffset, len(detailContent))
				end := min(dOffset+contentH, len(detailContent))
				lines = append(lines, detailContent[dOffset:end]...)
			}
		}
		lay.colItemCount = append(lay.colItemCount, itemCount)

		for len(lines) < listH+2 {
			lines = append(lines, "")
		}

		colRendered = append(colRendered, lines)
		xOffset += w
		if visIdx < numVis-1 {
			xOffset += 3 // separator width
		}
	}

	d.layout = lay

	totalLines := listH + 2
	var rows []string
	for i := range totalLines {
		var parts []string
		for ci, cl := range colRendered {
			w := colW
			if ci == len(colRendered)-1 {
				w = lastColW
			}
			parts = append(parts, padRight(getLine(cl, i), w))
		}
		rows = append(rows, strings.Join(parts, sep))
	}

	var crumbs []string
	for i := 0; i <= d.activeCol; i++ {
		crumbs = append(crumbs, d.columns[i].title)
	}
	breadcrumb := dimSt.Render(strings.Join(crumbs, " › "))
	help := dimSt.Render("Up/Down/hjkl navigate | Click/scroll | / filter | Tab cycle | q/Esc exit")

	var result []string
	result = append(result, breadcrumb)
	result = append(result, rows...)
	result = append(result, help)

	// Truncate lines to width.
	for i, line := range result {
		if pitui.VisibleWidth(line) > width {
			result[i] = pitui.Truncate(line, width, "")
		}
	}

	return pitui.RenderResult{Lines: result}
}

func (d *docBrowserOverlay) renderDetailContent(item docItem, w int, docStyle, argNameStyle, argTypeStyle, dimSt lipgloss.Style) []string {
	return renderDocDetail(item, w, docStyle, argNameStyle, argTypeStyle, dimSt)
}

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
	h := max(d.lastHeight-4, 5)
	return h
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
