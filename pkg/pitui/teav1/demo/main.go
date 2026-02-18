// Command demo shows a bubbletea v1 list bubble embedded inside a pitui
// TUI. The list is a standard bubbles/list component — pitui handles the
// terminal, input parsing, and differential rendering while the bubble
// handles its own state and view.
//
// Usage:
//
//	go run ./pkg/pitui/teav1/demo
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"

	"github.com/vito/dang/pkg/pitui"
	"github.com/vito/dang/pkg/pitui/teav1"
)

// item implements list.Item and list.DefaultItem.
type item struct {
	title, desc string
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }

// listModel wraps list.Model to satisfy tea.Model.
// Bubbles v1 list doesn't implement the interface directly because
// Update returns (list.Model, tea.Cmd) instead of (tea.Model, tea.Cmd).
type listModel struct {
	list list.Model
}

func (m listModel) Init() tea.Cmd                           { return nil }
func (m listModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) { var cmd tea.Cmd; m.list, cmd = m.list.Update(msg); return m, cmd }
func (m listModel) View() string                            { return m.list.View() }

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

	// Build a list of programming languages.
	items := []list.Item{
		item{"Go", "Fast, simple, concurrent"},
		item{"Rust", "Safe, fast, fearless concurrency"},
		item{"Python", "Readable, versatile, batteries included"},
		item{"TypeScript", "JavaScript with types"},
		item{"Haskell", "Pure, lazy, strongly typed"},
		item{"OCaml", "Fast, expressive, functional"},
		item{"Elixir", "Functional, concurrent, fault-tolerant"},
		item{"Zig", "Low-level control, no hidden allocations"},
		item{"Nim", "Efficient, expressive, elegant"},
		item{"Gleam", "Type-safe, functional, friendly"},
		item{"Dang", "Pipelines, types, Dagger-native"},
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("63")).
		BorderLeftForeground(lipgloss.Color("63"))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("241")).
		BorderLeftForeground(lipgloss.Color("63"))

	m := list.New(items, delegate, 60, 20)
	m.Title = "Languages"
	m.SetShowHelp(true)

	// Wrap the list bubble as a pitui Component.
	comp := teav1.New(listModel{list: m})

	// Header above the list.
	header := &staticText{line: dimStyle.Render("  bubbletea v1 list bubble embedded in pitui  ")}
	header.Update()

	tui.AddChild(header)
	tui.AddChild(comp)
	tui.SetFocus(comp)

	// Handle quit from the bubble (when the user presses 'q').
	quit := make(chan struct{})
	comp.OnQuit(func() {
		select {
		case <-quit:
		default:
			close(quit)
		}
	})

	fmt.Fprintf(os.Stderr, "Render debug → %s\n", logPath)
	fmt.Fprintf(os.Stderr, "Run 'go run ./cmd/render-debug' in another terminal for live charts.\n")

	if err := tui.Start(); err != nil {
		return err
	}

	// Also intercept Ctrl+C at the pitui level.
	tui.AddInputListener(func(data []byte) *pitui.InputListenerResult {
		if string(data) == "\x03" { // Ctrl+C
			select {
			case <-quit:
			default:
				close(quit)
			}
			return &pitui.InputListenerResult{Consume: true}
		}
		return nil
	})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
	case <-sigCh:
	}

	signal.Stop(sigCh)
	tui.Stop()

	// Show what was selected.
	if lm, ok := comp.Model().(listModel); ok {
		if sel, ok := lm.list.SelectedItem().(item); ok {
			fmt.Printf("Selected: %s — %s\n", sel.title, sel.desc)
		}
	}
	return nil
}

var dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

type staticText struct {
	pitui.Compo
	line string
}

func (s *staticText) Render(ctx pitui.RenderContext) pitui.RenderResult {
	return pitui.RenderResult{Lines: []string{s.line}}
}
