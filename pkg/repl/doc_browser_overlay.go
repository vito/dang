package repl

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/tuist"
)

// ── styles ──────────────────────────────────────────────────────────────────

var (
	DocTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	DocActiveTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	DocSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	DocDocTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("249"))
	DocArgNameStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	DocArgTypeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	DocDimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	DocSepStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	DocHoverStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("237"))
)

// ── breadcrumb crumb ────────────────────────────────────────────────────────

type BreadcrumbCrumb struct {
	tuist.Compo
	Label   string
	Active  bool
	Hovered bool
	OnClick func()
}

func (b *BreadcrumbCrumb) Render(_ tuist.RenderContext) tuist.RenderResult {
	var st lipgloss.Style
	switch {
	case b.Active:
		st = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	case b.Hovered:
		st = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Underline(true)
	default:
		st = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Underline(true)
	}
	return tuist.RenderResult{Lines: []string{st.Render(b.Label)}}
}

func (b *BreadcrumbCrumb) HandleMouse(_ tuist.EventContext, ev tuist.MouseEvent) bool {
	switch ev.MouseEvent.(type) {
	case uv.MouseClickEvent:
		if b.OnClick != nil {
			b.OnClick()
		}
		return true
	}
	return false
}

func (b *BreadcrumbCrumb) SetHovered(_ tuist.EventContext, hovered bool) {
	if b.Hovered != hovered {
		b.Hovered = hovered
		b.Update()
	}
}

// ── column component ────────────────────────────────────────────────────────

type DocColumnComp struct {
	tuist.Compo
	Browser  *DocBrowserOverlay
	ColIdx   int
	IsActive bool
	Hovered  bool
	HoverRow int

	ItemStartRow int
	ItemCount    int
	ScrollOffset int
}

func (c *DocColumnComp) Render(ctx tuist.RenderContext) tuist.RenderResult {
	w := ctx.Width
	col := c.Browser.Columns[c.ColIdx]
	isFiltering := c.Browser.Filtering && c.IsActive

	var lines []string

	t := col.Title
	if c.IsActive {
		lines = append(lines, DocActiveTitle.Render(Truncate(t, w)))
	} else {
		lines = append(lines, DocTitleStyle.Render(Truncate(t, w)))
	}
	lines = append(lines, DocSepStyle.Render(strings.Repeat("─", w)))

	vis := col.Visible()
	filterLineH := 0
	if len(col.Items) > 0 && (isFiltering || col.Filter != "") {
		filterLineH = 1
		filterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
		filterText := "/" + col.Filter
		if isFiltering {
			filterText += "_"
		}
		countText := DocDimStyle.Render(fmt.Sprintf(" %d/%d", len(vis), len(col.Items)))
		countW := lipgloss.Width(countText)
		filterDisp := filterStyle.Render(Truncate(filterText, w-countW))
		dispW := lipgloss.Width(filterDisp)
		gap := max(w-dispW-countW, 0)
		lines = append(lines, filterDisp+strings.Repeat(" ", gap)+countText)
	}

	c.ItemStartRow = 2 + filterLineH
	c.ScrollOffset = col.Offset

	listH := ctx.Height - 2
	if listH < 1 {
		listH = 5
	}
	itemListH := listH - filterLineH
	itemCount := 0

	if len(col.Items) > 0 {
		end := min(col.Offset+itemListH, len(vis))
		for i := col.Offset; i < end; i++ {
			item := vis[i]
			label := item.Name
			if len(item.Args) > 0 {
				label += "(...)"
			}

			tag := item.Kind.Label()
			tagStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(item.Kind.Color())).Render(tag)
			tagW := lipgloss.Width(tagStyled)

			maxLabel := max(w-3-tagW, 4)
			label = Truncate(label, maxLabel)

			prefix := "  "
			if i == col.Index {
				prefix = "▸ "
			}
			leftPart := prefix + label
			leftW := lipgloss.Width(leftPart)
			gap := max(w-leftW-tagW, 1)

			isHovered := c.Hovered && c.HoverRow == (i-col.Offset)

			if i == col.Index && c.IsActive {
				leftStyled := DocSelectedStyle.Render(prefix+label) + strings.Repeat(" ", gap) + tagStyled
				lines = append(lines, leftStyled)
			} else if isHovered {
				leftStyled := DocHoverStyle.Render(prefix + label + strings.Repeat(" ", gap) + tag)
				lines = append(lines, leftStyled)
			} else {
				raw := leftPart + strings.Repeat(" ", gap) + tagStyled
				lines = append(lines, raw)
			}
			itemCount++
		}
	} else if c.ColIdx > 0 {
		prevCol := &c.Browser.Columns[c.ColIdx-1]
		if item, ok := prevCol.SelectedItem(); ok {
			detailContent := RenderDocDetail(item, w, DocDocTextStyle, DocArgNameStyle, DocArgTypeStyle, DocDimStyle)
			contentH := listH
			dOffset := min(col.DetailOffset, len(detailContent))
			end := min(dOffset+contentH, len(detailContent))
			lines = append(lines, detailContent[dOffset:end]...)
		}
	}
	c.ItemCount = itemCount

	for len(lines) < listH+2 {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = PadRight(line, w)
	}

	return tuist.RenderResult{Lines: lines}
}

