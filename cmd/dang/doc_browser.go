package main

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

// docColumn represents one column in the Miller-column browser.
type docColumn struct {
	title        string
	doc          string    // doc string for the column header (type/module doc)
	items        []docItem // all items (unfiltered)
	filtered     []int     // indices into items matching filter (nil = show all)
	filter       string    // current filter text
	index        int       // selected index within visible() items
	offset       int       // scroll offset (for item lists)
	detailOffset int       // scroll offset (for detail panes)
	detailLines  int       // total rendered detail lines
	typeEnv      dang.Env  // the env this column lists members of (nil for detail)
}

// visible returns the items to display, respecting the filter.
func (c *docColumn) visible() []docItem {
	if c.filtered == nil {
		return c.items
	}
	vis := make([]docItem, len(c.filtered))
	for i, idx := range c.filtered {
		vis[i] = c.items[idx]
	}
	return vis
}

// selectedItem returns the currently selected item, if any.
func (c *docColumn) selectedItem() (docItem, bool) {
	vis := c.visible()
	if c.index >= 0 && c.index < len(vis) {
		return vis[c.index], true
	}
	return docItem{}, false
}

// applyFilter updates the filtered indices based on the current filter string.
func (c *docColumn) applyFilter() {
	if c.filter == "" {
		c.filtered = nil
		return
	}
	lower := strings.ToLower(c.filter)
	c.filtered = nil
	for i, item := range c.items {
		if strings.Contains(strings.ToLower(item.name), lower) {
			c.filtered = append(c.filtered, i)
		}
	}
	// Clamp selection
	if len(c.filtered) == 0 {
		c.index = 0
	} else if c.index >= len(c.filtered) {
		c.index = len(c.filtered) - 1
	}
	c.offset = 0
}

// itemKind classifies a doc browser entry.
type itemKind int

const (
	kindField     itemKind = iota // functions, methods, fields
	kindType                      // object types (Container, File, ...)
	kindInterface                 // interface types
	kindEnum                      // enum types
	kindScalar                    // scalar types (String, Int, ...)
	kindUnion                     // union types
	kindInput                     // input object types
)

var kindOrder = [...]string{
	kindField:     "field",
	kindType:      "type",
	kindInterface: "interface",
	kindEnum:      "enum",
	kindScalar:    "scalar",
	kindUnion:     "union",
	kindInput:     "input",
}

var kindColors = [...]string{
	kindField:     "117", // blue
	kindType:      "213", // pink
	kindInterface: "141", // purple
	kindEnum:      "221", // yellow
	kindScalar:    "114", // green
	kindUnion:     "209", // orange
	kindInput:     "183", // lavender
}

func (k itemKind) label() string {
	if int(k) < len(kindOrder) {
		return kindOrder[k]
	}
	return "?"
}

func (k itemKind) color() string {
	if int(k) < len(kindColors) {
		return kindColors[k]
	}
	return "247"
}

// docItem is a single entry in a column.
type docItem struct {
	name      string
	kind      itemKind
	typeStr   string
	doc       string
	args      []docArg
	blockArgs []docArg // block/callback parameters
	blockRet  string   // block return type
	// If selecting this item can produce a deeper column, retEnv is set.
	retEnv dang.Env
}

// docArg represents an argument to a function.
type docArg struct {
	name    string
	typeStr string
	doc     string
}

// docBrowserModel is the Miller-column API browser.
type docBrowserModel struct {
	columns   []docColumn // stack of columns
	activeCol int         // which column is focused
	filtering bool        // true when filter input is active on the focused column

	width  int
	height int
}

func newDocBrowser(typeEnv dang.Env, width, height int) docBrowserModel {
	root := buildColumn("(root)", "Top-level scope", typeEnv)

	m := docBrowserModel{
		columns: []docColumn{root},
		width:   width,
		height:  height,
	}
	// Pre-expand the first selection
	m.expandSelection()
	return m
}

