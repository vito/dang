// Package pitui implements a differential terminal renderer that uses the
// normal scrollback buffer (no alternate screen). It can surgically update
// any line via cursor movement, and falls back to a full clear+repaint when
// off-screen content changes. Synchronized output prevents flickering.
//
// This is a Go port of the pi TUI renderer.
package pitui

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/unix"
)

// Terminal abstracts terminal I/O so the renderer can be tested with a
// fake terminal.
type Terminal interface {
	// Start puts the terminal into raw mode and begins listening for input
	// and resize events. onInput receives raw bytes from stdin. onResize is
	// called when the terminal dimensions change.
	Start(onInput func([]byte), onResize func()) error

	// Stop restores the terminal to its original state.
	Stop()

	// Write sends raw bytes to the terminal.
	Write(p []byte)

	// WriteString sends a string to the terminal.
	WriteString(s string)

	// Columns returns the current terminal width.
	Columns() int

	// Rows returns the current terminal height.
	Rows() int

	// HideCursor hides the hardware cursor.
	HideCursor()

	// ShowCursor shows the hardware cursor.
	ShowCursor()
}

// ProcessTerminal is a Terminal backed by os.Stdin / os.Stdout.
// Terminal dimensions are cached and refreshed on SIGWINCH to avoid
// repeated ioctl syscalls during rendering.
type ProcessTerminal struct {
	origTermios *unix.Termios
	onInput     func([]byte)
	onResize    func()
	sigCh       chan os.Signal
	stopCancel  context.CancelFunc
	stopCtx     context.Context

	sizeMu sync.RWMutex
	cols   int
	rows   int
}

func NewProcessTerminal() *ProcessTerminal {
	return &ProcessTerminal{}
}

func (t *ProcessTerminal) Start(onInput func([]byte), onResize func()) error {
	t.onInput = onInput
	t.onResize = onResize
	t.stopCtx, t.stopCancel = context.WithCancel(context.Background())

	// Save and set raw mode.
	fd := int(os.Stdin.Fd())
	orig, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return fmt.Errorf("get termios: %w", err)
	}
	t.origTermios = orig

	raw := *orig
	raw.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Cflag |= unix.CS8
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &raw); err != nil {
		return fmt.Errorf("set raw: %w", err)
	}

	// Cache initial terminal size.
	t.refreshSize()

	// Enable bracketed paste.
	t.WriteString("\x1b[?2004h")

	// Enable Kitty keyboard protocol (disambiguate escape codes).
	// This allows detecting Shift+Enter and other modified keys.
	// Uses the same approach as BubbleTea v2: set mode with flag 1
	// (disambiguate) and mode 1 (set given flags, unset others).
	t.WriteString(ansi.KittyKeyboard(ansi.KittyDisambiguateEscapeCodes, 1))
	// Query the terminal for its keyboard enhancement support.
	// The response arrives as input and is decoded by ultraviolet.
	t.WriteString(ansi.RequestKittyKeyboard)

	// Read stdin in a goroutine.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				// Copy so the callback can keep the slice.
				data := make([]byte, n)
				copy(data, buf[:n])
				t.onInput(data)
			}
			if err != nil {
				return
			}
		}
	}()

	// Listen for SIGWINCH.
	t.sigCh = make(chan os.Signal, 1)
	signal.Notify(t.sigCh, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-t.sigCh:
				t.refreshSize()
				if t.onResize != nil {
					t.onResize()
				}
			case <-t.stopCtx.Done():
				return
			}
		}
	}()

	return nil
}

func (t *ProcessTerminal) Stop() {
	// Disable Kitty keyboard protocol.
	t.WriteString(ansi.KittyKeyboard(0, 1))

	// Disable bracketed paste.
	t.WriteString("\x1b[?2004l")

	if t.stopCancel != nil {
		t.stopCancel()
	}
	if t.sigCh != nil {
		signal.Stop(t.sigCh)
	}
	if t.origTermios != nil {
		fd := int(os.Stdin.Fd())
		_ = unix.IoctlSetTermios(fd, ioctlWriteTermios, t.origTermios)
	}
}

func (t *ProcessTerminal) Write(p []byte) {
	_, _ = os.Stdout.Write(p)
}

func (t *ProcessTerminal) WriteString(s string) {
	_, _ = os.Stdout.WriteString(s)
}

func (t *ProcessTerminal) Columns() int {
	t.sizeMu.RLock()
	c := t.cols
	t.sizeMu.RUnlock()
	if c == 0 {
		return 80
	}
	return c
}

func (t *ProcessTerminal) Rows() int {
	t.sizeMu.RLock()
	r := t.rows
	t.sizeMu.RUnlock()
	if r == 0 {
		return 24
	}
	return r
}

// refreshSize queries the kernel for current terminal dimensions and caches
// them. Called once at Start and on every SIGWINCH.
func (t *ProcessTerminal) refreshSize() {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return
	}
	t.sizeMu.Lock()
	if ws.Col > 0 {
		t.cols = int(ws.Col)
	}
	if ws.Row > 0 {
		t.rows = int(ws.Row)
	}
	t.sizeMu.Unlock()
}

func (t *ProcessTerminal) HideCursor() {
	t.WriteString("\x1b[?25l")
}

func (t *ProcessTerminal) ShowCursor() {
	t.WriteString("\x1b[?25h")
}