func (c *DocColumnComp) HandleMouse(_ tuist.EventContext, ev tuist.MouseEvent) bool {
	col := &c.Browser.Columns[c.ColIdx]

	itemRow := ev.Row - c.ItemStartRow
	isOnItem := itemRow >= 0 && itemRow < c.ItemCount

	switch e := ev.MouseEvent.(type) {
	case uv.MouseMotionEvent:
		_ = e
		newHover := -1
		if isOnItem {
			newHover = itemRow
		}
		if c.HoverRow != newHover {
			c.HoverRow = newHover
			c.Update()
		}
		return true

	case uv.MouseClickEvent:
		if isOnItem {
			absItem := c.ScrollOffset + itemRow
			vis := col.Visible()
			if absItem < len(vis) {
				c.Browser.ActiveCol = c.ColIdx
				col.Index = absItem
				c.Browser.ClampScroll(col)
				c.Browser.ExpandSelection()
				c.Browser.Update()
			}
		}
		return true

	case uv.MouseWheelEvent:
		m := ev.Mouse()
		vis := col.Visible()
		if len(col.Items) > 0 {
			switch m.Button {
			case uv.MouseWheelUp:
				if col.Index > 0 {
					col.Index--
					c.Browser.ActiveCol = c.ColIdx
					c.Browser.ClampScroll(col)
					c.Browser.ExpandSelection()
					c.Browser.Update()
				}
			case uv.MouseWheelDown:
				if col.Index < len(vis)-1 {
					col.Index++
					c.Browser.ActiveCol = c.ColIdx
					c.Browser.ClampScroll(col)
					c.Browser.ExpandSelection()
					c.Browser.Update()
				}
			}
		} else {
			switch m.Button {
			case uv.MouseWheelUp:
				if col.DetailOffset > 0 {
					col.DetailOffset--
					c.Browser.Update()
				}
			case uv.MouseWheelDown:
				col.DetailOffset++
				c.Browser.Update()
			}
		}
		return true
	}

	return false
}

func (c *DocColumnComp) SetHovered(_ tuist.EventContext, hovered bool) {
	if c.Hovered != hovered {
		c.Hovered = hovered
		if !hovered {
			c.HoverRow = -1
		}
		c.Update()
	}
}

// ── doc browser overlay ─────────────────────────────────────────────────────

// DocBrowserOverlay is a Miller-column API documentation browser component.
type DocBrowserOverlay struct {
	tuist.Compo
	Columns    []DocColumn
	ActiveCol  int
	Filtering  bool
	OnExit     func()
	LastHeight int

	Crumbs   []*BreadcrumbCrumb
	ColComps []*DocColumnComp
}

// NewDocBrowserOverlay creates a new doc browser for the given type environment.
func NewDocBrowserOverlay(typeEnv dang.Env) *DocBrowserOverlay {
	root := BuildColumn("(root)", "Top-level scope", typeEnv)
	db := &DocBrowserOverlay{
		Columns: []DocColumn{root},
	}
	db.ExpandSelection()
	return db
}

