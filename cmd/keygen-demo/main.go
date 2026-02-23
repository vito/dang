// Command keygen-demo is a keygen-style ASCII art stress test for pitui.
// It renders an animated Mandelbrot zoom with a retro status chrome,
// pushing a full-screen repaint every frame to exercise the render pipeline.
//
// Usage:
//
//	go run ./cmd/keygen-demo
//	go run ./cmd/keygen-demo -fps 30
package main

import (
	"flag"
	"fmt"
	"math"
	"math/cmplx"
	"os"
	"os/signal"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/dang/pkg/pitui"
)

func main() {
	duration := flag.Duration("duration", 0, "exit after this duration (e.g. 10s, 1m)")
	cpuProfile := flag.String("cpuprofile", "", "write CPU profile to file")
	heapProfile := flag.String("heapprofile", "", "write heap profile to file on exit")
	bench := flag.Bool("bench", false, "render as fast as possible and report FPS")
	flag.Parse()

	if err := run(*duration, *cpuProfile, *heapProfile, *bench); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(duration time.Duration, cpuProfile, heapProfile string, bench bool) error {
	if cpuProfile != "" {
		f, err := os.Create(cpuProfile)
		if err != nil {
			return fmt.Errorf("create CPU profile: %w", err)
		}
		defer f.Close() //nolint:errcheck // best-effort close of profiling file
		if err := pprof.StartCPUProfile(f); err != nil {
			return fmt.Errorf("start CPU profile: %w", err)
		}
		defer pprof.StopCPUProfile()
	}
	term := pitui.NewProcessTerminal()
	tui := pitui.New(term)

	logPath := "/tmp/pitui-keygen-debug.log"
	debugFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open debug log: %w", err)
	}
	defer debugFile.Close() //nolint:errcheck // best-effort close of debug log
	tui.SetDebugWriter(debugFile)

	fractal := newFractalView()
	fractal.bench = bench
	chrome := newChromeBar(fractal)
	fractal.chrome = chrome
	log := newFrameLog()
	fractal.log = log

	tui.Dispatch(func() {
		tui.AddChild(log)
		tui.AddChild(fractal)
		tui.AddChild(chrome)
	})

	if err := tui.Start(); err != nil {
		return fmt.Errorf("TUI start: %w", err)
	}

	quit := make(chan struct{})
	fractal.quit = quit

	tui.Dispatch(func() {
		tui.SetFocus(fractal)
	})

	fmt.Fprintf(os.Stderr, "Render debug → %s\n", logPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var timeout <-chan time.Time
	if duration > 0 {
		timeout = time.After(duration)
	}

	select {
	case <-quit:
	case <-sigCh:
	case <-timeout:
	}

	signal.Stop(sigCh)
	tui.Stop()

	if bench {
		elapsed := time.Since(fractal.benchStart)
		frames := fractal.renderCount
		fps := float64(frames) / elapsed.Seconds()
		fmt.Fprintf(os.Stderr, "\nBenchmark: %d frames in %s (%.1f fps)\n", frames, elapsed.Truncate(time.Millisecond), fps)
	}

	if heapProfile != "" {
		f, err := os.Create(heapProfile)
		if err != nil {
			return fmt.Errorf("create heap profile: %w", err)
		}
		defer f.Close() //nolint:errcheck // best-effort close of profiling file
		if err := pprof.WriteHeapProfile(f); err != nil {
			return fmt.Errorf("write heap profile: %w", err)
		}
	}

	return nil
}

// ── Inline input widget ────────────────────────────────────────────────────

// inlineInput is a reusable inline numeric editor. It handles its own mouse
// interaction (hover highlighting, click-to-edit, click-outside-to-commit)
// and keyboard editing (cursor movement, insert/delete, commit/cancel).
//
// It is not a pitui Component — it renders as an inline styled string that
// the parent component incorporates into its output line. The parent must
// forward mouse and key events.
type inlineInput struct {
	value  *float64     // pointer to the backing value
	format string       // printf format for display (e.g. "%.12f")
	chrome *chromeBar   // parent — for Update() and focus management
	peer   *inlineInput // other input — for Tab switching

	hovered bool
	editing bool
	buf     []rune
	cursor  int

	// Layout: X range set during Render, used for hit testing.
	startX int
	endX   int
}

// HandleMouse handles hover, click-to-edit, and click-outside-to-commit.
// The caller passes the full event; the input does its own hit testing.
func (inp *inlineInput) HandleMouse(ctx pitui.EventContext, ev pitui.MouseEvent) bool {
	hit := ev.Col >= inp.startX && ev.Col < inp.endX

	switch ev.MouseEvent.(type) {
	case uv.MouseMotionEvent:
		if hit != inp.hovered {
			inp.hovered = hit
			inp.chrome.Update()
		}
		return false
	case uv.MouseClickEvent:
		if ev.Mouse().Button != uv.MouseLeft {
			return false
		}
		if hit {
			if !inp.editing {
				inp.startEdit(ctx)
			}
			return true
		}
		// Click elsewhere — commit if we were editing.
		if inp.editing {
			inp.commit(ctx)
		}
		return false
	}
	return false
}

// HandleKey handles all keyboard input during editing.
func (inp *inlineInput) HandleKey(ctx pitui.EventContext, ev uv.KeyPressEvent) bool {
	if !inp.editing {
		return false
	}
	key := uv.Key(ev)
	switch {
	case key.Code == uv.KeyEnter:
		inp.commit(ctx)
	case key.Code == uv.KeyEscape:
		inp.cancel(ctx)
	case key.Code == uv.KeyBackspace:
		if inp.cursor > 0 {
			inp.buf = append(inp.buf[:inp.cursor-1], inp.buf[inp.cursor:]...)
			inp.cursor--
			inp.chrome.Update()
		}
	case key.Code == uv.KeyDelete:
		if inp.cursor < len(inp.buf) {
			inp.buf = append(inp.buf[:inp.cursor], inp.buf[inp.cursor+1:]...)
			inp.chrome.Update()
		}
	case key.Code == uv.KeyLeft && key.Mod == 0:
		if inp.cursor > 0 {
			inp.cursor--
			inp.chrome.Update()
		}
	case key.Code == uv.KeyRight && key.Mod == 0:
		if inp.cursor < len(inp.buf) {
			inp.cursor++
			inp.chrome.Update()
		}
	case key.Code == 'a' && key.Mod == uv.ModCtrl, key.Code == uv.KeyHome:
		inp.cursor = 0
		inp.chrome.Update()
	case key.Code == 'e' && key.Mod == uv.ModCtrl, key.Code == uv.KeyEnd:
		inp.cursor = len(inp.buf)
		inp.chrome.Update()
	case key.Code == 'u' && key.Mod == uv.ModCtrl:
		inp.buf = inp.buf[inp.cursor:]
		inp.cursor = 0
		inp.chrome.Update()
	case key.Code == 'k' && key.Mod == uv.ModCtrl:
		inp.buf = inp.buf[:inp.cursor]
		inp.chrome.Update()
	case key.Code == uv.KeyTab:
		inp.commit(ctx)
		inp.peer.startEdit(ctx)
	default:
		if key.Text != "" {
			for _, r := range key.Text {
				if isCoordRune(r) {
					newBuf := make([]rune, 0, len(inp.buf)+1)
					newBuf = append(newBuf, inp.buf[:inp.cursor]...)
					newBuf = append(newBuf, r)
					newBuf = append(newBuf, inp.buf[inp.cursor:]...)
					inp.buf = newBuf
					inp.cursor++
				}
			}
			inp.chrome.Update()
		}
	}
	return true // consume all keys while editing
}

func (inp *inlineInput) startEdit(ctx pitui.EventContext) {
	inp.buf = []rune(fmt.Sprintf(inp.format, *inp.value))
	inp.editing = true
	inp.cursor = len(inp.buf)
	inp.hovered = false
	ctx.SetFocus(inp.chrome) // chrome bar receives key events
	inp.chrome.Update()
}

func (inp *inlineInput) commit(ctx pitui.EventContext) {
	if !inp.editing {
		return
	}
	val, err := strconv.ParseFloat(strings.TrimSpace(string(inp.buf)), 64)
	if err == nil {
		*inp.value = val
		inp.chrome.fractal.Update()
	}
	inp.editing = false
	inp.buf = nil
	ctx.SetFocus(inp.chrome.fractal) // restore focus to fractal
	inp.chrome.Update()
}

func (inp *inlineInput) cancel(ctx pitui.EventContext) {
	inp.editing = false
	inp.buf = nil
	ctx.SetFocus(inp.chrome.fractal)
	inp.chrome.Update()
}

// Render returns the styled inline string and updates startX/endX for hit
// testing. The caller provides the X offset where the value starts and the
// minimum display width (to avoid jitter when the edit buffer is shorter).
func (inp *inlineInput) Render(startX, minWidth int) string {
	inp.startX = startX
	var rendered string
	if inp.editing {
		rendered = inp.renderEdit(minWidth)
	} else {
		s := fmt.Sprintf(inp.format, *inp.value)
		if inp.hovered {
			rendered = coordHoverStyle.Render(s)
		} else {
			rendered = topValueStyle.Render(s)
		}
	}
	inp.endX = startX + lipgloss.Width(rendered)
	return rendered
}

func (inp *inlineInput) renderEdit(minWidth int) string {
	runes := inp.buf
	width := max(len(runes)+1, minWidth) // +1 ensures room for end-of-line cursor

	display := make([]rune, width)
	copy(display, runes)
	for i := len(runes); i < width; i++ {
		display[i] = ' '
	}

	cur := min(inp.cursor, len(display)-1)
	before := string(display[:cur])
	cursorCh := string(display[cur : cur+1])
	after := string(display[cur+1:])

	return coordEditStyle.Render(before) +
		coordEditCursorStyle.Render(cursorCh) +
		coordEditStyle.Render(after)
}

// isCoordRune returns true for characters valid in a floating-point literal.
func isCoordRune(r rune) bool {
	return (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '+' || r == 'e' || r == 'E'
}

// ── Fractal view ───────────────────────────────────────────────────────────

// ASCII ramp from dark to bright.
const ramp = " .:-=+*#%@"

// Color palette — 256-color ANSI. We cycle through these based on iteration.
var palette = []string{
	"\x1b[38;5;17m", "\x1b[38;5;18m", "\x1b[38;5;19m", "\x1b[38;5;20m",
	"\x1b[38;5;21m", "\x1b[38;5;27m", "\x1b[38;5;33m", "\x1b[38;5;39m",
	"\x1b[38;5;45m", "\x1b[38;5;51m", "\x1b[38;5;50m", "\x1b[38;5;49m",
	"\x1b[38;5;48m", "\x1b[38;5;47m", "\x1b[38;5;46m", "\x1b[38;5;82m",
	"\x1b[38;5;118m", "\x1b[38;5;154m", "\x1b[38;5;190m", "\x1b[38;5;226m",
	"\x1b[38;5;220m", "\x1b[38;5;214m", "\x1b[38;5;208m", "\x1b[38;5;202m",
	"\x1b[38;5;196m", "\x1b[38;5;197m", "\x1b[38;5;198m", "\x1b[38;5;199m",
	"\x1b[38;5;200m", "\x1b[38;5;201m", "\x1b[38;5;165m", "\x1b[38;5;129m",
}

const resetColor = "\x1b[0m"

type activeNotification struct {
	handle *pitui.OverlayHandle
	width  int
	height int
}

type fractalView struct {
	pitui.Compo
	frame         int
	paused        bool
	targetRe      float64
	targetIm      float64
	quit          chan struct{}
	chrome        *chromeBar
	log           *frameLog
	bench         bool
	benchStart    time.Time
	renderCount   int
	notifications []*activeNotification
}

func newFractalView() *fractalView {
	f := &fractalView{
		targetRe: -0.7435,
		targetIm: 0.1314,
	}
	f.Update()
	return f
}

func (f *fractalView) notify(ctx pitui.EventContext, msg string) {
	bubble := &notificationBubble{msg: msg}
	rendered := bubbleStyle.Render(msg)
	bubbleW := lipgloss.Width(rendered)
	bubbleH := lipgloss.Height(rendered)

	y := 1
	for _, n := range f.notifications {
		y += n.height
	}

	handle := ctx.ShowOverlay(bubble, &pitui.OverlayOptions{
		Width:   pitui.SizeAbs(bubbleW),
		Anchor:  pitui.AnchorTopRight,
		OffsetX: -1,
		OffsetY: y,
	})

	n := &activeNotification{handle: handle, width: bubbleW, height: bubbleH}
	f.notifications = append(f.notifications, n)

	time.AfterFunc(2*time.Second, func() {
		ctx.Dispatch(func() {
			f.removeNotification(n)
		})
	})
}

func (f *fractalView) removeNotification(n *activeNotification) {
	n.handle.Hide()
	for i, existing := range f.notifications {
		if existing == n {
			f.notifications = append(f.notifications[:i], f.notifications[i+1:]...)
			break
		}
	}
	// Restack remaining notifications.
	y := 1
	for _, n := range f.notifications {
		n.handle.SetOptions(&pitui.OverlayOptions{
			Width:   pitui.SizeAbs(n.width),
			Anchor:  pitui.AnchorTopRight,
			OffsetX: -1,
			OffsetY: y,
		})
		y += n.height
	}
}

func (f *fractalView) HandleKeyPress(ctx pitui.EventContext, ev uv.KeyPressEvent) bool {
	key := uv.Key(ev)
	switch {
	case key.Text == "q" || (key.Code == 'c' && key.Mod == uv.ModCtrl):
		select {
		case <-f.quit:
		default:
			close(f.quit)
		}
		return true
	case key.Text == " ":
		f.paused = !f.paused
		if f.paused {
			f.notify(ctx, "⏸ paused")
		} else {
			f.notify(ctx, "▶ resumed")
		}
		return true
	case key.Text == "r":
		f.frame = 0
		f.targetRe = -0.7435
		f.targetIm = 0.1314
		f.Update()
		f.notify(ctx, "↺ reset")
		return true
	case key.Code == uv.KeyUp:
		f.targetIm -= f.scale() * 0.1
		f.Update()
		return true
	case key.Code == uv.KeyDown:
		f.targetIm += f.scale() * 0.1
		f.Update()
		return true
	case key.Code == uv.KeyLeft:
		f.targetRe -= f.scale() * 0.1
		f.Update()
		return true
	case key.Code == uv.KeyRight:
		f.targetRe += f.scale() * 0.1
		f.Update()
		return true
	case key.Text == "+" || key.Text == "=":
		f.frame += 50
		f.Update()
		return true
	case key.Text == "-":
		f.frame = max(0, f.frame-50)
		f.Update()
		return true
	}
	return false
}

func (f *fractalView) scale() float64 {
	return 3.0 * math.Exp(-0.003*float64(f.frame))
}

func (f *fractalView) OnMount(ctx pitui.EventContext) {
	f.benchStart = time.Now()

	// In bench mode, Render() self-advances — no goroutine needed.
	if !f.bench {
		ticker := time.NewTicker(6 * time.Millisecond) // ~165 fps
		go func() {
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					ctx.Dispatch(func() {
						if !f.paused {
							f.frame++
						}
						f.Update()
						f.chrome.Update()
						if f.log != nil {
							f.log.appendFrame(f.frame, f.targetRe, f.targetIm, f.scale())
						}
					})
				case <-ctx.Done():
					return
				}
			}
		}()
	}
}

