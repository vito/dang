// Command demo shows multiple bubbletea v2 bubbles embedded inside a
// pitui TUI, each wrapped in a border. Tab switches focus between them.
//
// Usage:
//
//	go run ./pkg/pitui/teav2/demo
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/vito/dang/pkg/pitui"
	"github.com/vito/dang/pkg/pitui/teav2"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	term := pitui.NewProcessTerminal()
	tui := pitui.New(term)

	// Enable render debug logging.
	logPath := "/tmp/dang_render_debug.log"
	debugFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open debug log: %w", err)
	}
	defer debugFile.Close()
	tui.SetDebugWriter(debugFile)

	// ── List ───────────────────────────────────────────────────
	items := []list.Item{
		langItem{"Go", "Fast, simple, concurrent"},
		langItem{"Rust", "Safe, fast, fearless concurrency"},
		langItem{"Python", "Readable, versatile, batteries included"},
		langItem{"TypeScript", "JavaScript with types"},
		langItem{"Haskell", "Pure, lazy, strongly typed"},
		langItem{"Elixir", "Functional, concurrent, fault-tolerant"},
		langItem{"Zig", "Low-level control, no hidden allocations"},
		langItem{"Dang", "Pipelines, types, Dagger-native"},
	}
	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("63")).
		BorderLeftForeground(lipgloss.Color("63"))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("241")).
		BorderLeftForeground(lipgloss.Color("63"))

	lm := list.New(items, delegate, 60, 12)
	lm.Title = "Languages"
	lm.SetShowHelp(false)
	lm.SetShowStatusBar(false)
	listComp := teav2.New(lm)
	listBox := newBorderBox("List", listComp, 14)

	// ── Table ──────────────────────────────────────────────────
	cols := []table.Column{
		{Title: "Name", Width: 14},
		{Title: "Typing", Width: 12},
		{Title: "Paradigm", Width: 20},
	}
	rows := []table.Row{
		{"Go", "Static", "Imperative, Concurrent"},
		{"Rust", "Static", "Multi-paradigm"},
		{"Python", "Dynamic", "Multi-paradigm"},
		{"TypeScript", "Static", "Multi-paradigm"},
		{"Haskell", "Static", "Purely Functional"},
		{"Elixir", "Dynamic", "Functional, Concurrent"},
		{"Zig", "Static", "Imperative"},
		{"Dang", "Static", "Functional, Pipelines"},
	}
	tm := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithWidth(60),
		table.WithHeight(10),
		table.WithFocused(true),
	)
	tableComp := teav2.New(tm)
	tableBox := newBorderBox("Table", tableComp, 10)

	// ── Viewport ───────────────────────────────────────────────
	content := strings.Join([]string{
		"Welcome to the pitui + bubbletea v2 demo!",
		"",
		"This demo shows three bubbletea bubbles — a list, a table,",
		"and a viewport — each embedded as a pitui component inside",
		"a bordered panel.",
		"",
		"Press Tab to cycle focus between panels. The focused panel",
		"gets a highlighted border. Keyboard input (arrows, filtering,",
		"scrolling) is routed to whichever panel has focus.",
		"",
		"Press q or Ctrl+C to quit.",
		"",
		"The border, focus management, and layout are all plain pitui",
		"components — no bubbletea Program is running. Each bubble is",
		"just a Model whose View() is called during pitui's render",
		"loop, and whose Update() is fed parsed key events from raw",
		"terminal input.",
	}, "\n")
	vm := viewport.New(viewport.WithWidth(60), viewport.WithHeight(8))
	vm.SetContent(content)
	vpComp := teav2.New(vm)
	vpBox := newBorderBox("Viewport", vpComp, 10)

	// ── Layout ─────────────────────────────────────────────────
	panels := []*borderBox{listBox, tableBox, vpBox}
	focusIdx := 0

	tui.AddChild(listBox)
	tui.AddChild(tableBox)
	tui.AddChild(vpBox)

	setFocus := func(idx int) {
		focusIdx = idx
		for i, p := range panels {
			p.setFocused(i == idx)
		}
		tui.SetFocus(panels[idx])
	}
	setFocus(0)

	// ── Input ──────────────────────────────────────────────────
	quit := make(chan struct{})
	closeQuit := func() {
		select {
		case <-quit:
		default:
			close(quit)
		}
	}

	tui.AddInputListener(func(data []byte) *pitui.InputListenerResult {
		switch {
		case pitui.Matches(data, pitui.KeyTab):
			setFocus((focusIdx + 1) % len(panels))
			return &pitui.InputListenerResult{Consume: true}
		case pitui.Matches(data, pitui.KeyCtrlC):
			closeQuit()
			return &pitui.InputListenerResult{Consume: true}
		case string(data) == "q":
			closeQuit()
			return &pitui.InputListenerResult{Consume: true}
		}
		return nil
	})

	fmt.Fprintf(os.Stderr, "Render debug → %s\n", logPath)
	fmt.Fprintf(os.Stderr, "Run 'go run ./cmd/render-debug' in another terminal for live charts.\n")

	if err := tui.Start(); err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
	case <-sigCh:
	}
	signal.Stop(sigCh)
	tui.Stop()
	return nil
}

