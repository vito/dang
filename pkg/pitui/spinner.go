package pitui

import (
	"time"
)

// Spinner is a component that shows an animated spinner.
type Spinner struct {
	Compo

	// Style wraps each frame (e.g. to apply color). May be nil.
	Style func(string) string
	// Label is displayed after the spinner frame.
	Label string

	frames   []string
	interval time.Duration
	start    time.Time

	ticker *time.Ticker
	done   chan struct{}
}

// NewSpinner creates a dot-style spinner.
func NewSpinner() *Spinner {
	return &Spinner{
		frames:   []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
		interval: 80 * time.Millisecond,
	}
}

// Start begins the spinner animation. Must be called on the UI goroutine
// (from an event handler, a Dispatch callback, or before TUI.Start).
func (s *Spinner) Start() {
	s.start = time.Now()
	s.done = make(chan struct{})
	s.ticker = time.NewTicker(s.interval)

	// Capture locally so the goroutine never touches Spinner fields.
	done := s.done
	ticker := s.ticker

	go func() {
		for {
			select {
			case <-ticker.C:
				s.Update()
			case <-done:
				return
			}
		}
	}()
}

// Stop ends the spinner animation. Must be called on the UI goroutine
// (from an event handler, a Dispatch callback, or before TUI.Start).
func (s *Spinner) Stop() {
	if s.done != nil {
		close(s.done)
		s.done = nil
	}
	if s.ticker != nil {
		s.ticker.Stop()
		s.ticker = nil
	}
}

func (s *Spinner) Render(ctx RenderContext) RenderResult {
	elapsed := time.Since(s.start)
	idx := int(elapsed/s.interval) % len(s.frames)
	frame := s.frames[idx]
	if s.Style != nil {
		frame = s.Style(frame)
	}
	line := frame + " " + s.Label
	if VisibleWidth(line) > ctx.Width {
		line = Truncate(line, ctx.Width, "")
	}
	return RenderResult{
		Lines: []string{line},
	}
}
