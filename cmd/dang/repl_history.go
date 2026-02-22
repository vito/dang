package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vito/dang/pkg/pitui"
)

const maxHistoryEntries = 1000

// replHistory manages REPL command history with file persistence.
type replHistory struct {
	entries []string
	index   int // -1 means "not navigating"
	file    string
}

func newReplHistory() *replHistory {
	return &replHistory{
		index: -1,
		file:  historyFilePath(),
	}
}

// historyFilePath returns the path to the history file, respecting
// XDG_DATA_HOME (default ~/.local/share/dang/history).
func historyFilePath() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "/tmp/dang_history" // last resort fallback
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "dang", "history")
}

// Add appends a line to history (skipping consecutive duplicates) and
// persists it to disk.
func (h *replHistory) Add(line string) {
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == line {
		h.index = -1
		return
	}
	h.entries = append(h.entries, line)
	h.index = -1
	h.appendToFile(line)
}

// Navigate moves through history. direction < 0 goes back, > 0 goes forward.
// Updates the text input value and cursor position.
func (h *replHistory) Navigate(direction int, ti *pitui.TextInput) {
	if len(h.entries) == 0 {
		return
	}
	if direction < 0 {
		if h.index == -1 {
			h.index = len(h.entries) - 1
		} else if h.index > 0 {
			h.index--
		}
	} else {
		if h.index == -1 {
			return
		}
		h.index++
		if h.index >= len(h.entries) {
			h.index = -1
			ti.SetValue("")
			return
		}
	}
	if h.index >= 0 && h.index < len(h.entries) {
		ti.SetValue(h.entries[h.index])
		ti.CursorEnd()
	}
}

// Reset clears navigation state (called after any non-navigation input).
func (h *replHistory) Reset() {
	h.index = -1
}

// Load reads history from the file. Should be called once at startup.
func (h *replHistory) Load() {
	data, err := os.ReadFile(h.file)
	if err != nil {
		return
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			h.entries = append(h.entries, historyDecode(line))
		}
	}
	// Truncate on load if file grew too large (amortized).
	if len(h.entries) > maxHistoryEntries {
		h.entries = h.entries[len(h.entries)-maxHistoryEntries:]
		h.rewriteFile()
	}
}

// appendToFile appends a single entry to the history file.
func (h *replHistory) appendToFile(line string) {
	_ = os.MkdirAll(filepath.Dir(h.file), 0755)
	f, err := os.OpenFile(h.file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintln(f, historyEncode(line))
}

// rewriteFile rewrites the entire history file (used for truncation).
func (h *replHistory) rewriteFile() {
	_ = os.MkdirAll(filepath.Dir(h.file), 0755)
	var buf strings.Builder
	for _, entry := range h.entries {
		buf.WriteString(historyEncode(entry))
		buf.WriteByte('\n')
	}
	_ = os.WriteFile(h.file, []byte(buf.String()), 0644)
}

// historyEncode escapes an entry for single-line storage.
// Newlines become literal \n, backslashes become \\.
func historyEncode(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// historyDecode reverses historyEncode.
func historyDecode(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				buf.WriteByte('\n')
				i++
			case '\\':
				buf.WriteByte('\\')
				i++
			default:
				buf.WriteByte(s[i])
			}
		} else {
			buf.WriteByte(s[i])
		}
	}
	return buf.String()
}
