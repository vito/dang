// Command grid-demo renders an interactive grid of colored rectangles
// that respond to mouse hover and click-to-focus, demonstrating pitui's
// marker-based zone system with side-by-side layout.
//
// Usage:
//
//	go run ./cmd/grid-demo
package main

import (
	"fmt"
	"os"
	"image/color"
	"os/signal"
	"strings"
	"syscall"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/dang/pkg/pitui"
)

const maxCells = 400

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	term := pitui.NewProcessTerminal()
	tui := pitui.New(term)

	g := newGrid()
	tui.Dispatch(func() {
		tui.AddChild(g)
		tui.SetFocus(g)
	})

	if err := tui.Start(); err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-g.quit:
	case <-sigCh:
	}
	signal.Stop(sigCh)
	tui.Stop()
	return nil
}

// ── Colors & styles ────────────────────────────────────────────────────────

// Cell palette — two alternating base shades for a checkerboard effect.
var (
	baseBg1 = lipgloss.Color("235")
	baseBg2 = lipgloss.Color("236")
	baseFg  = lipgloss.Color("243")

	hoverBg = lipgloss.Color("238")
	hoverFg = lipgloss.Color("51")

	focusBg      = lipgloss.Color("54")
	focusFg      = lipgloss.Color("255")
	focusHoverBg = lipgloss.Color("57")

	statusBg    = lipgloss.Color("234")
	statusFg    = lipgloss.Color("245")
	statusKeyFg = lipgloss.Color("81")
)

// ── Grid ───────────────────────────────────────────────────────────────────

type grid struct {
	pitui.Compo
	cells    []*cell
	cols     int // current dimensions, set during Render
	rows     int
	selected int // focused cell index, -1 = none
	quit     chan struct{}
}

func newGrid() *grid {
	g := &grid{
		selected: -1,
		quit:     make(chan struct{}),
	}
	for i := range maxCells {
		g.cells = append(g.cells, &cell{grid: g, index: i})
	}
	return g
}

func (g *grid) OnMount(ctx pitui.EventContext) {
	for _, c := range g.cells {
		ctx.Attach(c)
	}
}

// HandleKeyPress handles global keys and arrow navigation. Cell key
// events bubble here because cells are Attached with the grid as parent.
func (g *grid) HandleKeyPress(ctx pitui.EventContext, ev uv.KeyPressEvent) bool {
	key := uv.Key(ev)
	switch {
	case key.Text == "q" || (key.Code == 'c' && key.Mod == uv.ModCtrl):
		select {
		case <-g.quit:
		default:
			close(g.quit)
		}
		return true
	case key.Code == uv.KeyEscape:
		if g.selected >= 0 {
			g.selected = -1
			ctx.SetFocus(g)
			g.Update()
			return true
		}
	case key.Code == uv.KeyUp, key.Code == uv.KeyDown,
		key.Code == uv.KeyLeft, key.Code == uv.KeyRight:
		return g.navigate(ctx, key.Code)
	case key.Code == uv.KeyEnter || key.Text == " ":
		if g.selected < 0 && g.cols > 0 {
			g.selected = 0
			ctx.SetFocus(g.cells[0])
			g.Update()
			return true
		}
	}
	return false
}

func (g *grid) navigate(ctx pitui.EventContext, code rune) bool {
	if g.cols == 0 || g.rows == 0 {
		return false
	}
	total := min(g.cols*g.rows, maxCells)

	sel := g.selected
	if sel < 0 {
		sel = 0
	}
	row := sel / g.cols
	col := sel % g.cols

	switch code {
	case uv.KeyUp:
		if row > 0 {
			row--
		}
	case uv.KeyDown:
		if row < g.rows-1 {
			row++
		}
	case uv.KeyLeft:
		if col > 0 {
			col--
		}
	case uv.KeyRight:
		if col < g.cols-1 {
			col++
		}
	}

	newSel := row*g.cols + col
	if newSel >= total {
		return true
	}
	if newSel != g.selected {
		g.selected = newSel
		ctx.SetFocus(g.cells[newSel])
		g.Update()
	}
	return true
}

