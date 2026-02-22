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

func (c *Counter) HandleKeyPress(_ pitui.EventContext, ev uv.KeyPressEvent) bool {
	c.Count++
	c.Update()
	return true
}

func (c *Counter) SetFocused(_ pitui.EventContext, focused bool) { c.focused = focused }

// Banner is a static component that renders a multi-line banner.
type Banner struct {
	pitui.Compo
	Text string
}

func (b *Banner) Render(ctx pitui.RenderContext) pitui.RenderResult {
	var lines []string
	for line := range strings.SplitSeq(b.Text, "\n") {
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

		// Start the TUI.
		if err := tui.Start(); err != nil {
			panic(err)
		}
		defer tui.Stop()

		// Dispatch component setup to the UI goroutine.
		// All component state mutations (AddChild, SetFocus, etc.)
		// must happen on the UI goroutine — either inside a Dispatch
		// callback or inside an event handler (HandleKeyPress, etc.).
		counter := &Counter{}
		tui.Dispatch(func() {
			tui.AddChild(&Banner{Text: "=== My App ==="})
			tui.AddChild(counter)
			tui.SetFocus(counter)
		})

		// In a real app, you'd block on a signal or channel.
		time.Sleep(10 * time.Second)
	}

	fmt.Println("ok")
	// Output: ok
}
