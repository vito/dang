// Command demo shows a bubbletea v2 list bubble embedded inside a pitui
// TUI. The list is a standard bubbles/list component — pitui handles the
// terminal, input parsing, and differential rendering while the bubble
// handles its own state and view.
//
// Usage:
//
//	go run ./pkg/pitui/teav2/demo
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"

	"github.com/vito/dang/pkg/pitui"
	"github.com/vito/dang/pkg/pitui/teav2"
)

// item implements list.Item and list.DefaultItem.
type item struct {
	title, desc string
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	term := pitui.NewProcessTerminal()
	tui := pitui.New(term)

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
	comp := teav2.New(m)

	// Header above the list.
	header := &staticText{line: dimStyle.Render("  bubbletea list bubble embedded in pitui  ")}
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

	if err := tui.Start(); err != nil {
		return err
	}

	// Also intercept Ctrl+C at the pitui level.
	tui.AddInputListener(func(data []byte) *pitui.InputListenerResult {
		if string(data) == pitui.KeyCtrlC {
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
	selected := comp.Model().SelectedItem()
	if sel, ok := selected.(item); ok {
		fmt.Printf("Selected: %s — %s\n", sel.title, sel.desc)
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
