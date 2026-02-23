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
	chrome := &chromeBar{fractal: fractal}
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

// ── Coordinate field identifiers ───────────────────────────────────────────

type coordField int

const (
	fieldNone coordField = iota
	fieldRe
	fieldIm
)

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

	// Mouse / inline-edit state
	hoverField coordField
	editField  coordField
	editBuf    []rune
	editCursor int

	// Layout cache from last Render (X ranges for hit testing on row 0)
	reStartX int
	reEndX   int
	imStartX int
	imEndX   int
}

func (c *chromeBar) OnMount(ctx pitui.EventContext) {
	c.start = time.Now()
}

// HandleMouse implements pitui.MouseEnabled. The framework dispatches mouse
// events here only when the cursor is over the chrome bar, with
// component-relative coordinates available via ctx.MouseRow()/MouseCol().
func (c *chromeBar) HandleMouse(ctx pitui.EventContext, ev uv.MouseEvent) bool {
	row := ctx.MouseRow()
	col := ctx.MouseCol()
	field := c.hitTest(col, row)

	switch ev.(type) {
	case uv.MouseMotionEvent:
		if field != c.hoverField {
			c.hoverField = field
			c.Update()
		}
		return false // don't consume motion — let others see it
	case uv.MouseClickEvent:
		m := ev.Mouse()
		if m.Button != uv.MouseLeft {
			return false
		}
		if field != fieldNone {
			if c.editField != fieldNone && c.editField != field {
				c.commitEdit(ctx)
			}
			if c.editField != field {
				c.startEdit(ctx, field)
			}
			return true
		}
		// Click outside coordinate values — commit any active edit.
		if c.editField != fieldNone {
			c.commitEdit(ctx)
			return true
		}
	}
	return false
}

// SetHovered implements pitui.Hoverable — clears hover highlighting when
// the mouse leaves the chrome bar region.
func (c *chromeBar) SetHovered(_ pitui.EventContext, hovered bool) {
	if !hovered && c.hoverField != fieldNone {
		c.hoverField = fieldNone
		c.Update()
	}
}