// classifyEnv determines the itemKind for a module/env based on its ModuleKind.
func classifyEnv(env dang.Env) itemKind {
	if mod, ok := env.(*dang.Module); ok {
		switch mod.Kind {
		case dang.EnumKind:
			return kindEnum
		case dang.ScalarKind:
			return kindScalar
		case dang.InterfaceKind:
			return kindInterface
		case dang.UnionKind:
			return kindUnion
		case dang.InputKind:
			return kindInput
		}
	}
	return kindType
}

// buildColumn creates a column listing public members of an Env.
func buildColumn(title, doc string, env dang.Env) docColumn {
	col := docColumn{
		title:   title,
		doc:     doc,
		typeEnv: env,
	}

	if env == nil {
		return col
	}

	for name, scheme := range env.Bindings(dang.PublicVisibility) {
		t, _ := scheme.Type()
		if t == nil {
			continue
		}

		item := docItem{
			name:    name,
			typeStr: t.String(),
		}

		if d, found := env.GetDocString(name); found {
			item.doc = d
		}

		// Extract function args and return type
		if fn, ok := t.(*hm.FunctionType); ok {
			item.kind = kindField
			item.args = extractArgs(fn)
			item.typeStr = formatReturnType(fn)
			extractBlockInfo(fn, &item)

			// The return type env lets us drill deeper
			ret := unwrapType(fn.Ret(true))
			if mod, ok := ret.(dang.Env); ok {
				item.retEnv = mod
			}
		} else {
			// Non-function: check if the type itself is an env
			inner := unwrapType(t)
			if mod, ok := inner.(dang.Env); ok {
				item.retEnv = mod
				item.kind = classifyEnv(mod)
			} else {
				item.kind = kindField
			}
		}

		col.items = append(col.items, item)
	}

	// Include built-in methods registered via the global registry
	// (e.g. String.padLeft, List.map, etc.)
	seen := make(map[string]bool, len(col.items))
	for _, item := range col.items {
		seen[item.name] = true
	}
	if mod, ok := env.(*dang.Module); ok {
		dang.ForEachMethod(mod, func(def dang.BuiltinDef) {
			if seen[def.Name] {
				return
			}
			seen[def.Name] = true

			item := docItem{
				name: def.Name,
				kind: kindField,
				doc:  def.Doc,
			}

			// Build args
			for _, p := range def.ParamTypes {
				item.args = append(item.args, docArg{
					name:    p.Name,
					typeStr: formatType(p.Type),
				})
			}

			// Format return type
			if def.ReturnType != nil {
				item.typeStr = "→ " + formatType(def.ReturnType)
			}

			// Block args
			if def.BlockType != nil {
				item.blockArgs = extractArgs(def.BlockType)
				item.blockRet = formatType(def.BlockType.Ret(true))
			}

			// Check if return type is drillable
			if def.ReturnType != nil {
				ret := unwrapType(def.ReturnType)
				if retEnv, ok := ret.(dang.Env); ok {
					item.retEnv = retEnv
				}
			}

			col.items = append(col.items, item)
		})
	}

	// Also include named types (classes) that aren't already in bindings.
	// These are built-in types like String, Boolean, List, etc.
	for name, namedEnv := range env.NamedTypes() {
		if seen[name] {
			continue
		}

		item := docItem{
			name:    name,
			typeStr: namedEnv.Name(),
			retEnv:  namedEnv,
			kind:    classifyEnv(namedEnv),
		}
		if d := namedEnv.GetModuleDocString(); d != "" {
			item.doc = d
		}
		col.items = append(col.items, item)
	}

	// Sort by kind order, then alphabetically within each kind
	sort.Slice(col.items, func(i, j int) bool {
		if col.items[i].kind != col.items[j].kind {
			return col.items[i].kind < col.items[j].kind
		}
		return strings.ToLower(col.items[i].name) < strings.ToLower(col.items[j].name)
	})

	return col
}

