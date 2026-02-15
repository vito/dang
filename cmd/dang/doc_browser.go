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
	title   string
	doc     string     // doc string for the column header (type/module doc)
	items   []docItem  // selectable items
	index   int        // selected index
	offset  int        // scroll offset
	typeEnv dang.Env   // the env this column lists members of (nil for detail)
}

// docItem is a single entry in a column.
type docItem struct {
	name    string
	typeStr string
	doc     string
	args    []docArg
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

	// Detail pane scroll offset (scrolls independently of column selection)
	detailOffset int
	detailLines  int // total rendered detail lines (set during View)

	width  int
	height int
}

func newDocBrowser(typeEnv dang.Env) docBrowserModel {
	root := buildColumn("(root)", "Top-level scope", typeEnv)

	m := docBrowserModel{
		columns: []docColumn{root},
	}
	// Pre-expand the first selection
	m.expandSelection()
	return m
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
			item.args = extractArgs(fn)
			item.typeStr = formatReturnType(fn)

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
			}
		}

		col.items = append(col.items, item)
	}

	sort.Slice(col.items, func(i, j int) bool {
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
	// Reset detail scroll when selection changes
	m.detailOffset = 0

	// Trim columns to the right of the active one
	m.columns = m.columns[:m.activeCol+1]

	col := &m.columns[m.activeCol]
	if col.index >= len(col.items) {
		m.detailLines = 0
		return
	}
	item := col.items[col.index]

	// Always add a detail column for the selected item
	detail := buildDetailColumn(item)
	m.columns = append(m.columns, detail)

	// Pre-compute detail line count for scroll clamping.
	// Use a generous width estimate; the exact width depends on layout
	// but this is close enough for scroll bounds.
	noStyle := lipgloss.NewStyle()
	m.detailLines = len(m.renderDetail(item, 80, noStyle, noStyle, noStyle, noStyle))

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
		switch msg.String() {
		case "q", "escape":
			return m, func() tea.Msg { return docBrowserExitMsg{} }

		case "left", "h":
			if m.activeCol > 0 {
				m.activeCol--
				// Trim everything beyond active+1 and re-expand
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
			if col.index < len(col.items)-1 {
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

	case tea.MouseWheelMsg:
		// Mouse wheel always scrolls the detail pane
		switch msg.Button {
		case tea.MouseWheelUp:
			if m.detailOffset > 0 {
				m.detailOffset -= 3
				if m.detailOffset < 0 {
					m.detailOffset = 0
				}
			}
			return m, nil
		case tea.MouseWheelDown:
			maxOffset := m.detailLines - m.listHeight()
			if maxOffset < 0 {
				maxOffset = 0
			}
			m.detailOffset += 3
			if m.detailOffset > maxOffset {
				m.detailOffset = maxOffset
			}
			return m, nil
		}
	}
	return m, nil
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
		return v
	}

	listH := m.listHeight()

	// Styles
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	activeTitle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	normalStyle := lipgloss.NewStyle()
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

		// Title
		t := col.title
		if isActive {
			lines = append(lines, activeTitle.Render(truncate(t, w)))
		} else {
			lines = append(lines, titleStyle.Render(truncate(t, w)))
		}
		lines = append(lines, sepStyle.Render(strings.Repeat("─", w)))

		if len(col.items) > 0 {
			// Navigable column: show item list
			end := col.offset + listH
			if end > len(col.items) {
				end = len(col.items)
			}
			for i := col.offset; i < end; i++ {
				item := col.items[i]
				label := item.name
				if len(item.args) > 0 {
					label += "(…)"
				}
				label = truncate(label, w-2)
				if i == col.index {
					if isActive {
						lines = append(lines, selectedStyle.Render("▸ "+label))
					} else {
						lines = append(lines, normalStyle.Render("▸ "+label))
					}
				} else {
					lines = append(lines, normalStyle.Render("  "+label))
				}
			}
		} else {
			// Detail column: show item info with scroll support
			if ci > 0 {
				prevCol := m.columns[ci-1]
				if prevCol.index < len(prevCol.items) {
					item := prevCol.items[prevCol.index]
					detailContent := m.renderDetail(item, w, docTextStyle, argNameStyle, argTypeStyle, dimStyle)

					// Apply scroll offset
					contentH := listH // available height after header
					start := m.detailOffset
					if start > len(detailContent) {
						start = len(detailContent)
					}
					end := start + contentH
					if end > len(detailContent) {
						end = len(detailContent)
					}
					visible := detailContent[start:end]
					lines = append(lines, visible...)

					// Show scroll indicator
					if m.detailLines > contentH {
						scrollPct := ""
						if m.detailOffset == 0 {
							scrollPct = "top"
						} else if m.detailOffset >= m.detailLines-contentH {
							scrollPct = "end"
						} else {
							pct := m.detailOffset * 100 / (m.detailLines - contentH)
							scrollPct = fmt.Sprintf("%d%%", pct)
						}
						// Replace last line with scroll indicator
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
	help := dimStyle.Render("↑/↓ navigate • ←/→ drill in/out • scroll detail • Tab cycle • q/Esc exit")

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

	if len(lines) == 1 && item.doc == "" && len(item.args) == 0 {
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