func (f *fractalView) Render(ctx pitui.RenderContext) pitui.RenderResult {
	f.renderCount++

	// In bench mode each render advances the frame and immediately
	// marks dirty again, so the render loop runs flat-out with no
	// goroutine or ticker involved.
	if f.bench && !f.paused {
		f.frame++
		f.chrome.Update()
		if f.log != nil {
			f.log.appendFrame(f.frame, f.targetRe, f.targetIm, f.scale())
		}
		f.Update() // schedule next render
	}

	frame := f.frame

	w := ctx.Width
	// Reserve 2 lines for chrome.
	h := max(ctx.ScreenHeight-2, 1)

	// Exponential zoom: starts wide, zooms deeper each frame.
	// Rates are tuned for ~165fps so the zoom feels the same as 30fps.
	baseScale := 3.0
	zoomRate := 0.003
	scale := baseScale * math.Exp(-zoomRate*float64(frame))

	// Aspect ratio correction: terminal chars are ~2:1 tall.
	aspect := 2.0

	maxIter := min(64+frame/10, 256)

	// Reuse line buffer from framework's double-buffer.
	lines := ctx.Recycle
	if cap(lines) < h {
		lines = make([]string, h)
	} else {
		lines = lines[:h]
	}

	np := len(palette)
	nr := len(ramp)

	var buf strings.Builder
	for y := range h {
		buf.Reset()
		im := f.targetIm + (float64(y)/float64(h)-0.5)*scale
		for x := range w {
			re := f.targetRe + (float64(x)/float64(w)-0.5)*scale*float64(w)/float64(h)/aspect
			c := complex(re, im)
			iter := mandelbrot(c, maxIter)
			if iter == maxIter {
				// Inside the set — cycle background color with frame.
				buf.WriteString(palette[(frame/8)%np])
				buf.WriteByte(' ')
			} else {
				// Outside — shift color by frame so everything shimmers.
				ci := (iter + frame/4) % np
				ri := iter % nr
				buf.WriteString(palette[ci])
				buf.WriteByte(ramp[ri])
			}
		}
		buf.WriteString(resetColor)
		lines[y] = buf.String()
	}

	return pitui.RenderResult{Lines: lines}
}

