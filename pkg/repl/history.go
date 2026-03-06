package repl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vito/tuist"
)

const MaxHistoryEntries = 1000

// History manages REPL command history with file persistence.
type History struct {
	Entries []string
	Index   int // -1 means "not navigating"
	File    string
}

// NewHistory creates a new history with the given file path.
func NewHistory(file string) *History {
	return &History{
		Index: -1,
		File:  file,
	}
}

// DefaultHistoryPath returns the default history file path, respecting
// XDG_DATA_HOME (default ~/.local/share/<app>/history).
func DefaultHistoryPath(app string) string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join("/tmp", app+"_history")
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, app, "history")
}

// Add appends a line to history (skipping consecutive duplicates) and
// persists it to disk.
func (h *History) Add(line string) {
	if len(h.Entries) > 0 && h.Entries[len(h.Entries)-1] == line {
		h.Index = -1
		return
	}
	h.Entries = append(h.Entries, line)
	h.Index = -1
	h.appendToFile(line)
}

// Navigate moves through history. direction < 0 goes back, > 0 goes forward.
func (h *History) Navigate(direction int, ti *tuist.TextInput) {
	if len(h.Entries) == 0 {
		return
	}
	if direction < 0 {
		if h.Index == -1 {
			h.Index = len(h.Entries) - 1
		} else if h.Index > 0 {
			h.Index--
		}
	} else {
		if h.Index == -1 {
			return
		}
		h.Index++
		if h.Index >= len(h.Entries) {
			h.Index = -1
			ti.SetValue("")
			return
		}
	}
	if h.Index >= 0 && h.Index < len(h.Entries) {
		ti.SetValue(h.Entries[h.Index])
		ti.CursorEnd()
	}
}

// Reset clears navigation state.
func (h *History) Reset() {
	h.Index = -1
}

// Load reads history from the file.
func (h *History) Load() {
	data, err := os.ReadFile(h.File)
	if err != nil {
		return
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			h.Entries = append(h.Entries, HistoryDecode(line))
		}
	}
	if len(h.Entries) > MaxHistoryEntries {
		h.Entries = h.Entries[len(h.Entries)-MaxHistoryEntries:]
		h.rewriteFile()
	}
}

func (h *History) appendToFile(line string) {
	_ = os.MkdirAll(filepath.Dir(h.File), 0755)
	f, err := os.OpenFile(h.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, HistoryEncode(line))
}

func (h *History) rewriteFile() {
	_ = os.MkdirAll(filepath.Dir(h.File), 0755)
	var buf strings.Builder
	for _, entry := range h.Entries {
		buf.WriteString(HistoryEncode(entry))
		buf.WriteByte('\n')
	}
	_ = os.WriteFile(h.File, []byte(buf.String()), 0644)
}

// HistoryEncode escapes an entry for single-line storage.
func HistoryEncode(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// HistoryDecode reverses HistoryEncode.
func HistoryDecode(s string) string {
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
