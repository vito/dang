// Package teav1 adapts bubbletea v1 models for use as pitui components.
package teav1

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/dang/pkg/pitui"
)

// Wrap wraps a bubbletea v1 model as a pitui Component. It bridges
// the two frameworks:
//
//   - Render calls the model's View() and splits into lines
//   - HandleKeyPress forwards decoded key events as tea.KeyMsg
//   - Width changes are delivered as tea.WindowSizeMsg
//   - Commands returned by Init/Update are executed asynchronously
//     and their resulting messages are fed back through Update
//
// Usage:
//
//	m := myModel{...}
//	comp := teav1.New(m)
//	tui.AddChild(comp)
type Wrap struct {
	pitui.Compo
	model  tea.Model
	width  int
	height int
	onQuit func()
}

// New wraps a bubbletea v1 model as a pitui Component.
// The model's Init() is called immediately and any returned command
// is executed.
func New(model tea.Model) *Wrap {
	b := &Wrap{model: model}
	b.Compo.Update()
	if cmd := model.Init(); cmd != nil {
		b.execCmd(cmd)
	}
	return b
}

// OnQuit sets a callback invoked when the bubbletea model returns a
// tea.Quit command.
func (b *Wrap) OnQuit(fn func()) {
	b.onQuit = fn
}

// Model returns the underlying bubbletea v1 model.
func (b *Wrap) Model() tea.Model {
	return b.model
}

// SendMsg sends a message to the bubbletea model's Update function.
func (b *Wrap) SendMsg(msg tea.Msg) {
	b.updateModel(msg)
}

func (b *Wrap) updateModel(msg tea.Msg) {
	var cmd tea.Cmd
	b.model, cmd = b.model.Update(msg)
	b.Compo.Update()
	if cmd != nil {
		b.execCmd(cmd)
	}
}

func (b *Wrap) execCmd(cmd tea.Cmd) {
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

// Render implements pitui.Component.
func (b *Wrap) Render(ctx pitui.RenderContext) pitui.RenderResult {
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
func (b *Wrap) HandleKeyPress(ev uv.KeyPressEvent) {
	b.updateModel(uvKeyToV1(uv.Key(ev)))
}

// uvKeyToV1 converts an ultraviolet Key to a bubbletea v1 KeyMsg.
func uvKeyToV1(k uv.Key) tea.KeyMsg {
	alt := k.Mod.Contains(uv.ModAlt)
	ctrl := k.Mod.Contains(uv.ModCtrl)
	shift := k.Mod.Contains(uv.ModShift)

	// Printable text (no ctrl modifier).
	if k.Text != "" && !ctrl {
		return tea.KeyMsg{
			Type:  tea.KeyRunes,
			Runes: []rune(k.Text),
			Alt:   alt,
		}
	}

	// Map special keys.
	keyType, ok := uvToV1Key[k.Code]
	if ok {
		// Apply modifier variants for arrow keys and nav keys.
		if shifted, ok := shiftedKey(keyType, ctrl, shift); ok {
			return tea.KeyMsg{Type: shifted, Alt: alt}
		}
		return tea.KeyMsg{Type: keyType, Alt: alt}
	}

	// Ctrl+letter: bubbletea v1 maps ctrl+a to KeyCtrlA (0x01), etc.
	if ctrl && k.Code >= 'a' && k.Code <= 'z' {
		return tea.KeyMsg{Type: tea.KeyType(k.Code - 'a' + 1), Alt: alt}
	}

	// Printable rune fallback.
	if k.Code >= 0x20 {
		return tea.KeyMsg{
			Type:  tea.KeyRunes,
			Runes: []rune{k.Code},
			Alt:   alt,
		}
	}

	return tea.KeyMsg{Type: tea.KeyRunes}
}

var uvToV1Key = map[rune]tea.KeyType{
	uv.KeyUp:        tea.KeyUp,
	uv.KeyDown:      tea.KeyDown,
	uv.KeyLeft:      tea.KeyLeft,
	uv.KeyRight:     tea.KeyRight,
	uv.KeyHome:      tea.KeyHome,
	uv.KeyEnd:       tea.KeyEnd,
	uv.KeyPgUp:      tea.KeyPgUp,
	uv.KeyPgDown:    tea.KeyPgDown,
	uv.KeyDelete:    tea.KeyDelete,
	uv.KeyInsert:    tea.KeyInsert,
	uv.KeyTab:       tea.KeyTab,
	uv.KeyBackspace: tea.KeyBackspace,
	uv.KeyEnter:     tea.KeyEnter,
	uv.KeyEscape:    tea.KeyEscape,
	uv.KeySpace:     tea.KeySpace,
	uv.KeyF1:        tea.KeyF1,
	uv.KeyF2:        tea.KeyF2,
	uv.KeyF3:        tea.KeyF3,
	uv.KeyF4:        tea.KeyF4,
	uv.KeyF5:        tea.KeyF5,
	uv.KeyF6:        tea.KeyF6,
	uv.KeyF7:        tea.KeyF7,
	uv.KeyF8:        tea.KeyF8,
	uv.KeyF9:        tea.KeyF9,
	uv.KeyF10:       tea.KeyF10,
	uv.KeyF11:       tea.KeyF11,
	uv.KeyF12:       tea.KeyF12,
}

// shiftedKey returns the ctrl/shift variant of a base key type, if one exists
// in bubbletea v1's key model.
func shiftedKey(base tea.KeyType, ctrl, shift bool) (tea.KeyType, bool) {
	switch {
	case ctrl && shift:
		if k, ok := ctrlShiftKeys[base]; ok {
			return k, true
		}
	case ctrl:
		if k, ok := ctrlKeys[base]; ok {
			return k, true
		}
	case shift:
		if k, ok := shiftKeys[base]; ok {
			return k, true
		}
	}
	return 0, false
}

var ctrlKeys = map[tea.KeyType]tea.KeyType{
	tea.KeyUp:     tea.KeyCtrlUp,
	tea.KeyDown:   tea.KeyCtrlDown,
	tea.KeyLeft:   tea.KeyCtrlLeft,
	tea.KeyRight:  tea.KeyCtrlRight,
	tea.KeyHome:   tea.KeyCtrlHome,
	tea.KeyEnd:    tea.KeyCtrlEnd,
	tea.KeyPgUp:   tea.KeyCtrlPgUp,
	tea.KeyPgDown: tea.KeyCtrlPgDown,
}

var shiftKeys = map[tea.KeyType]tea.KeyType{
	tea.KeyUp:    tea.KeyShiftUp,
	tea.KeyDown:  tea.KeyShiftDown,
	tea.KeyLeft:  tea.KeyShiftLeft,
	tea.KeyRight: tea.KeyShiftRight,
	tea.KeyHome:  tea.KeyShiftHome,
	tea.KeyEnd:   tea.KeyShiftEnd,
	tea.KeyTab:   tea.KeyShiftTab,
}

var ctrlShiftKeys = map[tea.KeyType]tea.KeyType{
	tea.KeyUp:    tea.KeyCtrlShiftUp,
	tea.KeyDown:  tea.KeyCtrlShiftDown,
	tea.KeyLeft:  tea.KeyCtrlShiftLeft,
	tea.KeyRight: tea.KeyCtrlShiftRight,
	tea.KeyHome:  tea.KeyCtrlShiftHome,
	tea.KeyEnd:   tea.KeyCtrlShiftEnd,
}
