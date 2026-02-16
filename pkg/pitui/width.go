package pitui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// VisibleWidth returns the terminal display width of a string, ignoring ANSI
// escape sequences and accounting for wide characters.
func VisibleWidth(s string) int {
	return ansi.StringWidth(s)
}

// Truncate truncates s to at most maxWidth visible columns, appending tail
// (e.g. "...") if truncation occurred.
func Truncate(s string, maxWidth int, tail string) string {
	return ansi.Truncate(s, maxWidth, tail)
}

// SliceByColumn extracts a range of visible columns from a line.
// Returns the substring from startCol to startCol+length (in visible columns),
// preserving ANSI escape codes that are active at that point.
func SliceByColumn(line string, startCol, length int) string {
	if length <= 0 {
		return ""
	}

	// Fast path: just truncate from start.
	if startCol == 0 {
		return ansi.Truncate(line, length, "")
	}

	// We need to walk the string tracking visible width, skipping until
	// startCol, then collecting until we have length columns.
	var (
		result       strings.Builder
		pendingANSI  strings.Builder
		col          int
		collecting   bool
		collectedW   int
	)

	remaining := line
	for len(remaining) > 0 {
		// Check for ANSI escape sequence.
		if remaining[0] == '\x1b' {
			seq, seqLen := parseEscape(remaining)
			if seqLen > 0 {
				if collecting {
					result.WriteString(seq)
				} else {
					pendingANSI.WriteString(seq)
				}
				remaining = remaining[seqLen:]
				continue
			}
		}

		// Decode one grapheme cluster.
		cluster, clusterWidth := ansi.FirstGraphemeCluster(remaining, ansi.GraphemeWidth)
		if len(cluster) == 0 {
			break
		}

		if !collecting && col >= startCol {
			collecting = true
			if pendingANSI.Len() > 0 {
				result.WriteString(pendingANSI.String())
				pendingANSI.Reset()
			}
		}

		if collecting {
			if collectedW+clusterWidth > length {
				break
			}
			result.WriteString(cluster)
			collectedW += clusterWidth
		}

		col += clusterWidth
		remaining = remaining[len(cluster):]
	}

	return result.String()
}

// parseEscape detects an ANSI escape sequence at the start of s and returns
// the full sequence and its byte length. Returns ("", 0) if s does not start
// with a recognized sequence.
func parseEscape(s string) (string, int) {
	if len(s) < 2 || s[0] != '\x1b' {
		return "", 0
	}

	switch s[1] {
	case '[': // CSI sequence: ESC [ ... <letter>
		for j := 2; j < len(s); j++ {
			b := s[j]
			if b >= 0x40 && b <= 0x7e {
				return s[:j+1], j + 1
			}
		}
	case ']': // OSC sequence: ESC ] ... BEL  or  ESC ] ... ST
		for j := 2; j < len(s); j++ {
			if s[j] == '\x07' {
				return s[:j+1], j + 1
			}
			if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
				return s[:j+2], j + 2
			}
		}
	case '_': // APC sequence: ESC _ ... BEL  or  ESC _ ... ST
		for j := 2; j < len(s); j++ {
			if s[j] == '\x07' {
				return s[:j+1], j + 1
			}
			if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
				return s[:j+2], j + 2
			}
		}
	}
	return "", 0
}

// segmentReset resets all SGR attributes and cancels any active hyperlink.
const segmentReset = "\x1b[0m\x1b]8;;\x07"
