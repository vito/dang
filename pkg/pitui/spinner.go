package pitui

import (
	"time"
)

// Spinner is a component that shows an animated spinner.
type Spinner struct {
	// Style wraps each frame (e.g. to apply color). May be nil.
	Style func(string) string
	// Label is displayed after the spinner frame.
	Label string

	frames   []string
	interval time.Duration
	start    time.Time
	tui      *TUI
	ticker   *time.Ticker
	stopped  bool
}

// NewSpinner creates a dot-style spinner.
func NewSpinner(tui *TUI) *Spinner {
	return &Spinner{
		frames:   []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
		interval: 80 * time.Millisecond,
		tui:      tui,
	}
}

// Start begins the spinner animation.
func (s *Spinner) Start() {
	s.start = time.Now()
	s.stopped = false
	s.ticker = time.NewTicker(s.interval)
	go func() {
		for range s.ticker.C {
			if s.stopped {
				return
			}
			s.tui.RequestRender(false)
		}
	}()
}

// Stop ends the spinner animation.
func (s *Spinner) Stop() {
	s.stopped = true
	if s.ticker != nil {
		s.ticker.Stop()
	}
}

func (s *Spinner) Invalidate() {}

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
		Dirty: true, // always dirty (animating)
	}
}
