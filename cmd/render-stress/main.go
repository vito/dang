// Command render-stress is an interactive stress test for pitui rendering.
// It creates a TUI with a large scrollable log and hotkeys to exercise
// every rendering code path. Render debug is automatically enabled.
//
// Usage:
//
//	go run ./cmd/render-stress
//	go run ./cmd/render-stress -lines 500
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/dang/pkg/pitui"
)

func main() {
	lines := flag.Int("lines", 200, "initial number of log lines")
	flag.Parse()

	if err := run(*lines); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(initialLines int) error {
	term := pitui.NewProcessTerminal()
	tui := pitui.New(term)

	// Auto-enable render debug.
	logPath := "/tmp/dang_render_debug.log"
	debugFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open debug log: %w", err)
	}
	defer debugFile.Close() //nolint:errcheck // best-effort close of debug log
	tui.SetDebugWriter(debugFile)

	log := newStressLog(initialLines)
	statusBar := &statusBarComponent{}
	statusBar.set("\x1b[7m v=verbose c=color a/A=append d=delete o=overlay s=spinner r=force 1-9/0=continuous q/Ctrl+C=quit \x1b[0m")

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
		continuousTicker *time.Ticker
		continuousDone   chan struct{}
	)

	quit := make(chan struct{})

	// Spinner setup.
	spinner = pitui.NewSpinner()
	spinner.Label = "evaluating..."
	spinner.Style = func(s string) string { return "\x1b[35m" + s + "\x1b[0m" }
	spinnerSlot = pitui.NewSlot(nil)
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

	doQuit := func() {
		select {
		case <-quit:
		default:
			close(quit)
		}
	}

	// Input handler.
	tui.AddInputListener(func(ctx pitui.EventContext, ev uv.Event) bool {
		kp, ok := ev.(uv.KeyPressEvent)
		if !ok {
			return false
		}
		key := uv.Key(kp)

		switch {
		case key.Text == "q" || (key.Code == 'c' && key.Mod == uv.ModCtrl):
			doQuit()
			return true

		case key.Text == "v":
			log.mu.Lock()
			log.verbose = !log.verbose
			v := log.verbose
			log.mu.Unlock()
			log.Update()
			if v {
				statusBar.set("\x1b[7m VERBOSE ON — all lines expanded (off-screen repaint!) \x1b[0m")
			} else {
				statusBar.set("\x1b[7m VERBOSE OFF — compact view \x1b[0m")
			}
			ctx.RequestRender(false)
			return true

		case key.Text == "c":
			log.mu.Lock()
			log.colorize = !log.colorize
			c := log.colorize
			log.mu.Unlock()
			log.Update()
			if c {
				statusBar.set("\x1b[7m COLOR ON — ANSI styles changed on every line \x1b[0m")
			} else {
				statusBar.set("\x1b[7m COLOR OFF — plain text \x1b[0m")
			}
			ctx.RequestRender(false)
			return true

		case key.Text == "a":
			appendLines(log, 10)
			statusBar.set("\x1b[7m +10 lines appended \x1b[0m")
			ctx.RequestRender(false)
			return true

		case key.Text == "A":
			appendLines(log, 100)
			statusBar.set("\x1b[7m +100 lines appended \x1b[0m")
			ctx.RequestRender(false)
			return true

		case key.Text == "d":
			log.mu.Lock()
			if len(log.entries) > 10 {
				log.entries = log.entries[:len(log.entries)-10]
			} else {
				log.entries = nil
			}
			n := len(log.entries)
			log.mu.Unlock()
			log.Update()
			statusBar.set(fmt.Sprintf("\x1b[7m deleted 10 lines (now %d) \x1b[0m", n))
			ctx.RequestRender(false)
			return true

		case key.Text == "o":
			if overlayHandle != nil {
				overlayHandle.Hide()
				overlayHandle = nil
				statusBar.set("\x1b[7m overlay hidden \x1b[0m")
			} else {
				overlay := &staticLines{lines: []string{
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
				overlayHandle = ctx.ShowOverlay(overlay, &pitui.OverlayOptions{
					Width:   pitui.SizeAbs(22),
					Anchor:  pitui.AnchorBottomLeft,
					OffsetX: 2,
					OffsetY: -1,
				})
				statusBar.set("\x1b[7m overlay shown (press o to hide) \x1b[0m")
			}
			ctx.RequestRender(false)
			return true

		case key.Text == "s":
			if spinnerSlot.Get() != nil {
				spinnerSlot.Set(nil)
				statusBar.set("\x1b[7m spinner stopped \x1b[0m")
			} else {
				spinnerSlot.Set(spinner)
				statusBar.set("\x1b[7m spinner running (continuous repaints) \x1b[0m")
			}
			ctx.RequestRender(false)
			return true

		case key.Text == "r":
			statusBar.set("\x1b[7m forced full redraw \x1b[0m")
			ctx.RequestRender(true)
			return true

		case len(key.Text) == 1 && key.Text[0] >= '1' && key.Text[0] <= '9':
			stopContinuous()
			target := int(key.Text[0]-'0') * 10
			continuousDone = make(chan struct{})
			continuousTicker = time.NewTicker(50 * time.Millisecond)
			statusBar.set(fmt.Sprintf("\x1b[7m continuous repaint on line %d every 50ms (0 to stop) \x1b[0m", target))
			ctx.RequestRender(false)
			go func() {
				tick := continuousTicker
				done := continuousDone
				for {
					select {
					case <-done:
						return
					case <-tick.C:
						log.mu.Lock()
						if target < len(log.entries) {
							log.entries[target].message = fmt.Sprintf("[continuous] tick %d latency=%dµs",
								time.Now().UnixMicro()%100000, rand.Intn(5000))
						}
						log.mu.Unlock()
						log.Update()
					}
				}
			}()
			return true

		case key.Text == "0":
			stopContinuous()
			statusBar.set("\x1b[7m continuous repaint stopped \x1b[0m")
			ctx.RequestRender(false)
			return true
		}
		return false
	})

	fmt.Fprintf(os.Stderr, "Render debug → %s\n", logPath)
	fmt.Fprintf(os.Stderr, "Run 'go run ./cmd/render-debug' in another terminal for live charts.\n")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
	case <-sigCh:
	}

	stopContinuous()
	signal.Stop(sigCh)
	tui.Stop()
	fmt.Println("Done.")
	return nil
}

