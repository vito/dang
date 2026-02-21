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
}

func newDocBrowserOverlay(typeEnv dang.Env) *docBrowserOverlay {
	root := buildColumn("(root)", "Top-level scope", typeEnv)
	db := &docBrowserOverlay{
		columns: []docColumn{root},
	}
	db.expandSelection()
	return db
}

func (d *docBrowserOverlay) HandleKeyPress(_ pitui.EventContext, ev uv.KeyPressEvent) {
	defer d.Update()
	key := uv.Key(ev)
	if d.filtering {
		d.handleFilterKey(key)
	} else {
		d.handleKey(key)
	}
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
	switch {
	case key.Code == uv.KeyEscape:
		col := &d.columns[d.activeCol]
		col.filter = ""
		col.applyFilter()
		d.filtering = false
		d.expandSelection()
	case key.Code == uv.KeyEnter:
		d.filtering = false
	case key.Code == uv.KeyBackspace:
		col := &d.columns[d.activeCol]
		if len(col.filter) > 0 {
			col.filter = col.filter[:len(col.filter)-1]
			col.applyFilter()
			d.expandSelection()
		} else {
			d.filtering = false
		}
	case key.Code == uv.KeyUp:
		col := &d.columns[d.activeCol]
		if col.index > 0 {
			col.index--
			d.clampScroll(col)
			d.expandSelection()
		}
	case key.Code == uv.KeyDown:
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
	if width < 20 {
		return pitui.RenderResult{Lines: []string{"(too narrow)"}}
	}

	// Cache height for key handlers (clampScroll, page up/down).
	if height > 0 {
		d.lastHeight = height
	}
	listH := height - 4
	if listH < 5 {
		listH = 5
	}

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
	numVis := visEnd - visStart
	if numVis < 1 {
		numVis = 1
	}
	sepW := 3 * (numVis - 1)
	colW := (width - sepW) / numVis
	if colW < 15 {
		colW = 15
	}
	lastColW := width - sepW - colW*(numVis-1)
	if lastColW < colW {
		lastColW = colW
	}

	var colRendered [][]string
	for ci := visStart; ci < visEnd; ci++ {
		col := d.columns[ci]
		w := colW
		if ci == visEnd-1 {
			w = lastColW
		}
		isActive := ci == d.activeCol
		isFiltering := d.filtering && isActive

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
			gap := w - dispW - countW
			if gap < 0 {
				gap = 0
			}
			lines = append(lines, filterDisp+strings.Repeat(" ", gap)+countText)
		}

		itemListH := listH - filterLineH
		if len(col.items) > 0 {
			end := col.offset + itemListH
			if end > len(vis) {
				end = len(vis)
			}
			for i := col.offset; i < end; i++ {
				item := vis[i]
				label := item.name
				if len(item.args) > 0 {
					label += "(...)"
				}

				tag := item.kind.label()
				tagStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(item.kind.color())).Render(tag)
				tagW := lipgloss.Width(tagStyled)

				maxLabel := w - 3 - tagW
				if maxLabel < 4 {
					maxLabel = 4
				}
				label = truncate(label, maxLabel)

				prefix := "  "
				if i == col.index {
					prefix = "▸ "
				}
				leftPart := prefix + label
				leftW := lipgloss.Width(leftPart)
				gap := w - leftW - tagW
				if gap < 1 {
					gap = 1
				}

				if i == col.index && isActive {
					leftStyled := selectedStyle.Render(prefix+label) + strings.Repeat(" ", gap) + tagStyled
					lines = append(lines, leftStyled)
				} else {
					raw := leftPart + strings.Repeat(" ", gap) + tagStyled
					lines = append(lines, raw)
				}
			}
		} else if ci > 0 {
			prevCol := &d.columns[ci-1]
			if item, ok := prevCol.selectedItem(); ok {
				detailContent := d.renderDetailContent(item, w, docTextStyle, argNameStyle, argTypeStyle, dimSt)
				contentH := listH
				dOffset := col.detailOffset
				if dOffset > len(detailContent) {
					dOffset = len(detailContent)
				}
				end := dOffset + contentH
				if end > len(detailContent) {
					end = len(detailContent)
				}
				lines = append(lines, detailContent[dOffset:end]...)
			}
		}

		for len(lines) < listH+2 {
			lines = append(lines, "")
		}

		colRendered = append(colRendered, lines)
	}

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
	help := dimSt.Render("Up/Down/hjkl navigate | / filter | Tab cycle | q/Esc exit")

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
		for _, line := range strings.Split(wrapped, "\n") {
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
				for _, line := range strings.Split(wrapped, "\n") {
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
				for _, line := range strings.Split(wrapped, "\n") {
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
	h := d.lastHeight - 4
	if h < 5 {
		h = 5
	}
	return h
}

func (d *docBrowserOverlay) visibleRange() (int, int) {
	maxCols := 3
	total := len(d.columns)
	if total <= maxCols {
		return 0, total
	}
	start := d.activeCol - 1
	if start < 0 {
		start = 0
	}
	end := start + maxCols
	if end > total {
		end = total
		start = end - maxCols
		if start < 0 {
			start = 0
		}
	}
	return start, end
}