func mandelbrot(c complex128, maxIter int) int {
	z := complex(0, 0)
	for i := range maxIter {
		z = z*z + c
		if cmplx.Abs(z) > 2 {
			return i
		}
	}
	return maxIter
}

// ── Chrome bar ─────────────────────────────────────────────────────────────

type chromeBar struct {
	pitui.Compo
	start   time.Time
	fractal *fractalView
	reInput *inlineInput
	imInput *inlineInput
}

func newChromeBar(fractal *fractalView) *chromeBar {
	c := &chromeBar{fractal: fractal}
	c.reInput = &inlineInput{value: &fractal.targetRe, format: "%.12f", chrome: c}
	c.imInput = &inlineInput{value: &fractal.targetIm, format: "%+.12f", chrome: c}
	c.reInput.peer = c.imInput
	c.imInput.peer = c.reInput
	return c
}

func (c *chromeBar) OnMount(ctx pitui.EventContext) {
	c.start = time.Now()
}

// HandleMouse implements pitui.MouseEnabled — delegates entirely to inputs.
func (c *chromeBar) HandleMouse(ctx pitui.EventContext, ev pitui.MouseEvent) bool {
	if ev.Row != 0 {
		return false
	}
	a := c.reInput.HandleMouse(ctx, ev)
	b := c.imInput.HandleMouse(ctx, ev)
	return a || b
}

