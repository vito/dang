package pitui_test

import (
	"fmt"
	"strings"
	"time"

	"github.com/vito/dang/pkg/pitui"
)

// Counter is a simple interactive component that displays a count and
// increments it when any key is pressed.
type Counter struct {
	Count   int
	focused bool
}

func (c *Counter) Render(width int) []string {
	line := fmt.Sprintf("Count: %d (press any key)", c.Count)
	if c.focused {
		// Emit cursor marker at end of visible text.
		line += pitui.CursorMarker
	}
	if pitui.VisibleWidth(line) > width {
		line = pitui.Truncate(line, width, "...")
	}
	return []string{line}
}

func (c *Counter) HandleInput(data []byte) {
	// Ctrl-C: the caller should handle exit.
	c.Count++
}

func (c *Counter) SetFocused(focused bool) { c.focused = focused }
func (c *Counter) Invalidate()             {}

// Banner is a static component that renders a multi-line banner.
type Banner struct {
	Text string
}

func (b *Banner) Render(width int) []string {
	var lines []string
	for _, line := range strings.Split(b.Text, "\n") {
		if pitui.VisibleWidth(line) > width {
			line = pitui.Truncate(line, width, "")
		}
		lines = append(lines, line)
	}
	return lines
}

func (b *Banner) Invalidate() {}

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
