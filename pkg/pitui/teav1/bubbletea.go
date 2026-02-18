// Package teav1 adapts bubbletea v1 models for use as pitui components.
package teav1

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vito/dang/pkg/pitui"
)

// Wrap wraps a bubbletea v1 model as a pitui Component. It bridges
// the two frameworks:
//
//   - Render calls the model's View() and splits into lines
//   - HandleInput parses raw terminal bytes into tea.KeyMsg and
//     feeds them through Update
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

// HandleInput implements pitui.Interactive.
func (b *Wrap) HandleInput(data []byte) {
	buf := data
	for len(buf) > 0 {
		n, msg := parseKeyMsg(buf)
		if n == 0 {
			break
		}
		buf = buf[n:]
		if msg == nil {
			continue
		}
		b.updateModel(msg)
	}
}

// parseKeyMsg parses raw terminal bytes into a bubbletea v1 KeyMsg.
func parseKeyMsg(buf []byte) (int, tea.Msg) {
	if len(buf) == 0 {
		return 0, nil
	}

	// Try escape sequences (longest match first).
	if buf[0] == '\x1b' && len(buf) > 1 {
		best := 0
		var bestKey tea.Key
		for seq, key := range sequences {
			if len(seq) > len(buf) {
				continue
			}
			if string(buf[:len(seq)]) == seq && len(seq) > best {
				best = len(seq)
				bestKey = key
			}
		}
		if best > 0 {
			return best, tea.KeyMsg(bestKey)
		}

		// Alt+key: ESC followed by a printable character.
		if len(buf) >= 2 && buf[1] >= 0x20 && buf[1] < 0x7f {
			return 2, tea.KeyMsg{
				Type:  tea.KeyRunes,
				Runes: []rune{rune(buf[1])},
				Alt:   true,
			}
		}

		// Lone escape.
		return 1, tea.KeyMsg{Type: tea.KeyEscape}
	}

	// Control characters.
	if buf[0] < 0x20 {
		switch buf[0] {
		case '\r', '\n':
			return 1, tea.KeyMsg{Type: tea.KeyEnter}
		case '\t':
			return 1, tea.KeyMsg{Type: tea.KeyTab}
		default:
			return 1, tea.KeyMsg{Type: tea.KeyType(buf[0])}
		}
	}

	// DEL (backspace on most terminals).
	if buf[0] == 0x7f {
		return 1, tea.KeyMsg{Type: tea.KeyBackspace}
	}

	// Space.
	if buf[0] == ' ' {
		return 1, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	}

	// Printable ASCII / UTF-8.
	r, size := utf8.DecodeRune(buf)
	if r == utf8.RuneError && size <= 1 {
		return 1, nil
	}
	return size, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// sequences maps escape sequences to bubbletea v1 Key values.
var sequences = map[string]tea.Key{
	// Arrow keys
	"\x1b[A": {Type: tea.KeyUp},
	"\x1b[B": {Type: tea.KeyDown},
	"\x1b[C": {Type: tea.KeyRight},
	"\x1b[D": {Type: tea.KeyLeft},

	"\x1b[1;2A": {Type: tea.KeyShiftUp},
	"\x1b[1;2B": {Type: tea.KeyShiftDown},
	"\x1b[1;2C": {Type: tea.KeyShiftRight},
	"\x1b[1;2D": {Type: tea.KeyShiftLeft},

	"\x1b[1;3A": {Type: tea.KeyUp, Alt: true},
	"\x1b[1;3B": {Type: tea.KeyDown, Alt: true},
	"\x1b[1;3C": {Type: tea.KeyRight, Alt: true},
	"\x1b[1;3D": {Type: tea.KeyLeft, Alt: true},

	"\x1b[1;5A": {Type: tea.KeyCtrlUp},
	"\x1b[1;5B": {Type: tea.KeyCtrlDown},
	"\x1b[1;5C": {Type: tea.KeyCtrlRight},
	"\x1b[1;5D": {Type: tea.KeyCtrlLeft},

	// Powershell / DECCKM
	"\x1bOA": {Type: tea.KeyUp},
	"\x1bOB": {Type: tea.KeyDown},
	"\x1bOC": {Type: tea.KeyRight},
	"\x1bOD": {Type: tea.KeyLeft},

	// Misc
	"\x1b[Z":  {Type: tea.KeyShiftTab},
	"\x1b[2~": {Type: tea.KeyInsert},
	"\x1b[3~": {Type: tea.KeyDelete},
	"\x1b[5~": {Type: tea.KeyPgUp},
	"\x1b[6~": {Type: tea.KeyPgDown},

	"\x1b[1~": {Type: tea.KeyHome},
	"\x1b[H":  {Type: tea.KeyHome},
	"\x1b[4~": {Type: tea.KeyEnd},
	"\x1b[F":  {Type: tea.KeyEnd},
	"\x1b[7~": {Type: tea.KeyHome}, // urxvt
	"\x1b[8~": {Type: tea.KeyEnd},  // urxvt

	// Function keys
	"\x1bOP":   {Type: tea.KeyF1},
	"\x1bOQ":   {Type: tea.KeyF2},
	"\x1bOR":   {Type: tea.KeyF3},
	"\x1bOS":   {Type: tea.KeyF4},
	"\x1b[15~": {Type: tea.KeyF5},
	"\x1b[17~": {Type: tea.KeyF6},
	"\x1b[18~": {Type: tea.KeyF7},
	"\x1b[19~": {Type: tea.KeyF8},
	"\x1b[20~": {Type: tea.KeyF9},
	"\x1b[21~": {Type: tea.KeyF10},
	"\x1b[23~": {Type: tea.KeyF11},
	"\x1b[24~": {Type: tea.KeyF12},

	// Linux console
	"\x1b[[A": {Type: tea.KeyF1},
	"\x1b[[B": {Type: tea.KeyF2},
	"\x1b[[C": {Type: tea.KeyF3},
	"\x1b[[D": {Type: tea.KeyF4},
	"\x1b[[E": {Type: tea.KeyF5},
}
