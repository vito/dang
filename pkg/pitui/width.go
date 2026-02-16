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

// extractSegments extracts "before" (columns 0..beforeEnd) and "after"
// (columns afterStart..afterStart+afterLen) content from a line in a single
// pass. Used for overlay compositing.
func extractSegments(line string, beforeEnd, afterStart, afterLen int) (before string, beforeW int, after string, afterW int) {
	if len(line) == 0 {
		return "", 0, "", 0
	}

	var (
		beforeBuf    strings.Builder
		afterBuf     strings.Builder
		pendingANSI  strings.Builder
		col          int
		afterEnd     = afterStart + afterLen
		afterStarted bool
	)

	remaining := line
	for len(remaining) > 0 {
		if remaining[0] == '\x1b' {
			seq, seqLen := parseEscape(remaining)
			if seqLen > 0 {
				if col < beforeEnd {
					pendingANSI.WriteString(seq)
				} else if col >= afterStart && col < afterEnd && afterStarted {
					afterBuf.WriteString(seq)
				}
				remaining = remaining[seqLen:]
				continue
			}
		}

		cluster, clusterWidth := ansi.FirstGraphemeCluster(remaining, ansi.GraphemeWidth)
		if len(cluster) == 0 {
			break
		}

		if col < beforeEnd {
			if pendingANSI.Len() > 0 {
				beforeBuf.WriteString(pendingANSI.String())
				pendingANSI.Reset()
			}
			beforeBuf.WriteString(cluster)
			beforeW += clusterWidth
		} else if col >= afterStart && col < afterEnd {
			if !afterStarted {
				afterStarted = true
			}
			if clusterWidth <= afterEnd-col {
				afterBuf.WriteString(cluster)
				afterW += clusterWidth
			}
		}

		col += clusterWidth
		if afterLen <= 0 {
			if col >= beforeEnd {
				break
			}
		} else if col >= afterEnd {
			break
		}

		remaining = remaining[len(cluster):]
	}

	return beforeBuf.String(), beforeW, afterBuf.String(), afterW
}

// compositeLineAt splices overlay content into a base line at a specific
// column. Returns a line of exactly totalWidth visible columns.
func compositeLineAt(baseLine, overlayLine string, startCol, overlayWidth, totalWidth int) string {
	afterStart := startCol + overlayWidth
	before, beforeW, after, afterW := extractSegments(baseLine, startCol, afterStart, totalWidth-afterStart)

	overlayTrunc := ansi.Truncate(overlayLine, overlayWidth, "")
	overlayW := VisibleWidth(overlayTrunc)

	beforePad := max(0, startCol-beforeW)
	overlayPad := max(0, overlayWidth-overlayW)
	actualBeforeW := max(startCol, beforeW)
	actualOverlayW := max(overlayWidth, overlayW)
	afterTarget := max(0, totalWidth-actualBeforeW-actualOverlayW)
	afterPad := max(0, afterTarget-afterW)

	var buf strings.Builder
	buf.Grow(len(before) + beforePad + len(segmentReset) + len(overlayTrunc) + overlayPad + len(segmentReset) + len(after) + afterPad)

	buf.WriteString(before)
	writeSpaces(&buf, beforePad)
	buf.WriteString(segmentReset)
	buf.WriteString(overlayTrunc)
	writeSpaces(&buf, overlayPad)
	buf.WriteString(segmentReset)
	buf.WriteString(after)
	writeSpaces(&buf, afterPad)

	result := buf.String()
	if VisibleWidth(result) > totalWidth {
		result = ansi.Truncate(result, totalWidth, "")
	}
	return result
}

func writeSpaces(b *strings.Builder, n int) {
	for i := 0; i < n; i++ {
		b.WriteByte(' ')
	}
}