func (d *DocBrowserOverlay) HandleKeyPress(_ tuist.EventContext, ev uv.KeyPressEvent) bool {
	defer d.Update()
	key := uv.Key(ev)
	if d.Filtering {
		d.handleFilterKey(key)
	} else {
		d.handleKey(key)
	}
	return true
}

func (d *DocBrowserOverlay) handleKey(key uv.Key) {
	switch {
	case key.Text == "q" || key.Code == uv.KeyEscape:
		if d.OnExit != nil {
			d.OnExit()
		}
	case key.Text == "/":
		col := &d.Columns[d.ActiveCol]
		if len(col.Items) > 0 {
			d.Filtering = true
		}
	case key.Code == uv.KeyLeft || key.Text == "h":
		if d.ActiveCol > 0 {
			d.Columns[d.ActiveCol].Filter = ""
			d.Columns[d.ActiveCol].ApplyFilter()
			d.Filtering = false
			d.ActiveCol--
			d.ExpandSelection()
		}
	case key.Code == uv.KeyRight || key.Text == "l" || key.Code == uv.KeyEnter:
		for i := d.ActiveCol + 1; i < len(d.Columns); i++ {
			if len(d.Columns[i].Items) > 0 {
				d.ActiveCol = i
				d.ExpandSelection()
				break
			}
		}
	case key.Code == uv.KeyUp || key.Text == "k":
		col := &d.Columns[d.ActiveCol]
		if col.Index > 0 {
			col.Index--
			d.ClampScroll(col)
			d.ExpandSelection()
		}
	case key.Code == uv.KeyDown || key.Text == "j":
		col := &d.Columns[d.ActiveCol]
		vis := col.Visible()
		if col.Index < len(vis)-1 {
			col.Index++
			d.ClampScroll(col)
			d.ExpandSelection()
		}
	case key.Code == uv.KeyTab:
		start := d.ActiveCol
		for {
			d.ActiveCol = (d.ActiveCol + 1) % len(d.Columns)
			if len(d.Columns[d.ActiveCol].Items) > 0 || d.ActiveCol == start {
				break
			}
		}
	}
}

func (d *DocBrowserOverlay) handleFilterKey(key uv.Key) {
	switch key.Code {
	case uv.KeyEscape:
		col := &d.Columns[d.ActiveCol]
		col.Filter = ""
		col.ApplyFilter()
		d.Filtering = false
		d.ExpandSelection()
	case uv.KeyEnter:
		d.Filtering = false
	case uv.KeyBackspace:
		col := &d.Columns[d.ActiveCol]
		if len(col.Filter) > 0 {
			col.Filter = col.Filter[:len(col.Filter)-1]
			col.ApplyFilter()
			d.ExpandSelection()
		} else {
			d.Filtering = false
		}
	case uv.KeyUp:
		col := &d.Columns[d.ActiveCol]
		if col.Index > 0 {
			col.Index--
			d.ClampScroll(col)
			d.ExpandSelection()
		}
	case uv.KeyDown:
		col := &d.Columns[d.ActiveCol]
		vis := col.Visible()
		if col.Index < len(vis)-1 {
			col.Index++
			d.ClampScroll(col)
			d.ExpandSelection()
		}
	default:
		if key.Text != "" {
			col := &d.Columns[d.ActiveCol]
			col.Filter += key.Text
			col.ApplyFilter()
			d.ExpandSelection()
		}
	}
}