// ── stress log component ───────────────────────────────────────────────────

type stressLog struct {
	pitui.Compo
	mu       sync.Mutex
	entries  []stressEntry
	verbose  bool
	colorize bool
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
	s := &stressLog{entries: entries}
	s.Update()
	return s
}

func (s *stressLog) Render(ctx pitui.RenderContext) pitui.RenderResult {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	return pitui.RenderResult{Lines: lines}
}

func randomStack() string {
	frames := []string{
		"main.run", "pitui.doRender", "pitui.compositeOverlays",
		"pitui.CompositeLineAt", "ansi.Truncate", "dang.Eval",
		"runtime.goexit", "net/http.serve", "pitui.handleInput",
	}
	n := 2 + rand.Intn(3)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = frames[rand.Intn(len(frames))]
	}
	return strings.Join(parts, " → ")
}

func appendLines(log *stressLog, n int) {
	levels := []string{"INFO", "DEBUG", "WARN"}
	log.mu.Lock()
	for range n {
		log.entries = append(log.entries, stressEntry{
			ts:      time.Now(),
			level:   levels[rand.Intn(len(levels))],
			message: fmt.Sprintf("[append] new line %d val=%d", len(log.entries), rand.Intn(99999)),
		})
	}
	log.mu.Unlock()
	log.Update()
}

// ── helper components ──────────────────────────────────────────────────────

type statusBarComponent struct {
	pitui.Compo
	mu   sync.Mutex
	line string
}

func (s *statusBarComponent) set(line string) {
	s.mu.Lock()
	s.line = line
	s.mu.Unlock()
	s.Update()
}
func (s *statusBarComponent) Render(ctx pitui.RenderContext) pitui.RenderResult {
	s.mu.Lock()
	line := s.line
	s.mu.Unlock()
	if pitui.VisibleWidth(line) > ctx.Width {
		line = pitui.Truncate(line, ctx.Width, "")
	}
	return pitui.RenderResult{Lines: []string{line}}
}

type staticLines struct {
	pitui.Compo
	lines []string
}

func (s *staticLines) Render(ctx pitui.RenderContext) pitui.RenderResult {
	return pitui.RenderResult{Lines: s.lines}
}