// extractArgs pulls argument info from a function type.
func extractArgs(fn *hm.FunctionType) []docArg {
	arg := fn.Arg()
	rec, ok := arg.(*dang.RecordType)
	if !ok {
		return nil
	}

	var args []docArg
	for _, field := range rec.Fields {
		t, _ := field.Value.Type()
		a := docArg{
			name:    field.Key,
			typeStr: formatType(t),
		}
		if rec.DocStrings != nil {
			if doc, found := rec.DocStrings[field.Key]; found {
				a.doc = doc
			}
		}
		args = append(args, a)
	}
	return args
}

// extractBlockInfo populates blockArgs/blockRet on a docItem from a FunctionType's block.
func extractBlockInfo(fn *hm.FunctionType, item *docItem) {
	block := fn.Block()
	if block == nil {
		return
	}
	item.blockArgs = extractArgs(block)
	item.blockRet = formatType(block.Ret(true))
}

// formatReturnType shows "→ RetType" for a function.
func formatReturnType(fn *hm.FunctionType) string {
	ret := fn.Ret(true)
	return "→ " + formatType(ret)
}

func formatType(t hm.Type) string {
	if t == nil {
		return "?"
	}
	return t.String()
}

// unwrapType removes NonNull and follows function return types.
func unwrapType(t hm.Type) hm.Type {
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}
	return t
}

// expandSelection appends (or replaces) a column for the currently selected
// item, and also a detail column if the item has docs/args.
func (m *docBrowserModel) expandSelection() {
	// Trim columns to the right of the active one
	m.columns = m.columns[:m.activeCol+1]

	col := &m.columns[m.activeCol]
	item, ok := col.selectedItem()
	if !ok {
		return
	}

	// Always add a detail column for the selected item
	detail := buildDetailColumn(item)
	m.columns = append(m.columns, detail)

	// Precompute detail line count for the new column
	m.recomputeDetailLines(&m.columns[len(m.columns)-1], item)

	// If the item has a return env, add a members column too
	if item.retEnv != nil {
		members := buildColumn(item.name+" → "+item.retEnv.Name(), item.retEnv.GetModuleDocString(), item.retEnv)
		if len(members.items) > 0 {
			m.columns = append(m.columns, members)
		}
	}
}

// buildDetailColumn creates a non-interactive detail pane for an item.
func buildDetailColumn(item docItem) docColumn {
	return docColumn{
		title: item.name,
		doc:   item.typeStr,
		items: nil, // no selectable items — this is a detail pane
	}
}

func (m docBrowserModel) Init() tea.Cmd {
	return nil
}