func (g *grid) Render(ctx pitui.RenderContext) pitui.RenderResult {
	w := ctx.Width
	h := ctx.ScreenHeight - 1 // reserve 1 line for status bar

	cellW := max(w/10, 6)
	cellH := max(h/10, 3)
	g.cols = max(w/cellW, 1)
	g.rows = max(h/cellH, 1)
	total := min(g.cols*g.rows, maxCells)

	var allLines []string
	for r := range g.rows {
		var rowCells []string
		for c := range g.cols {
			idx := r*g.cols + c
			if idx >= total {
				break
			}
			cell := g.cells[idx]
			rendered := pitui.Mark(cell, cell.renderBox(cellW, cellH, r, c))
			rowCells = append(rowCells, rendered)
		}
		if len(rowCells) == 0 {
			continue
		}
		joined := lipgloss.JoinHorizontal(lipgloss.Top, rowCells...)
		allLines = append(allLines, strings.Split(joined, "\n")...)
	}

	// Pad to fill screen height.
	for len(allLines) < h {
		allLines = append(allLines, "")
	}

	// Status bar.
	allLines = append(allLines, g.renderStatus(w))

	return pitui.RenderResult{Lines: allLines}
}

func (g *grid) renderStatus(w int) string {
	sty := lipgloss.NewStyle().Background(statusBg).Foreground(statusFg)
	key := lipgloss.NewStyle().Background(statusBg).Foreground(statusKeyFg).Bold(true)
	sep := sty.Render(" │ ")

	var parts []string

	// Show hovered cell.
	for i := range min(g.cols*g.rows, maxCells) {
		c := g.cells[i]
		if c.hovered {
			parts = append(parts, sty.Render("hover ")+key.Render(fmt.Sprintf("%d,%d", i/g.cols, i%g.cols)))
			break
		}
	}

	// Show focused cell.
	if g.selected >= 0 {
		parts = append(parts, sty.Render("focus ")+key.Render(fmt.Sprintf("%d,%d", g.selected/g.cols, g.selected%g.cols)))
	}

	// Key bindings.
	parts = append(parts,
		key.Render("↑↓←→")+sty.Render(" navigate"),
		key.Render("click")+sty.Render(" select"),
		key.Render("esc")+sty.Render(" deselect"),
		key.Render("q")+sty.Render(" quit"),
	)

	content := sty.Render(" ") + strings.Join(parts, sep) + sty.Render(" ")
	pad := max(w-lipgloss.Width(content), 0)
	return content + sty.Render(strings.Repeat(" ", pad))
}

// ── Cell ───────────────────────────────────────────────────────────────────

type cell struct {
	pitui.Compo
	grid    *grid
	index   int
	hovered bool
	focused bool
}

// Render returns empty — cells render inline via renderBox + Mark.
func (c *cell) Render(_ pitui.RenderContext) pitui.RenderResult {
	return pitui.RenderResult{}
}

func (c *cell) renderBox(w, h, row, col int) string {
	bg, fg := c.colors(row, col)
	label := fmt.Sprintf("%d,%d", row, col)
	styledLabel := lipgloss.NewStyle().Foreground(fg).Bold(c.focused).Render(label)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, styledLabel,
		lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Background(bg)),
	)
}

func (c *cell) colors(row, col int) (color.Color, color.Color) {
	switch {
	case c.focused && c.hovered:
		return focusHoverBg, focusFg
	case c.focused:
		return focusBg, focusFg
	case c.hovered:
		return hoverBg, hoverFg
	default:
		if (row+col)%2 == 0 {
			return baseBg1, baseFg
		}
		return baseBg2, baseFg
	}
}

// HandleMouse implements pitui.MouseEnabled — click to focus.
func (c *cell) HandleMouse(ctx pitui.EventContext, ev pitui.MouseEvent) bool {
	if _, ok := ev.MouseEvent.(uv.MouseClickEvent); ok {
		if ev.Mouse().Button == uv.MouseLeft {
			c.grid.selected = c.index
			ctx.SetFocus(c)
			c.grid.Update()
			return true
		}
	}
	return false
}

// SetHovered implements pitui.Hoverable.
func (c *cell) SetHovered(_ pitui.EventContext, hovered bool) {
	if hovered != c.hovered {
		c.hovered = hovered
		c.grid.Update()
	}
}

// SetFocused implements pitui.Focusable.
func (c *cell) SetFocused(_ pitui.EventContext, focused bool) {
	if focused != c.focused {
		c.focused = focused
		c.grid.Update()
	}
}

// HandleKeyPress — cells don't consume keys; they bubble to the grid.
func (c *cell) HandleKeyPress(_ pitui.EventContext, _ uv.KeyPressEvent) bool {
	return false
}
