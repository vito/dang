package main

import (
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/vito/dang/pkg/pitui"
)

func renderStressCmd() *cobra.Command {
	var lines int

	cmd := &cobra.Command{
		Use:   "render-stress",
		Short: "Interactive stress test for pitui rendering",
		Long: `Launches a TUI with a large scrollable log and interactive controls
to exercise different rendering scenarios. Render debug is automatically
enabled and streamed to /tmp/dang_render_debug.log.

Controls:
  v         Toggle verbose mode — all log lines expand with extra detail,
            causing massive off-screen repaint (full redraw).
  c         Toggle colorized mode — changes ANSI styling on every line.
  a         Append 10 new log lines (scroll-append path).
  A         Append 100 new log lines.
  d         Delete last 10 log lines (shrink path).
  o         Toggle overlay (completion-menu-style popup).
  s         Start/stop a spinner (continuous repaints from timer).
  r         Force full redraw (RequestRender(true)).
  1-9       Set repaint rate: continuously modify line N*10 every 50ms.
  0         Stop continuous repaint.
  Ctrl+C    Quit.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRenderStress(lines)
		},
	}

	cmd.Flags().IntVar(&lines, "lines", 200, "Initial number of log lines")
	return cmd
}

// ── stress log component ───────────────────────────────────────────────────

type stressLog struct {
	mu       sync.Mutex
	entries  []stressEntry
	verbose  bool
	colorize bool
	dirty    bool
	cached   []string
}

type stressEntry struct {
	ts      time.Time
	level   string
	message string
}

func newStressLog(n int) *stressLog {
	levels := []string{"INFO", "DEBUG", "WARN", "ERROR", "TRACE"}
	modules := []string{"pitui.render", "pitui.diff", "pitui.overlay", "pitui.input", "pitui.cursor",
		"dang.eval", "dang.parse", "dang.infer", "dang.graphql", "dang.repl"}
	messages := []string{
		"processing request",
		"cache miss for key",
		"connection established",
		"rendering frame",
		"overlay composited",
		"differential update applied",
		"component tree walked",
		"escape sequence generated",
		"viewport scrolled",
		"cursor repositioned",
		"input dispatched to handler",
		"focus changed",
		"style computation completed",
		"width calculation for line",
		"ANSI truncation applied",
	}

	entries := make([]stressEntry, n)
	base := time.Now().Add(-time.Duration(n) * 100 * time.Millisecond)
	for i := range entries {
		entries[i] = stressEntry{
			ts:      base.Add(time.Duration(i) * 100 * time.Millisecond),
			level:   levels[rand.Intn(len(levels))],
			message: fmt.Sprintf("[%s] %s id=%d latency=%dµs", modules[rand.Intn(len(modules))], messages[rand.Intn(len(messages))], rand.Intn(10000), rand.Intn(5000)),
		}
	}
	return &stressLog{entries: entries, dirty: true}
}

func (s *stressLog) Invalidate() {
	s.mu.Lock()
	s.dirty = true
	s.cached = nil
	s.mu.Unlock()
}

func (s *stressLog) Render(ctx pitui.RenderContext) pitui.RenderResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.dirty && s.cached != nil {
		return pitui.RenderResult{Lines: s.cached, Dirty: false}
	}

	lines := make([]string, 0, len(s.entries)*2)
	for _, e := range s.entries {
		ts := e.ts.Format("15:04:05.000")
		var levelStyled string
		if s.colorize {
			switch e.level {
			case "ERROR":
				levelStyled = "\x1b[31m" + e.level + "\x1b[0m"
			case "WARN":
				levelStyled = "\x1b[33m" + e.level + "\x1b[0m"
			case "DEBUG":
				levelStyled = "\x1b[36m" + e.level + "\x1b[0m"
			case "TRACE":
				levelStyled = "\x1b[90m" + e.level + "\x1b[0m"
			default:
				levelStyled = "\x1b[32m" + e.level + "\x1b[0m"
			}
		} else {
			levelStyled = e.level
		}
		line := fmt.Sprintf("%s %-5s %s", ts, levelStyled, e.message)
		if pitui.VisibleWidth(line) > ctx.Width {
			line = pitui.Truncate(line, ctx.Width, "")
		}
		lines = append(lines, line)

		if s.verbose {
			detail := fmt.Sprintf("         → stack: %s | goroutine: %d | alloc: %dKB",
				randomStack(), rand.Intn(500), rand.Intn(8192))
			if pitui.VisibleWidth(detail) > ctx.Width {
				detail = pitui.Truncate(detail, ctx.Width, "")
			}
			if s.colorize {
				detail = "\x1b[90m" + detail + "\x1b[0m"
			}
			lines = append(lines, detail)
		}
	}

	s.cached = lines
	s.dirty = false
	return pitui.RenderResult{Lines: lines, Dirty: true}
}

func randomStack() string {
	frames := []string{
		"main.run", "pitui.doRender", "pitui.compositeOverlays",
		"pitui.CompositeLineAt", "ansi.Truncate", "dang.Eval",
		"runtime.goexit", "net/http.serve", "pitui.handleInput",
	}
	n := 2 + rand.Intn(3)
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, frames[rand.Intn(len(frames))])
	}
	return strings.Join(parts, " → ")
}

// ── status bar component ───────────────────────────────────────────────────

type stressStatusBar struct {
	mu      sync.Mutex
	line    string
}

func (s *stressStatusBar) Invalidate() {}

func (s *stressStatusBar) set(line string) {
	s.mu.Lock()
	s.line = line
	s.mu.Unlock()
}

func (s *stressStatusBar) Render(ctx pitui.RenderContext) pitui.RenderResult {
	s.mu.Lock()
	line := s.line
	s.mu.Unlock()
	if pitui.VisibleWidth(line) > ctx.Width {
		line = pitui.Truncate(line, ctx.Width, "")
	}
	return pitui.RenderResult{Lines: []string{line}, Dirty: true}
}

// ── stress overlay component ───────────────────────────────────────────────

type stressOverlay struct {
	lines []string
}

func (s *stressOverlay) Invalidate() {}

func (s *stressOverlay) Render(ctx pitui.RenderContext) pitui.RenderResult {
	return pitui.RenderResult{Lines: s.lines, Dirty: true}
}

// ── run ────────────────────────────────────────────────────────────────────

func runRenderStress(initialLines int) error {
	term := pitui.NewProcessTerminal()
	tui := pitui.New(term)

	// Auto-enable render debug.
	logPath := "/tmp/dang_render_debug.log"
	debugFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open debug log: %w", err)
	}
	defer debugFile.Close()
	tui.SetDebugWriter(debugFile)

	log := newStressLog(initialLines)
	statusBar := &stressStatusBar{}
	statusBar.set("\x1b[7m v=verbose c=color a/A=append d=delete o=overlay s=spinner r=force 1-9/0=continuous Ctrl+C=quit \x1b[0m")

	tui.AddChild(log)
	tui.AddChild(statusBar)

	if err := tui.Start(); err != nil {
		return fmt.Errorf("TUI start: %w", err)
	}

	// State.
	var (
		overlayHandle    *pitui.OverlayHandle
		spinner          *pitui.Spinner
		spinnerSlot      *pitui.Slot
		spinnerRunning   bool
		continuousTicker *time.Ticker
		continuousDone   chan struct{}
		continuousLine   int
	)

	// Spinner setup (not added to tree until toggled).
	spinner = pitui.NewSpinner(tui)
	spinner.Label = "evaluating..."
	spinner.Style = func(s string) string { return "\x1b[35m" + s + "\x1b[0m" }
	spinnerSlot = pitui.NewSlot(nil)
	// Insert spinner slot between log and status bar.
	tui.RemoveChild(statusBar)
	tui.AddChild(spinnerSlot)
	tui.AddChild(statusBar)

	stopContinuous := func() {
		if continuousTicker != nil {
			continuousTicker.Stop()
			close(continuousDone)
			continuousTicker = nil
		}
	}

	// Input handler.
	tui.AddInputListener(func(data []byte) *pitui.InputListenerResult {
		s := string(data)
		switch s {
		case "v":
			log.mu.Lock()
			log.verbose = !log.verbose
			log.dirty = true
			log.cached = nil
			v := log.verbose
			log.mu.Unlock()
			if v {
				statusBar.set("\x1b[7m VERBOSE ON — all lines expanded (off-screen repaint!) \x1b[0m")
			} else {
				statusBar.set("\x1b[7m VERBOSE OFF — compact view \x1b[0m")
			}
			tui.RequestRender(false)
			return &pitui.InputListenerResult{Consume: true}

		case "c":
			log.mu.Lock()
			log.colorize = !log.colorize
			log.dirty = true
			log.cached = nil
			c := log.colorize
			log.mu.Unlock()
			if c {
				statusBar.set("\x1b[7m COLOR ON — ANSI styles changed on every line \x1b[0m")
			} else {
				statusBar.set("\x1b[7m COLOR OFF — plain text \x1b[0m")
			}
			tui.RequestRender(false)
			return &pitui.InputListenerResult{Consume: true}

		case "a":
			appendStressLines(log, 10)
			statusBar.set("\x1b[7m +10 lines appended \x1b[0m")
			tui.RequestRender(false)
			return &pitui.InputListenerResult{Consume: true}

		case "A":
			appendStressLines(log, 100)
			statusBar.set("\x1b[7m +100 lines appended \x1b[0m")
			tui.RequestRender(false)
			return &pitui.InputListenerResult{Consume: true}

		case "d":
			log.mu.Lock()
			if len(log.entries) > 10 {
				log.entries = log.entries[:len(log.entries)-10]
			} else {
				log.entries = nil
			}
			log.dirty = true
			log.cached = nil
			n := len(log.entries)
			log.mu.Unlock()
			statusBar.set(fmt.Sprintf("\x1b[7m deleted 10 lines (now %d) \x1b[0m", n))
			tui.RequestRender(false)
			return &pitui.InputListenerResult{Consume: true}

		case "o":
			if overlayHandle != nil {
				overlayHandle.Hide()
				overlayHandle = nil
				statusBar.set("\x1b[7m overlay hidden \x1b[0m")
			} else {
				overlay := &stressOverlay{lines: []string{
					"╭──────────────────╮",
					"│ Completions      │",
					"│  container       │",
					"│  directory       │",
					"│  withExec        │",
					"│  withMountedDir  │",
					"│  stdout          │",
					"│  stderr          │",
					"│  file            │",
					"╰──────────────────╯",
				}}
				overlayHandle = tui.ShowOverlay(overlay, &pitui.OverlayOptions{
					Width:   pitui.SizeAbs(22),
					Anchor:  pitui.AnchorBottomLeft,
					OffsetX: 2,
					OffsetY: -1,
					NoFocus: true,
				})
				statusBar.set("\x1b[7m overlay shown (press o to hide) \x1b[0m")
			}
			tui.RequestRender(false)
			return &pitui.InputListenerResult{Consume: true}

		case "s":
			if spinnerRunning {
				spinner.Stop()
				spinnerSlot.Set(nil)
				spinnerRunning = false
				statusBar.set("\x1b[7m spinner stopped \x1b[0m")
			} else {
				spinnerSlot.Set(&evalSpinnerLine{spinner: spinner})
				spinner.Start()
				spinnerRunning = true
				statusBar.set("\x1b[7m spinner running (continuous repaints) \x1b[0m")
			}
			tui.RequestRender(false)
			return &pitui.InputListenerResult{Consume: true}

		case "r":
			statusBar.set("\x1b[7m forced full redraw \x1b[0m")
			tui.RequestRender(true)
			return &pitui.InputListenerResult{Consume: true}

		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			stopContinuous()
			continuousLine = int(s[0]-'0') * 10
			continuousDone = make(chan struct{})
			continuousTicker = time.NewTicker(50 * time.Millisecond)
			statusBar.set(fmt.Sprintf("\x1b[7m continuous repaint on line %d every 50ms (0 to stop) \x1b[0m", continuousLine))
			tui.RequestRender(false)
			go func() {
				tick := continuousTicker
				done := continuousDone
				target := continuousLine
				for {
					select {
					case <-done:
						return
					case <-tick.C:
						log.mu.Lock()
						if target < len(log.entries) {
							log.entries[target].message = fmt.Sprintf("[continuous] tick %d latency=%dµs",
								time.Now().UnixMicro()%100000, rand.Intn(5000))
							log.dirty = true
							log.cached = nil
						}
						log.mu.Unlock()
						tui.RequestRender(false)
					}
				}
			}()
			return &pitui.InputListenerResult{Consume: true}

		case "0":
			stopContinuous()
			statusBar.set("\x1b[7m continuous repaint stopped \x1b[0m")
			tui.RequestRender(false)
			return &pitui.InputListenerResult{Consume: true}

		case pitui.KeyCtrlC:
			return nil // let it through to stop
		}
		return nil
	})

	fmt.Fprintf(os.Stderr, "Render debug → %s\n", logPath)
	fmt.Fprintf(os.Stderr, "Run 'dang render-debug' in another terminal for live charts.\n")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	stopContinuous()
	if spinnerRunning {
		spinner.Stop()
	}
	signal.Stop(sigCh)
	tui.Stop()
	fmt.Println("Done.")
	return nil
}

func appendStressLines(log *stressLog, n int) {
	levels := []string{"INFO", "DEBUG", "WARN"}
	log.mu.Lock()
	for i := 0; i < n; i++ {
		log.entries = append(log.entries, stressEntry{
			ts:      time.Now(),
			level:   levels[rand.Intn(len(levels))],
			message: fmt.Sprintf("[append] new line %d val=%d", len(log.entries), rand.Intn(99999)),
		})
	}
	log.dirty = true
	log.cached = nil
	log.mu.Unlock()
}
