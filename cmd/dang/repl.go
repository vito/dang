package main

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// replEntry groups an input line with its associated output.
type replEntry struct {
	input  string
	logs   *strings.Builder
	result *strings.Builder
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

// Styles (REPL-specific, shared between repl_tuist.go and repl_commands.go).
//
// The whole REPL/TUI sticks to the ANSI 16-color palette (colors "0".."15"),
// so it honors the user's terminal theme instead of hard-coding 256-color
// shades. Syntax-highlighting colors live in repl_highlight.go and draw from
// the same palette.
var (
	promptStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true) // magenta
	resultStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))            // green
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))            // red
	welcomeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))            // magenta
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))            // bright black
)