func (m docBrowserModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyPressMsg:
		// Filter input mode: typing goes to filter
		if m.filtering {
			switch msg.String() {
			case "escape":
				// Clear filter and exit filter mode
				col := &m.columns[m.activeCol]
				col.filter = ""
				col.applyFilter()
				m.filtering = false
				m.expandSelection()
				return m, nil

			case "enter":
				// Confirm filter, return to navigation
				m.filtering = false
				return m, nil

			case "backspace":
				col := &m.columns[m.activeCol]
				if len(col.filter) > 0 {
					col.filter = col.filter[:len(col.filter)-1]
					col.applyFilter()
					m.expandSelection()
				} else {
					// Empty filter + backspace exits filter mode
					m.filtering = false
				}
				return m, nil

			case "up":
				col := &m.columns[m.activeCol]
				if col.index > 0 {
					col.index--
					m.clampScroll(col)
					m.expandSelection()
				}
				return m, nil

			case "down":
				col := &m.columns[m.activeCol]
				vis := col.visible()
				if col.index < len(vis)-1 {
					col.index++
					m.clampScroll(col)
					m.expandSelection()
				}
				return m, nil

			default:
				r := msg.String()
				if len(r) == 1 && r[0] >= 32 && r[0] < 127 {
					col := &m.columns[m.activeCol]
					col.filter += r
					col.applyFilter()
					m.expandSelection()
					return m, nil
				}
			}
			return m, nil
		}

		// Normal navigation mode
		switch msg.String() {
		case "q", "escape":
			return m, func() tea.Msg { return docBrowserExitMsg{} }

		case "/":
			// Enter filter mode on the active column
			col := &m.columns[m.activeCol]
			if len(col.items) > 0 {
				m.filtering = true
			}
			return m, nil

		case "left", "h":
			if m.activeCol > 0 {
				// Clear filter on current column when leaving
				m.columns[m.activeCol].filter = ""
				m.columns[m.activeCol].applyFilter()
				m.filtering = false
				m.activeCol--
				m.expandSelection()
			}
			return m, nil

		case "right", "l", "enter":
			// Move into the next navigable column (skip detail columns)
			for i := m.activeCol + 1; i < len(m.columns); i++ {
				if len(m.columns[i].items) > 0 {
					m.activeCol = i
					m.expandSelection()
					break
				}
			}
			return m, nil

		case "up", "k":
			col := &m.columns[m.activeCol]
			if col.index > 0 {
				col.index--
				m.clampScroll(col)
				m.expandSelection()
			}
			return m, nil

		case "down", "j":
			col := &m.columns[m.activeCol]
			vis := col.visible()
			if col.index < len(vis)-1 {
				col.index++
				m.clampScroll(col)
				m.expandSelection()
			}
			return m, nil

		case "tab":
			// Cycle through navigable columns
			start := m.activeCol
			for {
				m.activeCol = (m.activeCol + 1) % len(m.columns)
				if len(m.columns[m.activeCol].items) > 0 || m.activeCol == start {
					break
				}
			}
			return m, nil
		}

	case tea.MouseMsg:
		mouse := msg.Mouse()
		if mouse.Button != tea.MouseWheelUp && mouse.Button != tea.MouseWheelDown {
			break
		}

		delta := 3
		if mouse.Button == tea.MouseWheelUp {
			delta = -3
		}

		// Determine which column the mouse is over
		colIdx := m.columnAtX(mouse.X)
		if colIdx < 0 {
			break
		}

		col := &m.columns[colIdx]
		if len(col.items) > 0 {
			// Navigable column: scroll the item list
			col.offset += delta
			vis := col.visible()
			maxOffset := len(vis) - m.listHeight()
			if maxOffset < 0 {
				maxOffset = 0
			}
			if col.offset < 0 {
				col.offset = 0
			}
			if col.offset > maxOffset {
				col.offset = maxOffset
			}
		} else {
			// Detail column: scroll this specific detail pane
			if item, ok := m.detailItemForColumn(colIdx); ok {
				m.recomputeDetailLines(col, item)
			}
			col.detailOffset += delta
			maxOffset := col.detailLines - m.listHeight()
			if maxOffset < 0 {
				maxOffset = 0
			}
			if col.detailOffset < 0 {
				col.detailOffset = 0
			}
			if col.detailOffset > maxOffset {
				col.detailOffset = maxOffset
			}
		}
		return m, nil
	}
	return m, nil
}

// columnAtX returns the absolute column index under screen X coordinate,
// or -1 if X is outside visible columns (e.g. on a separator).
func (m docBrowserModel) columnAtX(x int) int {
	visStart, visEnd := m.visibleRange()
	numVis := visEnd - visStart
	if numVis < 1 {
		return -1
	}

	sepW := 3 * (numVis - 1)
	colW := (m.width - sepW) / numVis
	if colW < 15 {
		colW = 15
	}
	lastColW := m.width - sepW - colW*(numVis-1)
	if lastColW < colW {
		lastColW = colW
	}

	// Account for breadcrumb line: mouse Y=0 is breadcrumb, columns start at Y=1.
	// But X mapping is the same regardless of Y.
	pos := 0
	for i := 0; i < numVis; i++ {
		w := colW
		if i == numVis-1 {
			w = lastColW
		}
		if x >= pos && x < pos+w {
			return visStart + i
		}
		pos += w + 3 // +3 for separator " │ "
	}
	return -1
}

// detailColWidth returns the width of the detail column based on current layout.
func (m docBrowserModel) detailColWidth() int {
	visStart, visEnd := m.visibleRange()
	numVis := visEnd - visStart
	if numVis < 1 {
		numVis = 1
	}
	sepW := 3 * (numVis - 1)
	colW := (m.width - sepW) / numVis
	if colW < 15 {
		colW = 15
	}
	lastColW := m.width - sepW - colW*(numVis-1)
	if lastColW < colW {
		lastColW = colW
	}
	return lastColW
}

