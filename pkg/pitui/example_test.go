package pitui_test

import (
	"fmt"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/dang/pkg/pitui"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	countStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	hintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	keyStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
)

// Label is a static single-line component.
type Label struct {
	pitui.Compo
	Text string
}

func (l *Label) Render(ctx pitui.RenderContext) pitui.RenderResult {
	line := l.Text
	if pitui.VisibleWidth(line) > ctx.Width {
		line = pitui.Truncate(line, ctx.Width, "…")
	}
	return pitui.RenderResult{Lines: []string{line}}
}

// Counter increments on each key press, and 'q' quits.
type Counter struct {
	pitui.Compo
	Count   int
	quit    func()
	focused bool
}

func (c *Counter) Render(ctx pitui.RenderContext) pitui.RenderResult {
	line := countStyle.Render(fmt.Sprintf("%d", c.Count))
	if pitui.VisibleWidth(line) > ctx.Width {
		line = pitui.Truncate(line, ctx.Width, "…")
	}
	return pitui.RenderResult{Lines: []string{line}}
}

var _ pitui.Interactive = (*Counter)(nil)

func (c *Counter) HandleKeyPress(_ pitui.EventContext, ev uv.KeyPressEvent) bool {
	if ev.Text == "q" {
		c.quit()
		return true
	}
	c.Count++
	c.Update()
	return true
}

var _ pitui.Focusable = (*Counter)(nil)

func (c *Counter) SetFocused(_ pitui.EventContext, focused bool) { c.focused = focused }

func Example() {
	term := pitui.NewProcessTerminal()
	tui := pitui.New(term)

	if err := tui.Start(); err != nil {
		panic(err)
	}
	defer tui.Stop()

	done := make(chan struct{})
	counter := &Counter{quit: func() { close(done) }}

	// All component mutations must happen on the UI goroutine.
	tui.Dispatch(func() {
		tui.AddChild(&Label{Text: titleStyle.Render("● Counter")})
		tui.AddChild(counter)
		tui.AddChild(&Label{
			Text: keyStyle.Render("any key") + hintStyle.Render(" increment  ") +
				keyStyle.Render("q") + hintStyle.Render(" quit"),
		})
		tui.SetFocus(counter)
	})

	<-done
}
