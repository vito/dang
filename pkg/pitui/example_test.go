package pitui_test

import (
	"fmt"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/dang/pkg/pitui"
)

// Counter is a simple interactive component that displays a count and
// increments it when any key is pressed.
type Counter struct {
	pitui.Compo
	Count   int
	focused bool
}

func (c *Counter) Render(ctx pitui.RenderContext) pitui.RenderResult {
	line := fmt.Sprintf("Count: %d (press any key)", c.Count)
	if pitui.VisibleWidth(line) > ctx.Width {
		line = pitui.Truncate(line, ctx.Width, "...")
	}
	var cursor *pitui.CursorPos
	if c.focused {
		cursor = &pitui.CursorPos{Row: 0, Col: pitui.VisibleWidth(line)}
	}
	return pitui.RenderResult{
		Lines:  []string{line},
		Cursor: cursor,
	}
}

func (c *Counter) HandleKeyPress(ev uv.KeyPressEvent) {
	c.Count++
	c.Update()
}

func (c *Counter) SetFocused(focused bool) { c.focused = focused }

// Banner is a static component that renders a multi-line banner.
type Banner struct {
	pitui.Compo
	Text string
}

func (b *Banner) Render(ctx pitui.RenderContext) pitui.RenderResult {
	var lines []string
	for _, line := range strings.Split(b.Text, "\n") {
		if pitui.VisibleWidth(line) > ctx.Width {
			line = pitui.Truncate(line, ctx.Width, "")
		}
		lines = append(lines, line)
	}
	return pitui.RenderResult{Lines: lines}
}

func Example() {
	// This example shows the basic wiring. In a real app you'd use
	// NewProcessTerminal() and handle Ctrl-C properly.
	_ = func() {
		term := pitui.NewProcessTerminal()
		tui := pitui.New(term)

		// Add a static banner.
		tui.AddChild(&Banner{Text: "=== My App ==="})

		// Add an interactive counter and give it focus.
		counter := &Counter{}
		tui.AddChild(counter)
		tui.SetFocus(counter)

		// Start the TUI.
		if err := tui.Start(); err != nil {
			panic(err)
		}
		defer tui.Stop()

		// In a real app, you'd block on a signal or channel.
		time.Sleep(10 * time.Second)
	}

	fmt.Println("ok")
	// Output: ok
}