// HandleKeyPress implements pitui.Interactive — handles keyboard input
// when the chrome bar has focus (during inline coordinate editing).
func (c *chromeBar) HandleKeyPress(ctx pitui.EventContext, ev uv.KeyPressEvent) bool {
	if c.editField != fieldNone {
		return c.handleEditKey(ctx, ev)
	}
	return false
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

func (c *chromeBar) Render(ctx pitui.RenderContext) pitui.RenderResult {
	f := c.fractal
	elapsed := time.Since(c.start).Truncate(time.Second)
	w := ctx.Width

	// ── Top bar ──────────────────────────────────────────────────────────

	title := topTitleStyle.Render(" ◆ MANDELBROT ")
	titleW := lipgloss.Width(title)

	// Re coordinate — track X range for mouse hit testing.
	reLabel := topLabelStyle.Render(" re ")
	reLabelW := lipgloss.Width(reLabel)
	reValueStr := fmt.Sprintf("%.12f", f.targetRe)
	reValueW := len(reValueStr)
	c.reStartX = titleW + reLabelW

	var reRendered string
	switch {
	case c.editField == fieldRe:
		reRendered = c.renderEditField(reValueW)
	case c.hoverField == fieldRe:
		reRendered = coordHoverStyle.Render(reValueStr)
	default:
		reRendered = topValueStyle.Render(reValueStr)
	}
	c.reEndX = c.reStartX + lipgloss.Width(reRendered)

	// Im coordinate — same treatment.
	imLabel := topLabelStyle.Render("  im ")
	imLabelW := lipgloss.Width(imLabel)
	imValueStr := fmt.Sprintf("%+.12f", f.targetIm)
	imValueW := len(imValueStr)
	c.imStartX = c.reEndX + imLabelW

	var imRendered string
	switch {
	case c.editField == fieldIm:
		imRendered = c.renderEditField(imValueW)
	case c.hoverField == fieldIm:
		imRendered = coordHoverStyle.Render(imValueStr)
	default:
		imRendered = topValueStyle.Render(imValueStr)
	}
	c.imEndX = c.imStartX + lipgloss.Width(imRendered)

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
	if c.editField != fieldNone {
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

// ── Chrome bar: mouse & inline-edit ────────────────────────────────────────

// hitTest maps a component-relative (col, row) to a coordinate field.
// Row 0 is the top bar (where coordinates live); row 1 is the bottom bar.
func (c *chromeBar) hitTest(col, row int) coordField {
	if row != 0 {
		return fieldNone
	}
	if col >= c.reStartX && col < c.reEndX {
		return fieldRe
	}
	if col >= c.imStartX && col < c.imEndX {
		return fieldIm
	}
	return fieldNone
}

func (c *chromeBar) startEdit(ctx pitui.EventContext, field coordField) {
	f := c.fractal
	switch field {
	case fieldRe:
		c.editBuf = []rune(fmt.Sprintf("%.12f", f.targetRe))
	case fieldIm:
		c.editBuf = []rune(fmt.Sprintf("%+.12f", f.targetIm))
	}
	c.editField = field
	c.editCursor = len(c.editBuf)
	c.hoverField = fieldNone
	ctx.SetFocus(c) // take keyboard focus for editing
	c.Update()
}

func (c *chromeBar) commitEdit(ctx pitui.EventContext) {
	if c.editField == fieldNone {
		return
	}
	val, err := strconv.ParseFloat(strings.TrimSpace(string(c.editBuf)), 64)
	if err == nil {
		switch c.editField {
		case fieldRe:
			c.fractal.targetRe = val
		case fieldIm:
			c.fractal.targetIm = val
		}
		c.fractal.Update()
	}
	c.editField = fieldNone
	c.editBuf = nil
	ctx.SetFocus(c.fractal) // restore focus to fractal
	c.Update()
}

func (c *chromeBar) cancelEdit(ctx pitui.EventContext) {
	c.editField = fieldNone
	c.editBuf = nil
	ctx.SetFocus(c.fractal)
	c.Update()
}

func (c *chromeBar) handleEditKey(ctx pitui.EventContext, ev uv.KeyPressEvent) bool {
	key := uv.Key(ev)
	switch {
	case key.Code == uv.KeyEnter:
		c.commitEdit(ctx)
	case key.Code == uv.KeyEscape:
		c.cancelEdit(ctx)
	case key.Code == uv.KeyBackspace:
		if c.editCursor > 0 {
			c.editBuf = append(c.editBuf[:c.editCursor-1], c.editBuf[c.editCursor:]...)
			c.editCursor--
			c.Update()
		}
	case key.Code == uv.KeyDelete:
		if c.editCursor < len(c.editBuf) {
			c.editBuf = append(c.editBuf[:c.editCursor], c.editBuf[c.editCursor+1:]...)
			c.Update()
		}
	case key.Code == uv.KeyLeft && key.Mod == 0:
		if c.editCursor > 0 {
			c.editCursor--
			c.Update()
		}
	case key.Code == uv.KeyRight && key.Mod == 0:
		if c.editCursor < len(c.editBuf) {
			c.editCursor++
			c.Update()
		}
	case key.Code == 'a' && key.Mod == uv.ModCtrl, key.Code == uv.KeyHome:
		c.editCursor = 0
		c.Update()
	case key.Code == 'e' && key.Mod == uv.ModCtrl, key.Code == uv.KeyEnd:
		c.editCursor = len(c.editBuf)
		c.Update()
	case key.Code == 'u' && key.Mod == uv.ModCtrl:
		c.editBuf = c.editBuf[c.editCursor:]
		c.editCursor = 0
		c.Update()
	case key.Code == 'k' && key.Mod == uv.ModCtrl:
		c.editBuf = c.editBuf[:c.editCursor]
		c.Update()
	case key.Code == uv.KeyTab:
		// Tab switches between re ↔ im.
		next := fieldRe
		if c.editField == fieldRe {
			next = fieldIm
		}
		c.commitEdit(ctx)
		c.startEdit(ctx, next)
	default:
		if key.Text != "" {
			for _, r := range key.Text {
				if isCoordRune(r) {
					newBuf := make([]rune, 0, len(c.editBuf)+1)
					newBuf = append(newBuf, c.editBuf[:c.editCursor]...)
					newBuf = append(newBuf, r)
					newBuf = append(newBuf, c.editBuf[c.editCursor:]...)
					c.editBuf = newBuf
					c.editCursor++
				}
			}
			c.Update()
		}
	}
	return true // consume all keys while editing
}

// isCoordRune returns true for characters valid in a floating-point literal.
func isCoordRune(r rune) bool {
	return (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '+' || r == 'e' || r == 'E'
}

// renderEditField renders the inline edit buffer as a styled string with a
// block cursor. The field is padded to at least minWidth visible characters
// so the chrome bar doesn't jitter.
func (c *chromeBar) renderEditField(minWidth int) string {
	runes := c.editBuf
	width := max(len(runes)+1, minWidth) // +1 ensures room for end-of-line cursor

	display := make([]rune, width)
	copy(display, runes)
	for i := len(runes); i < width; i++ {
		display[i] = ' '
	}

	cur := min(c.editCursor, len(display)-1)
	before := string(display[:cur])
	cursorCh := string(display[cur : cur+1])
	after := string(display[cur+1:])

	return coordEditStyle.Render(before) +
		coordEditCursorStyle.Render(cursorCh) +
		coordEditStyle.Render(after)
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
