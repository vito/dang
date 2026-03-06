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

// Styles (REPL-specific, shared between repl_pitui.go and repl_commands.go)
var (
	promptStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	resultStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	welcomeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)
