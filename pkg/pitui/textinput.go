package pitui

import (
	"slices"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

// TextInput is a text editor component with cursor, history, and
// kill-line support. It supports multiline editing via Shift+Enter.
type TextInput struct {
	Compo

	// Prompt is rendered before the first line. May contain ANSI codes.
	Prompt string

	// ContinuationPrompt is rendered before continuation lines (lines
	// after the first in multiline input). Defaults to aligned spacing
	// if empty.
	ContinuationPrompt string

	// Value is the current input text.
	value []rune
	// cursor is the position within value (0 = before first rune).
	cursor int

	focused bool

	// OnSubmit is called when Enter is pressed. The string is the trimmed
	// input value. Return true to clear the input after submission.
	OnSubmit func(ctx EventContext, value string) bool

	// Suggestion is a ghost completion hint shown after the cursor. It is
	// cleared on every keystroke and must be re-set by the caller (e.g. in
	// OnSubmit or OnChange). Cleared automatically on each keystroke.
	Suggestion string

	// SuggestionStyle wraps the suggestion text (e.g. dim style).
	// If nil, the suggestion is rendered as-is.
	SuggestionStyle func(string) string

	// OnChange is called after the input value has been modified (character
	// inserted, deleted, etc.). It is NOT called for cursor-only movements.
	OnChange func(ctx EventContext)
}

// NewTextInput creates a TextInput with the given prompt.
func NewTextInput(prompt string) *TextInput {
	return &TextInput{Prompt: prompt}
}

func (t *TextInput) SetFocused(_ EventContext, focused bool) { t.focused = focused }

// Value returns the current input string.
func (t *TextInput) Value() string { return string(t.value) }

// SetValue replaces the input and moves the cursor to the end.
func (t *TextInput) SetValue(s string) {
	t.value = []rune(s)
	t.cursor = len(t.value)
}

// CursorEnd moves the cursor to the end of the input.
func (t *TextInput) CursorEnd() { t.cursor = len(t.value) }

// CursorScreenCol returns the screen column of the cursor, including
// the prompt width. This is useful for callers that need to position
// overlays (e.g. completion menus) relative to the cursor.
func (t *TextInput) CursorScreenCol() int {
	row, col := t.cursorRowCol()
	promptW := VisibleWidth(t.Prompt)
	if row > 0 && t.ContinuationPrompt != "" {
		promptW = VisibleWidth(t.ContinuationPrompt)
	}
	return promptW + col
}

// cursorRowCol computes the (row, col) of the cursor within the value,
// treating '\n' as line separators.
func (t *TextInput) cursorRowCol() (row, col int) {
	for i := 0; i < t.cursor && i < len(t.value); i++ {
		if t.value[i] == '\n' {
			row++
			col = 0
		} else {
			col++
		}
	}
	return
}

// Render returns one or more lines: prompt + input, with cursor position.
func (t *TextInput) Render(ctx RenderContext) RenderResult {
	val := string(t.value)
	inputLines := strings.Split(val, "\n")

	prompt := t.Prompt
	contPrompt := t.ContinuationPrompt
	if contPrompt == "" {
		// Default: align with the main prompt using spaces.
		w := VisibleWidth(prompt)
		contPrompt = strings.Repeat(" ", w)
	}

	var lines []string
	for i, inputLine := range inputLines {
		var buf strings.Builder
		if i == 0 {
			buf.WriteString(prompt)
		} else {
			buf.WriteString(contPrompt)
		}
		buf.WriteString(inputLine)

		// Append ghost suggestion on the last line, at the end.
		if i == len(inputLines)-1 && t.Suggestion != "" && t.cursor == len(t.value) {
			hint := t.Suggestion
			current := val
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
		lines = append(lines, line)
	}

	var cursor *CursorPos
	if t.focused {
		row, col := t.cursorRowCol()
		promptW := VisibleWidth(prompt)
		if row > 0 {
			promptW = VisibleWidth(contPrompt)
		}
		cursor = &CursorPos{Row: row, Col: promptW + col}
	}

	return RenderResult{
		Lines:  lines,
		Cursor: cursor,
	}
}

// HandleKeyPress implements [Interactive].
func (t *TextInput) HandleKeyPress(ctx EventContext, ev uv.KeyPressEvent) bool {
	return t.handleKeyPress(ctx, ev)
}

// HandlePaste implements [Pasteable].
func (t *TextInput) HandlePaste(ctx EventContext, ev uv.PasteEvent) bool {
	t.handlePaste(ctx, ev.Content)
	return true
}

func (t *TextInput) handlePaste(ctx EventContext, content string) {
	oldValue := string(t.value)
	runes := []rune(content)
	newVal := make([]rune, 0, len(t.value)+len(runes))
	newVal = append(newVal, t.value[:t.cursor]...)
	newVal = append(newVal, runes...)
	newVal = append(newVal, t.value[t.cursor:]...)
	t.value = newVal
	t.cursor += len(runes)
	t.Update()
	if t.OnChange != nil && string(t.value) != oldValue {
		t.OnChange(ctx)
	}
}

func (t *TextInput) handleKeyPress(ctx EventContext, e uv.KeyPressEvent) bool {
	key := uv.Key(e)

	oldValue := string(t.value)
	savedSuggestion := t.Suggestion
	t.Suggestion = "" // Clear suggestion on every keystroke

	// handled tracks whether the key was consumed by this component.
	// The deferred Update/OnChange runs regardless (suggestion was cleared).
	handled := true
	defer func() {
		t.Update() // any input may change cursor or content
		if t.OnChange != nil && string(t.value) != oldValue {
			t.OnChange(ctx)
		}
	}()

	// Shift+Enter, Alt+Enter, or Ctrl+J: insert newline for multiline input.
	// Shift+Enter requires kitty keyboard protocol support in the terminal.
	// Alt+Enter works in most terminals (\x1b \x0d).
	// Ctrl+J (\x0a) works universally as it's always distinct from Enter (\x0d).
	if (key.Code == uv.KeyEnter && (key.Mod.Contains(uv.ModShift) || key.Mod.Contains(uv.ModAlt))) ||
		(key.Code == 'j' && key.Mod == uv.ModCtrl) {
		t.insertRune('\n')
		return true
	}

	// Enter (unmodified): submit.
	if key.Code == uv.KeyEnter && key.Mod == 0 {
		if t.OnSubmit != nil {
			val := strings.TrimSpace(string(t.value))
			if t.OnSubmit(ctx, val) {
				t.value = nil
				t.cursor = 0
			}
		}
		return true
	}

	// Tab: accept suggestion if available, otherwise bubble.
	if key.Code == uv.KeyTab && key.Mod == 0 {
		if savedSuggestion != "" {
			t.SetValue(savedSuggestion)
			return true
		}
		handled = false
		return handled
	}

	// Backtab (Shift+Tab): bubble.
	if key.Code == uv.KeyTab && key.Mod.Contains(uv.ModShift) {
		handled = false
		return handled
	}

	// Right arrow / Ctrl+F: accept suggestion at end, else move cursor.
	if (key.Code == uv.KeyRight || key.Code == 'f' && key.Mod.Contains(uv.ModCtrl)) && !key.Mod.Contains(uv.ModShift) && !key.Mod.Contains(uv.ModAlt) {
		if key.Code == uv.KeyRight && savedSuggestion != "" && t.cursor == len(t.value) {
			t.SetValue(savedSuggestion)
			return true
		}
		if t.cursor < len(t.value) {
			t.cursor++
		}
		return true
	}

	// Backspace.
	if key.Code == uv.KeyBackspace {
		if t.cursor > 0 {
			t.value = append(t.value[:t.cursor-1], t.value[t.cursor:]...)
			t.cursor--
		}
		return true
	}

	// Delete.
	if key.Code == uv.KeyDelete {
		if t.cursor < len(t.value) {
			t.value = append(t.value[:t.cursor], t.value[t.cursor+1:]...)
		}
		return true
	}

	// Cursor movement.
	if key.Code == uv.KeyLeft && key.Mod == 0 || key.Code == 'b' && key.Mod == uv.ModCtrl {
		if t.cursor > 0 {
			t.cursor--
		}
		return true
	}
	if key.Code == uv.KeyHome || key.Code == 'a' && key.Mod == uv.ModCtrl {
		t.cursor = t.lineStart()
		return true
	}
	if key.Code == uv.KeyEnd || key.Code == 'e' && key.Mod == uv.ModCtrl {
		t.cursor = t.lineEnd()
		return true
	}

	// Up/Down: move between lines if multiline, else bubble.
	if key.Code == uv.KeyUp && key.Mod == 0 {
		if t.hasMultipleLines() {
			t.moveCursorVertically(-1)
			return true
		}
		handled = false
		return handled
	}
	if key.Code == uv.KeyDown && key.Mod == 0 {
		if t.hasMultipleLines() {
			t.moveCursorVertically(1)
			return true
		}
		handled = false
		return handled
	}

	// Word movement.
	if key.MatchString("alt+left") || key.MatchString("ctrl+left") || key.MatchString("alt+b") {
		t.cursor = t.wordLeft()
		return true
	}
	if key.MatchString("alt+right") || key.MatchString("ctrl+right") || key.MatchString("alt+f") {
		if key.Code == uv.KeyRight && savedSuggestion != "" && t.cursor == len(t.value) {
			t.SetValue(savedSuggestion)
			return true
		}
		t.cursor = t.wordRight()
		return true
	}

	// Kill line.
	if key.Code == 'u' && key.Mod == uv.ModCtrl {
		start := t.lineStart()
		t.value = append(t.value[:start], t.value[t.cursor:]...)
		t.cursor = start
		return true
	}
	if key.Code == 'k' && key.Mod == uv.ModCtrl {
		end := t.lineEnd()
		t.value = append(t.value[:t.cursor], t.value[end:]...)
		return true
	}

	// Kill subword backward (Ctrl+W).
	if key.Code == 'w' && key.Mod == uv.ModCtrl {
		start := t.subwordLeft()
		t.value = append(t.value[:start], t.value[t.cursor:]...)
		t.cursor = start
		return true
	}

	// Kill word forward (Alt+D).
	if key.Code == 'd' && key.Mod == uv.ModAlt {
		end := t.wordRight()
		t.value = append(t.value[:t.cursor], t.value[end:]...)
		return true
	}

	// Transpose (Ctrl+T).
	if key.Code == 't' && key.Mod == uv.ModCtrl {
		if t.cursor > 0 && t.cursor < len(t.value) {
			t.value[t.cursor-1], t.value[t.cursor] = t.value[t.cursor], t.value[t.cursor-1]
			t.cursor++
		}
		return true
	}

	// Unhandled non-printable keys: bubble.
	if key.Text == "" {
		handled = false
		return handled
	}

	// Insert printable text.
	runes := []rune(key.Text)
	newVal := make([]rune, 0, len(t.value)+len(runes))
	newVal = append(newVal, t.value[:t.cursor]...)
	newVal = append(newVal, runes...)
	newVal = append(newVal, t.value[t.cursor:]...)
	t.value = newVal
	t.cursor += len(runes)
	return true
}

func (t *TextInput) insertRune(r rune) {
	newVal := make([]rune, 0, len(t.value)+1)
	newVal = append(newVal, t.value[:t.cursor]...)
	newVal = append(newVal, r)
	newVal = append(newVal, t.value[t.cursor:]...)
	t.value = newVal
	t.cursor++
}

func (t *TextInput) hasMultipleLines() bool {
	return slices.Contains(t.value, '\n')
}

// lineStart returns the index of the start of the current line.
func (t *TextInput) lineStart() int {
	i := t.cursor - 1
	for i >= 0 && t.value[i] != '\n' {
		i--
	}
	return i + 1
}

// lineEnd returns the index of the end of the current line (before '\n' or at len).
func (t *TextInput) lineEnd() int {
	i := t.cursor
	for i < len(t.value) && t.value[i] != '\n' {
		i++
	}
	return i
}

// moveCursorVertically moves the cursor up (dir=-1) or down (dir=1) by one line.
func (t *TextInput) moveCursorVertically(dir int) {
	row, col := t.cursorRowCol()
	targetRow := row + dir

	// Find the target row start and length.
	lines := strings.Split(string(t.value), "\n")
	if targetRow < 0 || targetRow >= len(lines) {
		return
	}

	// Compute the rune offset of the target position.
	offset := 0
	for i := range targetRow {
		offset += len([]rune(lines[i])) + 1 // +1 for '\n'
	}
	lineLen := len([]rune(lines[targetRow]))
	if col > lineLen {
		col = lineLen
	}
	t.cursor = offset + col
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

// subwordLeft moves backward to the previous subword boundary, where
// boundaries are transitions between character classes: identifiers
// (letters/digits/_), whitespace, and symbols (everything else).
// This makes Ctrl+W stop at ".", "(", ")" etc. instead of only at
// whitespace.
func (t *TextInput) subwordLeft() int {
	i := t.cursor
	// Skip whitespace.
	for i > 0 && isSpace(t.value[i-1]) {
		i--
	}
	if i == 0 {
		return 0
	}
	// Delete a run of the same character class.
	class := runeClass(t.value[i-1])
	for i > 0 && runeClass(t.value[i-1]) == class {
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

// runeClass classifies a rune for subword boundary detection.
// 0 = identifier (letter/digit/_), 1 = symbol/punctuation.
// Whitespace is handled separately before calling this.
func runeClass(r rune) int {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
		return 0
	}
	return 1
}
