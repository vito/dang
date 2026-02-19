package pitui

import (
	"context"
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
	cancel context.CancelFunc
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
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.ticker = time.NewTicker(s.interval)

	// Capture locally so the goroutine never touches Spinner fields.
	ticker := s.ticker

	go func() {
		for {
			select {
			case <-ticker.C:
				s.Update()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop ends the spinner animation. Must be called on the UI goroutine
// (from an event handler, a Dispatch callback, or before TUI.Start).
func (s *Spinner) Stop() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
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
