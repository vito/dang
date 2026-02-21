// Package teav2 adapts bubbletea v2 models for use as pitui components.
package teav2

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/dang/pkg/pitui"
)

// Model is the interface for bubbletea v2 models that can be wrapped
// as pitui components. It matches the common pattern used by bubbles
// (list, table, viewport, etc.) where Update returns the concrete
// type and View returns a string.
type Model[T any] interface {
	Update(tea.Msg) (T, tea.Cmd)
	View() string
}

// Wrap wraps a bubbletea v2 model as a pitui Component. It bridges
// the two frameworks:
//
//   - Render calls the model's View() and splits into lines
//   - HandleKeyPress forwards decoded key events as tea.KeyPressMsg
//   - Width changes are delivered as tea.WindowSizeMsg
//   - Commands returned by Update are executed asynchronously and
//     their resulting messages are fed back through Update
//
// Usage:
//
//	items := []list.Item{...}
//	delegate := list.NewDefaultDelegate()
//	m := list.New(items, delegate, 80, 20)
//	comp := teav2.Wrap(m)
//	tui.AddChild(comp)
type Wrap[T Model[T]] struct {
	pitui.Compo
	model    T
	width    int
	height   int
	onQuit   func()
	dispatch func(func()) // set on mount; schedules work on the UI goroutine
}

// New wraps a bubbletea v2 model as a pitui Component.
func New[T Model[T]](model T) *Wrap[T] {
	b := &Wrap[T]{model: model}
	b.Update()
	return b
}

// OnQuit sets a callback invoked when the bubbletea model returns a
// tea.QuitMsg. This lets the host application handle quit requests
// (e.g. close an overlay).
func (b *Wrap[T]) OnQuit(fn func()) {
	b.onQuit = fn
}

// OnMount captures the dispatch function for scheduling command results
// back on the UI goroutine.
func (b *Wrap[T]) OnMount(ctx pitui.EventContext) {
	b.dispatch = ctx.Dispatch
}

// Model returns the underlying bubbletea model.
func (b *Wrap[T]) Model() T {
	return b.model
}

// SendMsg sends a message to the bubbletea model's Update function,
// as if it came from a command. Useful for programmatic control.
// Must be called from the UI goroutine.
func (b *Wrap[T]) SendMsg(msg tea.Msg) {
	b.updateModel(msg)
}

func (b *Wrap[T]) updateModel(msg tea.Msg) {
	var cmd tea.Cmd
	b.model, cmd = b.model.Update(msg)
	b.Update()
	if cmd != nil {
		b.execCmd(cmd)
	}
}

func (b *Wrap[T]) execCmd(cmd tea.Cmd) {
	go func() {
		msg := cmd()
		if msg == nil {
			return
		}
		if _, ok := msg.(tea.QuitMsg); ok {
			if b.dispatch != nil {
				b.dispatch(func() {
					if b.onQuit != nil {
						b.onQuit()
					}
				})
			}
			return
		}
		if b.dispatch != nil {
			b.dispatch(func() {
				b.updateModel(msg)
			})
		}
	}()
}

// Render implements pitui.Component.
func (b *Wrap[T]) Render(ctx pitui.RenderContext) pitui.RenderResult {
	if ctx.Width != b.width || ctx.Height != b.height {
		b.width = ctx.Width
		b.height = ctx.Height
		var cmd tea.Cmd
		b.model, cmd = b.model.Update(tea.WindowSizeMsg{
			Width:  ctx.Width,
			Height: ctx.Height,
		})
		if cmd != nil {
			b.execCmd(cmd)
		}
	}

	view := b.model.View()
	lines := strings.Split(view, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return pitui.RenderResult{Lines: lines}
}

// HandleKeyPress implements pitui.Interactive.
func (b *Wrap[T]) HandleKeyPress(_ pitui.EventContext, ev uv.KeyPressEvent) bool {
	b.updateModel(tea.KeyPressMsg(ev))
	return true // bubbletea models consume all key events
}