// SetHovered implements pitui.Hoverable — clears hover on both inputs
// when the mouse leaves the chrome bar region.
func (c *chromeBar) SetHovered(_ pitui.EventContext, hovered bool) {
	if !hovered {
		changed := false
		if c.reInput.hovered {
			c.reInput.hovered = false
			changed = true
		}
		if c.imInput.hovered {
			c.imInput.hovered = false
			changed = true
		}
		if changed {
			c.Update()
		}
	}
}

// HandleKeyPress implements pitui.Interactive — delegates to the active input.
func (c *chromeBar) HandleKeyPress(ctx pitui.EventContext, ev uv.KeyPressEvent) bool {
	if c.reInput.HandleKey(ctx, ev) {
		return true
	}
	return c.imInput.HandleKey(ctx, ev)
}

var (
	// Top bar styles
	topBarBg      = lipgloss.Color("235")
	topBarStyle   = lipgloss.NewStyle().Background(topBarBg).Foreground(lipgloss.Color("252"))
	topTitleStyle = lipgloss.NewStyle().Background(lipgloss.Color("63")).Foreground(lipgloss.Color("255")).Bold(true)
	topLabelStyle = topBarStyle.Foreground(lipgloss.Color("243"))
	topValueStyle = topBarStyle.Foreground(lipgloss.Color("81"))
	topDimStyle   = topBarStyle.Foreground(lipgloss.Color("243"))
	topTimerStyle = lipgloss.NewStyle().Background(lipgloss.Color("238")).Foreground(lipgloss.Color("250"))

	// Bottom bar styles
	botBarBg    = lipgloss.Color("236")
	botBarStyle = lipgloss.NewStyle().Background(botBarBg).Foreground(lipgloss.Color("245"))
	botKeyStyle = lipgloss.NewStyle().Background(botBarBg).Foreground(lipgloss.Color("81")).Bold(true)
	botSepStyle = lipgloss.NewStyle().Background(botBarBg).Foreground(lipgloss.Color("240"))

	// Coordinate hover / inline-edit styles
	coordHoverStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("123")).Background(lipgloss.Color("238")).Underline(true)
	coordEditStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("24"))
	coordEditCursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("232")).Background(lipgloss.Color("81"))
)

