package main

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// replEntry groups an input line with its associated output. There are
// three regions rendered in order:
//
//	input  — the echoed prompt line
//	logs   — streaming raw output (Dagger progress dots, print(), etc.)
//	result — the final "=> value" line(s), always rendered last
//
// Late-arriving log chunks update the logs section while the result
// stays anchored at the bottom.
type replEntry struct {
	input  string           // echoed prompt line ("" for system/welcome messages)
	logs   *strings.Builder // raw streaming output (no per-chunk styling)
	result *strings.Builder // final result lines
}

func newReplEntry(input string) *replEntry {
	return &replEntry{
		input:  input,
		logs:   &strings.Builder{},
		result: &strings.Builder{},
	}
}

func (e *replEntry) writeLog(s string)     { e.logs.WriteString(s) }
func (e *replEntry) writeLogLine(s string) { e.logs.WriteString(s); e.logs.WriteByte('\n') }
func (e *replEntry) writeResult(s string)  { e.result.WriteString(s); e.result.WriteByte('\n') }

// Styles (shared between REPL and doc browser)
var (
	promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	resultStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	detailTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("255")).Bold(true)
	welcomeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

// isIdentByte returns true for ASCII identifier characters.
func isIdentByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// lastIdent extracts the last identifier fragment from text.
func lastIdent(s string) string {
	i := len(s) - 1
	for i >= 0 && isIdentByte(s[i]) {
		i--
	}
	return s[i+1:]
}