func (d *DocBrowserOverlay) Render(ctx tuist.RenderContext) tuist.RenderResult {
	width := ctx.Width
	height := ctx.Height
	if height == 0 && ctx.ScreenHeight > 0 {
		height = ctx.ScreenHeight
	}
	if width < 20 {
		return tuist.RenderResult{Lines: []string{"(too narrow)"}}
	}

	if height > 0 {
		d.LastHeight = height
	}
	listH := max(height-4, 5)

	sep := DocSepStyle.Render(" │ ")

	visStart, visEnd := d.VisibleRange()
	numVis := max(visEnd-visStart, 1)
	sepW := 3 * (numVis - 1)
	colW := max((width-sepW)/numVis, 15)
	lastColW := max(width-sepW-colW*(numVis-1), colW)

	d.SyncColComps(visStart, visEnd)
	for ci, cc := range d.ColComps {
		cc.ColIdx = visStart + ci
		cc.IsActive = cc.ColIdx == d.ActiveCol
		cc.Update()
	}

	var colRendered [][]string
	for ci, cc := range d.ColComps {
		w := colW
		if ci == len(d.ColComps)-1 {
			w = lastColW
		}
		r := d.RenderChild(cc, tuist.RenderContext{Width: w, Height: listH + 2, ScreenHeight: height})
		colRendered = append(colRendered, r.Lines)
	}

	totalLines := listH + 2
	var rows []string
	for i := range totalLines {
		var parts []string
		for _, cl := range colRendered {
			parts = append(parts, GetLine(cl, i))
		}
		rows = append(rows, strings.Join(parts, sep))
	}

	d.SyncCrumbs()
	var crumbParts []string
	for i, c := range d.Crumbs {
		if i > 0 {
			crumbParts = append(crumbParts, DocDimStyle.Render(" › "))
		}
		c.Active = i == d.ActiveCol
		c.Label = d.Columns[i].Title
		c.Update()
		r := d.RenderChild(c, tuist.RenderContext{Width: width})
		text := ""
		if len(r.Lines) > 0 {
			text = r.Lines[0]
		}
		crumbParts = append(crumbParts, text)
	}
	breadcrumb := strings.Join(crumbParts, "")

	help := DocDimStyle.Render("Up/Down/hjkl navigate | Click/scroll | / filter | Tab cycle | q/Esc exit")

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

// SyncCrumbs ensures Crumbs has exactly ActiveCol+1 entries.
func (d *DocBrowserOverlay) SyncCrumbs() {
	need := d.ActiveCol + 1
	for len(d.Crumbs) > need {
		d.Crumbs = d.Crumbs[:len(d.Crumbs)-1]
	}
	for len(d.Crumbs) < need {
		idx := len(d.Crumbs)
		c := &BreadcrumbCrumb{}
		c.OnClick = func() {
			if idx < d.ActiveCol {
				d.ActiveCol = idx
				d.ExpandSelection()
				d.Update()
			}
		}
		d.Crumbs = append(d.Crumbs, c)
	}
}

// SyncColComps ensures ColComps has exactly visEnd-visStart entries.
func (d *DocBrowserOverlay) SyncColComps(visStart, visEnd int) {
	need := visEnd - visStart
	for len(d.ColComps) > need {
		d.ColComps = d.ColComps[:len(d.ColComps)-1]
	}
	for len(d.ColComps) < need {
		cc := &DocColumnComp{Browser: d, HoverRow: -1}
		d.ColComps = append(d.ColComps, cc)
	}
}

// ExpandSelection rebuilds columns after the active one based on the selection.
func (d *DocBrowserOverlay) ExpandSelection() {
	d.Columns = d.Columns[:d.ActiveCol+1]
	col := &d.Columns[d.ActiveCol]
	item, ok := col.SelectedItem()
	if !ok {
		return
	}

	detail := BuildDetailColumn(item)
	d.Columns = append(d.Columns, detail)

	if item.RetEnv != nil {
		members := BuildColumn(item.Name+" -> "+item.RetEnv.Name(), item.RetEnv.GetModuleDocString(), item.RetEnv)
		if len(members.Items) > 0 {
			d.Columns = append(d.Columns, members)
		}
	}
}

// ClampScroll ensures the selected item is visible.
func (d *DocBrowserOverlay) ClampScroll(col *DocColumn) {
	h := d.ListHeight()
	if col.Index < col.Offset {
		col.Offset = col.Index
	}
	if col.Index >= col.Offset+h {
		col.Offset = col.Index - h + 1
	}
}

// ListHeight returns the usable list height.
func (d *DocBrowserOverlay) ListHeight() int {
	return max(d.LastHeight-4, 5)
}

// VisibleRange returns the start/end indices of visible columns.
func (d *DocBrowserOverlay) VisibleRange() (int, int) {
	maxCols := 3
	total := len(d.Columns)
	if total <= maxCols {
		return 0, total
	}
	start := max(d.ActiveCol-1, 0)
	end := start + maxCols
	if end > total {
		end = total
		start = max(end-maxCols, 0)
	}
	return start, end
}