func (c *chromeBar) isEditing() bool {
	return c.reInput.editing || c.imInput.editing
}

func (c *chromeBar) Render(ctx pitui.RenderContext) pitui.RenderResult {
	f := c.fractal
	elapsed := time.Since(c.start).Truncate(time.Second)
	w := ctx.Width

	// ── Top bar ──────────────────────────────────────────────────────────

	title := topTitleStyle.Render(" ◆ MANDELBROT ")
	titleW := lipgloss.Width(title)

	reLabel := topLabelStyle.Render(" re ")
	reLabelW := lipgloss.Width(reLabel)
	reValueW := len(fmt.Sprintf("%.12f", f.targetRe))
	reRendered := c.reInput.Render(titleW+reLabelW, reValueW)

	imLabel := topLabelStyle.Render("  im ")
	imLabelW := lipgloss.Width(imLabel)
	imValueW := len(fmt.Sprintf("%+.12f", f.targetIm))
	imRendered := c.imInput.Render(c.reInput.endX+imLabelW, imValueW)

	coord := reLabel + reRendered + imLabel + imRendered
	zoom := topLabelStyle.Render("  zoom ") + topValueStyle.Render(fmt.Sprintf("%.2e", 3.0/f.scale()))
	iter := topLabelStyle.Render("  iter ") + topValueStyle.Render(fmt.Sprintf("%d", min(64+f.frame/10, 256)))
	state := ""
	if f.paused {
		state = topDimStyle.Render("  ") + topTitleStyle.Render(" ⏸ ")
	}
	timer := topTimerStyle.Render(fmt.Sprintf(" %s ", elapsed))

	topContent := title + coord + zoom + iter + state
	topPad := max(w-lipgloss.Width(topContent)-lipgloss.Width(timer), 0)
	top := topContent + topBarStyle.Render(strings.Repeat(" ", topPad)) + timer

	// ── Bottom bar ───────────────────────────────────────────────────────

	sep := botSepStyle.Render(" │ ")
	var bindings []string
	if c.isEditing() {
		bindings = []string{
			botKeyStyle.Render("Enter") + botBarStyle.Render(" apply"),
			botKeyStyle.Render("Esc") + botBarStyle.Render(" cancel"),
			botKeyStyle.Render("Tab") + botBarStyle.Render(" switch"),
		}
	} else {
		bindings = []string{
			botKeyStyle.Render("↑↓←→") + botBarStyle.Render(" pan"),
			botKeyStyle.Render("+/-") + botBarStyle.Render(" zoom"),
			botKeyStyle.Render("space") + botBarStyle.Render(" pause"),
			botKeyStyle.Render("click re/im") + botBarStyle.Render(" edit"),
			botKeyStyle.Render("r") + botBarStyle.Render(" reset"),
			botKeyStyle.Render("q") + botBarStyle.Render(" quit"),
		}
	}
	controls := " " + strings.Join(bindings, sep) + " "
	botPad := max(w-lipgloss.Width(controls), 0)
	left := botPad / 2
	right := botPad - left
	bot := botBarStyle.Render(strings.Repeat(" ", left)) + controls + botBarStyle.Render(strings.Repeat(" ", right))

	return pitui.RenderResult{Lines: []string{top, bot}}
}