// ── langItem ───────────────────────────────────────────────────────────────

type langItem struct{ title, desc string }

func (i langItem) Title() string       { return i.title }
func (i langItem) Description() string { return i.desc }
func (i langItem) FilterValue() string { return i.title }

// ── borderBox ──────────────────────────────────────────────────────────────

// borderBox is a pitui component that renders a child inside a bordered
// panel. The border style changes depending on focus.
type borderBox struct {
	pitui.Compo
	title   string
	child   pitui.Component
	height  int // inner height (lines of child content visible)
	focused bool
}

func newBorderBox(title string, child pitui.Component, height int) *borderBox {
	b := &borderBox{title: title, child: child, height: height}
	b.Update()
	return b
}

func (b *borderBox) setFocused(focused bool) {
	if b.focused == focused {
		return
	}
	b.focused = focused
	b.Update()
}

var (
	focusedBorder  = lipgloss.Color("63")
	blurredBorder  = lipgloss.Color("241")
	focusedTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	blurredTitle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

func (b *borderBox) Render(ctx pitui.RenderContext) pitui.RenderResult {
	innerWidth := ctx.Width - 4 // │ + space + content + space + │
	if innerWidth < 1 {
		innerWidth = 1
	}

	childCtx := pitui.RenderContext{
		Width:  innerWidth,
		Height: b.height,
	}
	childResult := b.RenderChild(b.child, childCtx)

	// Pad or truncate child lines to the fixed inner height.
	content := childResult.Lines
	for len(content) < b.height {
		content = append(content, "")
	}
	if len(content) > b.height {
		content = content[:b.height]
	}

	// Pick border style.
	borderColor := blurredBorder
	titleStyle := blurredTitle
	if b.focused {
		borderColor = focusedBorder
		titleStyle = focusedTitle
	}
	bc := lipgloss.NewStyle().Foreground(borderColor)

	// Build the box.
	lines := make([]string, 0, b.height+2)

	// Top border: ╭─ Title ────────╮
	titleStr := titleStyle.Render(" " + b.title + " ")
	topFill := ctx.Width - 4 - lipgloss.Width(titleStr)
	if topFill < 0 {
		topFill = 0
	}
	top := bc.Render("╭─") + titleStr + bc.Render(strings.Repeat("─", topFill)+"─╮")
	lines = append(lines, top)

	// Content lines: │ content │
	for _, line := range content {
		lineWidth := lipgloss.Width(line)
		pad := innerWidth - lineWidth
		if pad < 0 {
			pad = 0
		}
		lines = append(lines,
			bc.Render("│ ")+line+strings.Repeat(" ", pad)+bc.Render(" │"),
		)
	}

	// Bottom border: ╰────────────────╯
	bottom := bc.Render("╰" + strings.Repeat("─", ctx.Width-2) + "╯")
	lines = append(lines, bottom)

	return pitui.RenderResult{Lines: lines}
}

func (b *borderBox) HandleInput(data []byte) {
	if ic, ok := b.child.(pitui.Interactive); ok {
		ic.HandleInput(data)
	}
}