// recomputeDetailLines recalculates detailLines on a column using the actual column width.
func (m *docBrowserModel) recomputeDetailLines(col *docColumn, item docItem) {
	w := m.detailColWidth()
	if w <= 0 {
		w = 80
	}
	noStyle := lipgloss.NewStyle()
	col.detailLines = len(m.renderDetail(item, w, noStyle, noStyle, noStyle, noStyle))
}

// detailItemForColumn returns the item whose detail is shown in column colIdx.
// Detail columns are placed right after the navigable column whose selection they describe.
func (m docBrowserModel) detailItemForColumn(colIdx int) (docItem, bool) {
	if colIdx > 0 {
		prev := &m.columns[colIdx-1]
		return prev.selectedItem()
	}
	return docItem{}, false
}

func (m *docBrowserModel) clampScroll(col *docColumn) {
	h := m.listHeight()
	if col.index < col.offset {
		col.offset = col.index
	}
	if col.index >= col.offset+h {
		col.offset = col.index - h + 1
	}
}

func (m docBrowserModel) listHeight() int {
	h := m.height - 4
	if h < 5 {
		h = 5
	}
	return h
}

func (m docBrowserModel) View() tea.View {
	if m.width == 0 || m.height == 0 {
		v := tea.NewView("Loading...")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	listH := m.listHeight()

	// Styles
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	activeTitle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	docTextStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("249"))
	argNameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	argTypeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	sep := sepStyle.Render(" │ ")

	// Determine visible columns: show activeCol and neighbors
	// Try to show 3 columns, biased to show the detail/child
	visStart, visEnd := m.visibleRange()

	// Divide width among visible columns
	numVis := visEnd - visStart
	if numVis < 1 {
		numVis = 1
	}
	sepW := 3 * (numVis - 1) // separator widths
	colW := (m.width - sepW) / numVis
	if colW < 15 {
		colW = 15
	}

	// Last column gets remaining space
	lastColW := m.width - sepW - colW*(numVis-1)
	if lastColW < colW {
		lastColW = colW
	}

	// Render each visible column
	var colRendered [][]string // one []string per column
	for ci := visStart; ci < visEnd; ci++ {
		col := m.columns[ci]
		w := colW
		if ci == visEnd-1 {
			w = lastColW
		}
		isActive := ci == m.activeCol

		var lines []string
		isFiltering := m.filtering && isActive

		// Title
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
			// Show filter input line, reducing available list height
			filterLineH = 1
			filterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
			filterText := "/" + col.filter
			if isFiltering {
				filterText += "█" // cursor
			}
			countText := dimStyle.Render(fmt.Sprintf(" %d/%d", len(vis), len(col.items)))
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
			// Navigable column: show item list
			end := col.offset + itemListH
			if end > len(vis) {
				end = len(vis)
			}
			for i := col.offset; i < end; i++ {
				item := vis[i]
				label := item.name
				if len(item.args) > 0 {
					label += "(…)"
				}

				// Build kind tag
				tag := item.kind.label()
				tagStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(item.kind.color())).Render(tag)
				tagW := lipgloss.Width(tagStyled)

				// Truncate label to fit: "▸ " (2) + label + " " (1) + tag
				maxLabel := w - 3 - tagW
				if maxLabel < 4 {
					maxLabel = 4
				}
				label = truncate(label, maxLabel)

				// Compose line with right-aligned tag
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
				raw := leftPart + strings.Repeat(" ", gap) + tagStyled

				if i == col.index {
					if isActive {
						// Re-render left part in selected style, keep tag colored
						leftStyled := selectedStyle.Render(prefix+label) + strings.Repeat(" ", gap) + tagStyled
						lines = append(lines, leftStyled)
					} else {
						lines = append(lines, raw)
					}
				} else {
					lines = append(lines, raw)
				}
			}
		} else {
			// Detail column: show item info with scroll support
			if ci > 0 {
				prevCol := &m.columns[ci-1]
				if item, ok := prevCol.selectedItem(); ok {
					detailContent := m.renderDetail(item, w, docTextStyle, argNameStyle, argTypeStyle, dimStyle)

					// Apply per-column scroll offset
					contentH := listH
					dOffset := col.detailOffset
					if dOffset > len(detailContent) {
						dOffset = len(detailContent)
					}
					end := dOffset + contentH
					if end > len(detailContent) {
						end = len(detailContent)
					}
					visible := detailContent[dOffset:end]
					lines = append(lines, visible...)

					// Show scroll indicator
					if len(detailContent) > contentH {
						scrollPct := ""
						if dOffset == 0 {
							scrollPct = "top"
						} else if dOffset >= len(detailContent)-contentH {
							scrollPct = "end"
						} else {
							pct := dOffset * 100 / (len(detailContent) - contentH)
							scrollPct = fmt.Sprintf("%d%%", pct)
						}
						if len(lines) >= listH+2 {
							lines[listH+1] = dimStyle.Render(fmt.Sprintf("── scroll: %s ──", scrollPct))
						}
					}
				}
			}
		}

		// Pad to height
		for len(lines) < listH+2 {
			lines = append(lines, "")
		}

		colRendered = append(colRendered, lines)
	}

	// Compose rows
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

	// Breadcrumb
	var crumbs []string
	for i := 0; i <= m.activeCol; i++ {
		crumbs = append(crumbs, m.columns[i].title)
	}
	breadcrumb := dimStyle.Render(strings.Join(crumbs, " → "))

	// Help
	help := dimStyle.Render("↑/↓/hjkl navigate • / filter • Tab cycle • q/Esc exit")

	content := breadcrumb + "\n" + strings.Join(rows, "\n") + "\n" + help

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// visibleRange returns the start (inclusive) and end (exclusive) indices of
// columns to display, keeping the active column visible.
func (m docBrowserModel) visibleRange() (int, int) {
	maxCols := 3
	total := len(m.columns)

	if total <= maxCols {
		return 0, total
	}

	// Center on active column, biased to show children
	start := m.activeCol - 1
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

func (m docBrowserModel) renderDetail(item docItem, w int, docStyle, argNameStyle, argTypeStyle, dimStyle lipgloss.Style) []string {
	var lines []string

	// Type
	lines = append(lines, argTypeStyle.Render(truncate(item.typeStr, w)))
	lines = append(lines, "")

	// Doc string
	if item.doc != "" {
		wrapped := wordWrap(item.doc, w)
		for _, line := range strings.Split(wrapped, "\n") {
			lines = append(lines, docStyle.Render(line))
		}
		lines = append(lines, "")
	}

	// Arguments
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
					lines = append(lines, "    "+dimStyle.Render(line))
				}
			}
		}
	}

	// Block args section
	if len(item.blockArgs) > 0 {
		lines = append(lines, "")
		blockHeader := "Block:"
		if item.blockRet != "" {
			blockHeader = fmt.Sprintf("Block → %s:", argTypeStyle.Render(item.blockRet))
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
					lines = append(lines, "    "+dimStyle.Render(line))
				}
			}
		}
	}

	if len(lines) == 1 && item.doc == "" && len(item.args) == 0 && len(item.blockArgs) == 0 {
		lines = append(lines, dimStyle.Render("(no documentation)"))
	}

	return lines
}

// docBrowserExitMsg signals the REPL to close the doc browser.
type docBrowserExitMsg struct{}

// Helper functions

func truncate(s string, maxW int) string {
	if len(s) <= maxW {
		return s
	}
	if maxW <= 3 {
		return s[:maxW]
	}
	return s[:maxW-1] + "…"
}

func padRight(s string, w int) string {
	visible := lipgloss.Width(s)
	if visible >= w {
		return s
	}
	return s + strings.Repeat(" ", w-visible)
}

func getLine(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

func wordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}

	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
		} else {
			line += " " + w
		}
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}