// ── Frame log ──────────────────────────────────────────────────────────────

// frameLog is a scrollback component that appends one line per frame,
// exercising the TUI's ability to render scrollback and full-screen
// content simultaneously.
type frameLog struct {
	pitui.Compo
	lines []string
}

func newFrameLog() *frameLog {
	fl := &frameLog{}
	fl.Update()
	return fl
}

var (
	logFrameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	logCoordStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	logZoomStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("156"))
)

func (fl *frameLog) appendFrame(frame int, re, im, scale float64) {
	line := logFrameStyle.Render(fmt.Sprintf("frame %5d", frame)) + "  " +
		logCoordStyle.Render(fmt.Sprintf("re=%.10f im=%+.10f", re, im)) + "  " +
		logZoomStyle.Render(fmt.Sprintf("scale=%.4e", scale))
	fl.lines = append(fl.lines, line)
	fl.Update()
}

func (fl *frameLog) Render(ctx pitui.RenderContext) pitui.RenderResult {
	return pitui.RenderResult{Lines: fl.lines}
}

// ── Notification bubble ────────────────────────────────────────────────────

type notificationBubble struct {
	pitui.Compo
	msg string
}

var bubbleStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("232")).
	Background(lipgloss.Color("229")).
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("229")).
	Padding(0, 1)

func (n *notificationBubble) Render(ctx pitui.RenderContext) pitui.RenderResult {
	rendered := bubbleStyle.Render(n.msg)
	return pitui.RenderResult{Lines: strings.Split(rendered, "\n")}
}
