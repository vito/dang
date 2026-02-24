// Command demos is a consolidated launcher for pitui demo programs.
//
// Usage:
//
//	go run ./pkg/pitui/demos                # interactive menu
//	go run ./pkg/pitui/demos keygen         # Mandelbrot fractal
//	go run ./pkg/pitui/demos grid           # interactive color grid
//	go run ./pkg/pitui/demos logs           # scrollable log stress test
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/dang/pkg/pitui"
)

// sharedTerm is the single ProcessTerminal for the process. It is created
// once and reused across the selector and whichever demo is launched so
// that only one goroutine ever reads stdin.
var sharedTerm = pitui.NewProcessTerminal()

type demoEntry struct {
	name string
	desc string
	run  func()
}

var demoList = []demoEntry{
	{"keygen", "Animated Mandelbrot fractal zoom with chrome bar & inline editing", keygenMain},
	{"grid", "Interactive colored grid with hover, click, and keyboard navigation", gridMain},
	{"logs", "Scrollable log viewer stress test with overlays & spinner", logsMain},
}

func main() {
	if len(os.Args) >= 2 {
		name := os.Args[1]
		// Shift args so flag.Parse() in sub-demos sees the right flags.
		os.Args = append(os.Args[:1], os.Args[2:]...)
		for _, d := range demoList {
			if d.name == name {
				d.run()
				return
			}
		}
		fmt.Fprintf(os.Stderr, "unknown demo: %s\n", name)
		os.Exit(1)
	}

	selected := runSelector()
	if selected < 0 {
		return
	}
	demoList[selected].run()
}

func runSelector() int {
	tui := pitui.New(sharedTerm)

	menu := &selectorView{
		done: make(chan struct{}),
		sel:  -1,
	}
	menu.Update()

	tui.Dispatch(func() {
		tui.AddChild(menu)
		tui.SetFocus(menu)
	})

	if err := tui.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-menu.done:
	case <-sigCh:
		menu.sel = -1
	}
	signal.Stop(sigCh)
	tui.Stop()

	return menu.sel
}

// ── Selector TUI ───────────────────────────────────────────────────────────

type selectorView struct {
	pitui.Compo
	cursor int
	sel    int
	done   chan struct{}
}

func (s *selectorView) HandleKeyPress(_ pitui.EventContext, ev uv.KeyPressEvent) bool {
	key := uv.Key(ev)
	switch {
	case key.Text == "q" || (key.Code == 'c' && key.Mod == uv.ModCtrl):
		s.sel = -1
		close(s.done)
		return true
	case key.Code == uv.KeyUp, key.Text == "k":
		if s.cursor > 0 {
			s.cursor--
			s.Update()
		}
		return true
	case key.Code == uv.KeyDown, key.Text == "j":
		if s.cursor < len(demoList)-1 {
			s.cursor++
			s.Update()
		}
		return true
	case key.Code == uv.KeyEnter:
		s.sel = s.cursor
		close(s.done)
		return true
	}
	return false
}

var (
	selTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	selItemStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	selCurStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	selDescStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	selHintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

func (s *selectorView) Render(_ pitui.RenderContext) pitui.RenderResult {
	var lines []string
	lines = append(lines, "")
	lines = append(lines, selTitleStyle.Render("  ◆ pitui demos"))
	lines = append(lines, "")

	for i, d := range demoList {
		prefix := "  "
		style := selItemStyle
		if i == s.cursor {
			prefix = "▸ "
			style = selCurStyle
		}
		lines = append(lines, style.Render(prefix+d.name))
		lines = append(lines, selDescStyle.Render("    "+d.desc))
		lines = append(lines, "")
	}

	hints := strings.Join([]string{"↑↓/jk navigate", "enter select", "q quit"}, "  •  ")
	lines = append(lines, selHintStyle.Render("  "+hints))

	return pitui.RenderResult{Lines: lines}
}
