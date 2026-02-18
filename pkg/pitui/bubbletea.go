package pitui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// BubbleTeaModel is the interface for bubbletea models that can be
// wrapped as pitui components. It matches the common pattern used by
// bubbles (list, table, viewport, etc.) where Update returns the
// concrete type and View returns a string.
type BubbleTeaModel[T any] interface {
	Update(tea.Msg) (T, tea.Cmd)
	View() string
}

// BubbleTea wraps a bubbletea model as a pitui Component. It bridges
// the two frameworks:
//
//   - Render calls the model's View() and splits into lines
//   - HandleInput parses raw terminal bytes into tea.KeyPressMsg and
//     feeds them through Update
//   - Width changes are delivered as tea.WindowSizeMsg
//   - Commands returned by Update are executed asynchronously and
//     their resulting messages are fed back through Update
//
// Usage:
//
//	items := []list.Item{...}
//	delegate := list.NewDefaultDelegate()
//	m := list.New(items, delegate, 80, 20)
//	comp := pitui.NewBubbleTea(m)
//	tui.AddChild(comp)
type BubbleTea[T BubbleTeaModel[T]] struct {
	Compo
	model   T
	decoder uv.EventDecoder
	width   int // last width seen, for WindowSizeMsg
	height  int // last height seen
	onQuit  func()
}

// NewBubbleTea wraps a bubbletea model as a pitui Component.
func NewBubbleTea[T BubbleTeaModel[T]](model T) *BubbleTea[T] {
	b := &BubbleTea[T]{model: model}
	b.Update()
	return b
}

// OnQuit sets a callback invoked when the bubbletea model returns a
// tea.QuitMsg. This lets the host application handle quit requests
// (e.g. close an overlay).
func (b *BubbleTea[T]) OnQuit(fn func()) {
	b.onQuit = fn
}

// Model returns the underlying bubbletea model.
func (b *BubbleTea[T]) Model() T {
	return b.model
}

// SendMsg sends a message to the bubbletea model's Update function,
// as if it came from a command. Useful for programmatic control
// (e.g. sending an initial tea.WindowSizeMsg or custom messages).
func (b *BubbleTea[T]) SendMsg(msg tea.Msg) {
	b.updateModel(msg)
}

func (b *BubbleTea[T]) updateModel(msg tea.Msg) {
	var cmd tea.Cmd
	b.model, cmd = b.model.Update(msg)
	b.Update()
	if cmd != nil {
		b.execCmd(cmd)
	}
}

func (b *BubbleTea[T]) execCmd(cmd tea.Cmd) {
	go func() {
		msg := cmd()
		if msg == nil {
			return
		}
		if _, ok := msg.(tea.QuitMsg); ok {
			if b.onQuit != nil {
				b.onQuit()
			}
			return
		}
		b.updateModel(msg)
	}()
}

// Render implements Component.
func (b *BubbleTea[T]) Render(ctx RenderContext) RenderResult {
	// Send WindowSizeMsg if dimensions changed.
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
	// Trim trailing empty line from final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return RenderResult{Lines: lines}
}

// HandleInput implements Interactive.
func (b *BubbleTea[T]) HandleInput(data []byte) {
	buf := data
	for len(buf) > 0 {
		n, ev := b.decoder.Decode(buf)
		if n == 0 {
			break
		}
		buf = buf[n:]
		if ev == nil {
			continue
		}

		// Convert ultraviolet events to bubbletea messages.
		var msg tea.Msg
		switch e := ev.(type) {
		case uv.KeyPressEvent:
			msg = tea.KeyPressMsg(e)
		case uv.KeyReleaseEvent:
			msg = tea.KeyReleaseMsg(e)
		default:
			continue
		}

		b.updateModel(msg)
	}
}
