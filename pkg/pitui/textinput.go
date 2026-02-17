package pitui

import (
	"strings"
	"unicode/utf8"
)

// TextInput is a single-line text editor component with cursor, history,
// and kill-line support.
type TextInput struct {
	// Prompt is rendered before the input text. May contain ANSI codes.
	Prompt string

	// Value is the current input text.
	value []rune
	// cursor is the position within value (0 = before first rune).
	cursor int

	focused bool

	// OnSubmit is called when Enter is pressed. The string is the trimmed
	// input value. Return true to clear the input after submission.
	OnSubmit func(value string) bool

	// OnKey is called for keys not handled by the editor. Return true if
	// the key was consumed.
	OnKey func(data []byte) bool

	// Suggestion is a ghost completion hint shown after the cursor. It is
	// cleared on every keystroke and must be re-set by the caller (e.g. in
	// OnKey or OnSubmit).
	Suggestion string

	// SuggestionStyle wraps the suggestion text (e.g. dim style).
	// If nil, the suggestion is rendered as-is.
	SuggestionStyle func(string) string

	// OnChange is called after the input value has been modified (character
	// inserted, deleted, etc.). It is NOT called for cursor-only movements.
	OnChange func()
}

// NewTextInput creates a TextInput with the given prompt.
func NewTextInput(prompt string) *TextInput {
	return &TextInput{Prompt: prompt}
}

func (t *TextInput) SetFocused(focused bool) { t.focused = focused }

// Value returns the current input string.
func (t *TextInput) Value() string { return string(t.value) }

// SetValue replaces the input and moves the cursor to the end.
func (t *TextInput) SetValue(s string) {
	t.value = []rune(s)
	t.cursor = len(t.value)
}

// CursorEnd moves the cursor to the end of the input.
func (t *TextInput) CursorEnd() { t.cursor = len(t.value) }

// Render returns a single line: prompt + input, with cursor position.
func (t *TextInput) Render(ctx RenderContext) RenderResult {
	var buf strings.Builder
	buf.WriteString(t.Prompt)

	before := string(t.value[:t.cursor])
	after := string(t.value[t.cursor:])
	buf.WriteString(before)

	// Calculate cursor column position.
	cursorCol := VisibleWidth(t.Prompt + before)

	buf.WriteString(after)

	// Append ghost suggestion if present.
	if t.Suggestion != "" && t.cursor == len(t.value) {
		hint := t.Suggestion
		current := string(t.value)
		hint = strings.TrimPrefix(hint, current)
		if hint != "" {
			if t.SuggestionStyle != nil {
				buf.WriteString(t.SuggestionStyle(hint))
			} else {
				buf.WriteString(hint)
			}
		}
	}

	line := buf.String()
	if VisibleWidth(line) > ctx.Width {
		line = Truncate(line, ctx.Width, "")
	}

	var cursor *CursorPos
	if t.focused {
		cursor = &CursorPos{Row: 0, Col: cursorCol}
	}

	return RenderResult{
		Lines:  []string{line},
		Cursor: cursor,
	}
}

// HandleInput processes raw terminal input.
func (t *TextInput) HandleInput(data []byte) {
	s := string(data)

	oldValue := string(t.value)
	savedSuggestion := t.Suggestion
	t.Suggestion = "" // Clear suggestion on every keystroke
	defer func() {
		if t.OnChange != nil && string(t.value) != oldValue {
			t.OnChange()
		}
	}()

	switch s {
	// Enter
	case KeyEnter:
		if t.OnSubmit != nil {
			val := strings.TrimSpace(string(t.value))
			if t.OnSubmit(val) {
				t.value = nil
				t.cursor = 0
			}
		}

	// Tab: accept suggestion or delegate
	case KeyTab:
		if savedSuggestion != "" {
			t.SetValue(savedSuggestion)
			return
		}
		if t.OnKey != nil {
			t.OnKey(data)
		}

	// Right arrow: accept suggestion at end of input (fish-style), else move cursor
	case KeyRight, KeyCtrlF:
		if savedSuggestion != "" && t.cursor == len(t.value) {
			t.SetValue(savedSuggestion)
			return
		}
		if t.cursor < len(t.value) {
			t.cursor++
		}

	// Backspace
	case KeyBackspace, KeyCtrlH:
		if t.cursor > 0 {
			t.value = append(t.value[:t.cursor-1], t.value[t.cursor:]...)
			t.cursor--
		}

	// Delete
	case KeyDelete:
		if t.cursor < len(t.value) {
			t.value = append(t.value[:t.cursor], t.value[t.cursor+1:]...)
		}

	// Cursor movement
	case KeyLeft, KeyCtrlB:
		if t.cursor > 0 {
			t.cursor--
		}
	case KeyHome, KeyHome2, KeyCtrlA:
		t.cursor = 0
	case KeyEnd, KeyEnd2, KeyCtrlE:
		t.cursor = len(t.value)

	// Word movement
	case KeyAltLeft, KeyCtrlLeft, KeyAltB:
		t.cursor = t.wordLeft()
	case KeyAltRight, KeyCtrlRight, KeyAltF:
		t.cursor = t.wordRight()

	// Kill line
	case KeyCtrlU:
		t.value = t.value[t.cursor:]
		t.cursor = 0
	case KeyCtrlK:
		t.value = t.value[:t.cursor]

	// Kill word backward (Ctrl+W)
	case KeyCtrlW:
		start := t.wordLeft()
		t.value = append(t.value[:start], t.value[t.cursor:]...)
		t.cursor = start

	// Kill word forward (Alt+D)
	case KeyAltD:
		end := t.wordRight()
		t.value = append(t.value[:t.cursor], t.value[end:]...)

	// Transpose (Ctrl+T)
	case KeyCtrlT:
		if t.cursor > 0 && t.cursor < len(t.value) {
			t.value[t.cursor-1], t.value[t.cursor] = t.value[t.cursor], t.value[t.cursor-1]
			t.cursor++
		}

	// Delegate to OnKey for unhandled keys
	default:
		if t.OnKey != nil && t.OnKey(data) {
			return
		}
		// If it's a printable character, insert it.
		t.insertPrintable(data)
	}
}

func (t *TextInput) insertPrintable(data []byte) {
	rest := data
	var runes []rune
	for len(rest) > 0 {
		r, size := utf8.DecodeRune(rest)
		if r == utf8.RuneError && size <= 1 {
			return
		}
		if r < 0x20 && r != '\t' {
			return
		}
		runes = append(runes, r)
		rest = rest[size:]
	}
	if len(runes) == 0 {
		return
	}

	newVal := make([]rune, 0, len(t.value)+len(runes))
	newVal = append(newVal, t.value[:t.cursor]...)
	newVal = append(newVal, runes...)
	newVal = append(newVal, t.value[t.cursor:]...)
	t.value = newVal
	t.cursor += len(runes)
}

func (t *TextInput) wordLeft() int {
	i := t.cursor
	for i > 0 && isSpace(t.value[i-1]) {
		i--
	}
	for i > 0 && !isSpace(t.value[i-1]) {
		i--
	}
	return i
}

func (t *TextInput) wordRight() int {
	i := t.cursor
	for i < len(t.value) && !isSpace(t.value[i]) {
		i++
	}
	for i < len(t.value) && isSpace(t.value[i]) {
		i++
	}
	return i
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}
