package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kr/pretty"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/ioctx"
)

// syncWriter is a thread-safe writer that streams output into the REPL.
// Dagger's WithLogOutput writes to it from arbitrary goroutines; each
// write sends a tea message so progress appears in real-time.
type syncWriter struct {
	mu   sync.Mutex
	prog *tea.Program
}

// lastEntry returns a pointer to the most recent entry, creating one if
// needed. All streaming output (dagger logs, eval results) goes here.
func (m *replModel) lastEntry() *replEntry {
	if len(m.entries) == 0 {
		m.entries = append(m.entries, newReplEntry(""))
	}
	return m.entries[len(m.entries)-1]
}

// daggerLogMsg is sent when Dagger writes progress output.
type daggerLogMsg string

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	prog := w.prog
	w.mu.Unlock()
	if prog != nil {
		prog.Send(daggerLogMsg(string(p)))
	}
	return len(p), nil
}

// SetProgram wires up the tea.Program so writes become messages.
// Called after tea.NewProgram is created.
func (w *syncWriter) SetProgram(p *tea.Program) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prog = p
}

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
	input  string          // echoed prompt line ("" for system/welcome messages)
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

// writeLog appends raw text to the log region.
func (e *replEntry) writeLog(s string) {
	e.logs.WriteString(s)
}

// writeLogLine appends a complete line to the log region.
func (e *replEntry) writeLogLine(s string) {
	e.logs.WriteString(s)
	e.logs.WriteByte('\n')
}

// writeResult appends a complete line to the result region.
func (e *replEntry) writeResult(s string) {
	e.result.WriteString(s)
	e.result.WriteByte('\n')
}

// replModel is the Bubbletea model for the Dang REPL.
type replModel struct {
	// Dang state
	importConfigs []dang.ImportConfig
	debug         bool
	typeEnv       dang.Env
	evalEnv       dang.EvalEnv

	// UI state
	textInput textinput.Model
	entries   []*replEntry // structured output log
	width     int
	height    int
	quitting  bool

	// Scrollback: how many lines the viewport is scrolled up from the bottom.
	scrollOffset int

	// Completion state
	completions    []string // all available completions
	menuVisible    bool     // whether the completion menu is shown
	menuItems      []string // current filtered menu items
	menuIndex      int      // selected item in menu
	menuMaxVisible int      // max items shown at once

	// Evaluation state
	evaluating bool               // true while an expression is being evaluated
	spinner    spinner.Model      // spinner shown during evaluation
	cancelEval context.CancelFunc // cancels the in-flight evaluation

	// Doc browser (nil when not active)
	docBrowser *docBrowserModel

	// History
	history      []string
	historyIndex int
	historyFile  string

	// Context for evaluation
	ctx context.Context
}

// evalResultMsg is sent when a background evaluation completes.
type evalResultMsg struct {
	logs    []string // stdout/print output lines
	results []string // "=> value" result lines
}

// evalCancelledMsg is sent when an evaluation is cancelled.
type evalCancelledMsg struct{}

// Styles
var (
	promptStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	resultStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	menuStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("237"))
	menuSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("63")).Bold(true)
	menuBorderStyle   = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("63"))
	hintStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	welcomeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

func newREPLModel(ctx context.Context, importConfigs []dang.ImportConfig, debug bool) replModel {
	typeEnv, evalEnv := buildEnvFromImports(importConfigs)

	ti := textinput.New()
	ti.Prompt = promptStyle.Render("dang> ")
	ti.SetVirtualCursor(false)
	ti.Focus()
	ti.CharLimit = 4096
	ti.ShowSuggestions = true

	// Style the inline suggestion hint (fish-style)
	s := ti.Styles()
	s.Focused.Suggestion = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	ti.SetStyles(s)

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))

	m := replModel{
		importConfigs:  importConfigs,
		debug:          debug,
		typeEnv:        typeEnv,
		evalEnv:        evalEnv,
		textInput:      ti,
		spinner:        sp,
		menuMaxVisible: 8,
		historyIndex:   -1,
		historyFile:    "/tmp/dang_history",
		ctx:            ctx,
	}

	// Welcome message
	welcome := newReplEntry("")
	welcome.writeLogLine(welcomeStyle.Render("Welcome to Dang REPL v0.1.0"))
	if len(m.importConfigs) > 0 {
		var names []string
		for _, ic := range m.importConfigs {
			names = append(names, ic.Name)
		}
		welcome.writeLogLine(dimStyle.Render(fmt.Sprintf("Imports: %s", strings.Join(names, ", "))))
	}
	welcome.writeLogLine("")
	welcome.writeLogLine(dimStyle.Render("Type :help for commands, Tab for completion, Ctrl+C to exit"))
	welcome.writeLogLine("")
	m.entries = append(m.entries, welcome)

	m.completions = m.buildCompletions()
	m.textInput.SetSuggestions(m.completions)

	// Load history
	m.loadHistory()

	return m
}

func (m replModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m replModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Delegate to doc browser when active
	if m.docBrowser != nil {
		switch msg := msg.(type) {
		case docBrowserExitMsg:
			m.docBrowser = nil
			return m, nil
		case tea.WindowSizeMsg:
			m.width = msg.Width
			m.height = msg.Height
			m.textInput.SetWidth(msg.Width - lipgloss.Width(promptStyle.Render("dang> ")) - 1)
			updated, cmd := m.docBrowser.Update(msg)
			db := updated.(docBrowserModel)
			m.docBrowser = &db
			return m, cmd
		default:
			updated, cmd := m.docBrowser.Update(msg)
			db := updated.(docBrowserModel)
			m.docBrowser = &db
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textInput.SetWidth(msg.Width - lipgloss.Width(promptStyle.Render("dang> ")) - 1)
		return m, nil

	case daggerLogMsg:
		// Append raw chunk to the last entry's log region — partial
		// lines (like progress dots) stay on the same line naturally.
		m.lastEntry().writeLog(string(msg))
		m.scrollOffset = 0
		return m, nil

	case evalResultMsg:
		m.evaluating = false
		m.cancelEval = nil
		m.refreshCompletions()
		e := m.lastEntry()
		for _, line := range msg.logs {
			e.writeLogLine(line)
		}
		for _, line := range msg.results {
			e.writeResult(line)
		}
		m.scrollOffset = 0
		return m, nil

	case evalCancelledMsg:
		m.evaluating = false
		m.cancelEval = nil
		m.lastEntry().writeLogLine(errorStyle.Render("cancelled"))
		m.scrollOffset = 0
		return m, nil

	case spinner.TickMsg:
		if m.evaluating {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyPressMsg:
		// While evaluating, only handle Ctrl+C to cancel
		if m.evaluating {
			if msg.String() == "ctrl+c" {
				if m.cancelEval != nil {
					m.cancelEval()
				}
				return m, nil
			}
			// Ignore all other keys during evaluation
			return m, nil
		}

		// Handle menu navigation when menu is visible
		if m.menuVisible {
			switch msg.String() {
			case "tab":
				// Accept selected menu item
				if m.menuIndex < len(m.menuItems) {
					m.textInput.SetValue(m.menuItems[m.menuIndex])
					m.textInput.CursorEnd()
				}
				m.menuVisible = false
				m.updateCompletionMenu()
				return m, nil
			case "down", "ctrl+n":
				m.menuIndex++
				if m.menuIndex >= len(m.menuItems) {
					m.menuIndex = 0
				}
				return m, nil
			case "up", "ctrl+p":
				m.menuIndex--
				if m.menuIndex < 0 {
					m.menuIndex = len(m.menuItems) - 1
				}
				return m, nil
			case "escape":
				m.menuVisible = false
				return m, nil
			case "enter":
				// Accept selected and execute
				if m.menuIndex < len(m.menuItems) {
					m.textInput.SetValue(m.menuItems[m.menuIndex])
					m.textInput.CursorEnd()
				}
				m.menuVisible = false
				// fall through to enter handling below
			}
		}

		switch msg.String() {
		case "ctrl+c":
			if m.textInput.Value() != "" {
				m.textInput.SetValue("")
				m.menuVisible = false
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit

		case "ctrl+d":
			if m.textInput.Value() == "" {
				m.quitting = true
				return m, tea.Quit
			}

		case "enter":
			line := strings.TrimSpace(m.textInput.Value())
			if line == "" {
				return m, nil
			}

			// Add to history
			m.addHistory(line)

			// Create a new entry for this input — all output
			// (eval results, dagger logs) streams into it.
			m.entries = append(m.entries, newReplEntry(
				promptStyle.Render("dang> ")+line,
			))

			// Clear input immediately
			m.textInput.SetValue("")
			m.menuVisible = false
			m.scrollOffset = 0

			// Commands run synchronously (they're fast)
			if strings.HasPrefix(line, ":") {
				m.handleCommand(line[1:])
				if m.quitting {
					return m, tea.Quit
				}
				m.updateCompletionMenu()
				return m, nil
			}

			// Expressions run asynchronously with a spinner.
			evalCmd := m.startEval(line)
			return m, evalCmd

		case "up":
			if !m.menuVisible {
				// History navigation
				m.navigateHistory(-1)
				return m, nil
			}

		case "down":
			if !m.menuVisible {
				m.navigateHistory(1)
				return m, nil
			}

		case "ctrl+l":
			m.entries = nil
			m.scrollOffset = 0
			return m, nil

		// Scroll through output
		case "pgup", "shift+up":
			maxScroll := len(m.renderedLines())
			m.scrollOffset = min(m.scrollOffset+5, maxScroll)
			return m, nil
		case "pgdown", "shift+down":
			m.scrollOffset = max(m.scrollOffset-5, 0)
			return m, nil
		}
	}

	// Don't process text input while evaluating
	if m.evaluating {
		return m, nil
	}

	// Update text input
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)

	// Update completion menu based on current input
	m.updateCompletionMenu()

	return m, cmd
}

func (m replModel) View() tea.View {
	if m.docBrowser != nil {
		return m.docBrowser.View()
	}

	// Reserve 1 line for the input.
	outputHeight := m.height - 1
	if outputHeight < 0 {
		outputHeight = 0
	}

	// Build the visible portion of the output log.
	visibleOutput := m.visibleOutputLines(outputHeight)

	// Build the input line.
	var inputLine string
	if m.evaluating {
		inputLine = m.spinner.View() + dimStyle.Render("Evaluating... (Ctrl+C to cancel)")
	} else {
		inputLine = m.textInput.View()
	}

	// Compose the full view: output + input at the bottom.
	base := visibleOutput + "\n" + inputLine

	// When the completion menu is visible, composite it as a floating layer
	// over the output area so the input line doesn't move.
	if !m.evaluating && m.menuVisible && len(m.menuItems) > 0 {
		menu := m.renderMenu()
		menuH := lipgloss.Height(menu)

		// Position the menu just above the input line.
		menuY := outputHeight - menuH
		if menuY < 0 {
			menuY = 0
		}

		// Align horizontally to the token being completed.
		menuX := m.completionXOffset()

		comp := lipgloss.NewCompositor(
			lipgloss.NewLayer(base),
			lipgloss.NewLayer(menu).X(menuX).Y(menuY).Z(1),
		)

		v := tea.NewView(comp)
		v.AltScreen = true
		if cursor := m.textInput.Cursor(); cursor != nil {
			cursor.Y += outputHeight
			v.Cursor = cursor
		}
		return v
	}

	v := tea.NewView(base)
	v.AltScreen = true
	if !m.evaluating {
		if cursor := m.textInput.Cursor(); cursor != nil {
			cursor.Y += outputHeight
			v.Cursor = cursor
		}
	}
	return v
}

// renderedLines flattens all entries into display lines.
// Each entry renders as: input, then logs, then result.
func (m replModel) renderedLines() []string {
	var lines []string
	for _, e := range m.entries {
		if e.input != "" {
			lines = append(lines, e.input)
		}
		// Logs region (streaming Dagger progress, print() output, etc.)
		if logStr := e.logs.String(); logStr != "" {
			logLines := strings.Split(logStr, "\n")
			// Drop trailing empty element from a final newline
			if len(logLines) > 0 && logLines[len(logLines)-1] == "" {
				logLines = logLines[:len(logLines)-1]
			}
			lines = append(lines, logLines...)
		}
		// Result region ("=> value" lines, always last)
		if resStr := e.result.String(); resStr != "" {
			resLines := strings.Split(resStr, "\n")
			if len(resLines) > 0 && resLines[len(resLines)-1] == "" {
				resLines = resLines[:len(resLines)-1]
			}
			lines = append(lines, resLines...)
		}
	}
	return lines
}

// visibleOutputLines returns the output text that fits in the given height,
// respecting the current scroll offset. The returned string always has
// exactly `height` newline-delimited lines (padded with blanks if needed).
func (m replModel) visibleOutputLines(height int) string {
	if height <= 0 {
		return ""
	}

	all := m.renderedLines()
	total := len(all)

	// end is the index just past the last visible line (from the bottom).
	end := total - m.scrollOffset
	if end < 0 {
		end = 0
	}
	if end > total {
		end = total
	}

	start := end - height
	if start < 0 {
		start = 0
	}

	visible := all[start:end]

	// Pad with empty lines so the output region is always `height` tall,
	// pushing the input to the bottom of the screen.
	padCount := height - len(visible)
	lines := make([]string, 0, height)
	for i := 0; i < padCount; i++ {
		lines = append(lines, "")
	}
	lines = append(lines, visible...)

	return strings.Join(lines, "\n")
}

// completionXOffset returns the column at which to place the completion popup,
// aligned to the start of the token being completed.
func (m replModel) completionXOffset() int {
	val := m.textInput.Value()
	promptWidth := lipgloss.Width(promptStyle.Render("dang> "))

	// Walk backwards to find the start of the current token (including dot prefix).
	i := len(val) - 1
	for i >= 0 && isIdentByte(val[i]) {
		i--
	}
	// Include a leading dot (for member completions like "container.fr")
	if i >= 0 && val[i] == '.' {
		i--
		for i >= 0 && isIdentByte(val[i]) {
			i--
		}
	}
	tokenStart := i + 1

	return promptWidth + tokenStart
}

// renderMenu renders the completion dropdown menu with a border.
func (m replModel) renderMenu() string {
	if len(m.menuItems) == 0 {
		return ""
	}

	visible := min(len(m.menuItems), m.menuMaxVisible)

	// Scroll the menu if needed
	start := 0
	if m.menuIndex >= visible {
		start = m.menuIndex - visible + 1
	}
	end := start + visible

	// Find the widest item for consistent padding
	maxWidth := 0
	for i := start; i < end && i < len(m.menuItems); i++ {
		if w := lipgloss.Width(m.menuItems[i]); w > maxWidth {
			maxWidth = w
		}
	}
	// Clamp
	if maxWidth > 60 {
		maxWidth = 60
	}
	if maxWidth < 20 {
		maxWidth = 20
	}

	var lines []string
	for i := start; i < end && i < len(m.menuItems); i++ {
		item := m.menuItems[i]
		// Truncate long items
		if len(item) > maxWidth {
			item = item[:maxWidth-3] + "..."
		}
		padded := fmt.Sprintf(" %-*s ", maxWidth, item)

		if i == m.menuIndex {
			lines = append(lines, menuSelectedStyle.Render(padded))
		} else {
			lines = append(lines, menuStyle.Render(padded))
		}
	}

	// Show scroll indicator inside the box
	if len(m.menuItems) > visible {
		info := fmt.Sprintf(" %d/%d ", m.menuIndex+1, len(m.menuItems))
		lines = append(lines, dimStyle.Render(info))
	}

	inner := strings.Join(lines, "\n")
	return menuBorderStyle.Render(inner)
}

// updateCompletionMenu updates the completion menu based on current input.
func (m *replModel) updateCompletionMenu() {
	val := m.textInput.Value()

	// Don't show menu for commands or empty input
	if val == "" || strings.HasPrefix(val, ":") {
		m.menuVisible = false
		m.menuItems = nil
		m.textInput.SetSuggestions(m.completions)
		return
	}

	// Use dang.CompleteInput for context-aware completions (handles dotted
	// member access like "container.fr" as well as lexical completions).
	cursorPos := len(val) // cursor is always at the end in REPL
	completions := dang.CompleteInput(m.ctx, m.typeEnv, val, cursorPos)

	if len(completions) > 0 {
		// We got context-aware completions (possibly member completions).
		// Figure out what prefix the user typed for the current token so
		// we can build full suggestion strings for the textinput bubble.
		prefix, partial := splitForSuggestion(val)

		var matches []string
		partialLower := strings.ToLower(partial)
		for _, c := range completions {
			cLower := strings.ToLower(c.Label)
			if cLower == partialLower {
				continue
			}
			if strings.HasPrefix(cLower, partialLower) {
				matches = append(matches, prefix+c.Label)
			}
		}

		// Sort: exact-case first
		matches = sortByCase(matches, prefix, partial)

		m.textInput.SetSuggestions(matches)
		m.setMenu(matches)
		return
	}

	// Fall back to static completions for simple prefix matching
	word := lastIdent(val)
	if word == "" {
		m.menuVisible = false
		m.menuItems = nil
		m.textInput.SetSuggestions(m.completions)
		return
	}

	// Filter and sort by case preference
	var exactCase, otherCase []string
	wordLower := strings.ToLower(word)
	for _, c := range m.completions {
		cLower := strings.ToLower(c)
		if cLower == wordLower {
			continue
		}
		if strings.HasPrefix(c, word) {
			exactCase = append(exactCase, c)
		} else if strings.HasPrefix(cLower, wordLower) {
			otherCase = append(otherCase, c)
		}
	}
	matches := append(exactCase, otherCase...)

	m.textInput.SetSuggestions(matches)
	m.setMenu(matches)
}

// setMenu updates the completion menu state from a matches list.
func (m *replModel) setMenu(matches []string) {
	if len(matches) <= 1 {
		m.menuVisible = false
		m.menuItems = nil
		return
	}
	m.menuItems = matches
	m.menuVisible = true
	if m.menuIndex >= len(matches) {
		m.menuIndex = 0
	}
}

// splitForSuggestion splits input into a prefix (everything before the current
// token being completed) and the partial token. For "container.fr" it returns
// ("container.", "fr"). For "dir" it returns ("", "dir").
func splitForSuggestion(val string) (prefix, partial string) {
	i := len(val) - 1
	for i >= 0 && isIdentByte(val[i]) {
		i--
	}
	if i >= 0 && val[i] == '.' {
		return val[:i+1], val[i+1:]
	}
	return val[:i+1], val[i+1:]
}

func isIdentByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// sortByCase re-orders matches so that entries whose suffix (after prefix)
// matches the case of partial come first.
func sortByCase(matches []string, prefix, partial string) []string {
	var exact, other []string
	for _, m := range matches {
		suffix := strings.TrimPrefix(m, prefix)
		if strings.HasPrefix(suffix, partial) {
			exact = append(exact, m)
		} else {
			other = append(other, m)
		}
	}
	return append(exact, other...)
}

// lastIdent extracts the last plain identifier from text (no dots).
func lastIdent(s string) string {
	i := len(s) - 1
	for i >= 0 && isIdentByte(s[i]) {
		i--
	}
	return s[i+1:]
}

// buildCompletions builds the full list of completions from the environment.
func (m *replModel) buildCompletions() []string {
	seen := map[string]bool{}
	var completions []string

	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			completions = append(completions, name)
		}
	}

	// REPL commands
	for _, cmd := range replCommands() {
		add(":" + cmd)
	}

	// Dang keywords
	keywords := []string{
		"let", "if", "else", "for", "in", "true", "false", "null",
		"self", "type", "pub", "new", "import", "assert", "try",
		"catch", "raise", "print",
	}
	for _, kw := range keywords {
		add(kw)
	}

	// Environment bindings (from type env, includes all imports).
	// Use the same filtering as completeLexical to exclude type
	// definitions and ID types.
	for name, scheme := range m.typeEnv.Bindings(dang.PublicVisibility) {
		if dang.IsTypeDefBinding(scheme) || dang.IsIDTypeName(name) {
			continue
		}
		add(name)
	}

	sort.Strings(completions)
	return completions
}

// refreshCompletions rebuilds completions (called after env changes).
func (m *replModel) refreshCompletions() {
	m.completions = m.buildCompletions()
	m.textInput.SetSuggestions(m.completions)
}

// startEval begins asynchronous evaluation of a Dang expression.
// Returns a tea.Cmd that runs the evaluation in a goroutine.
func (m *replModel) startEval(expr string) tea.Cmd {
	e := m.lastEntry()

	// Parse synchronously (fast) so we can show errors immediately
	result, err := dang.Parse("repl", []byte(expr))
	if err != nil {
		e.writeLogLine(errorStyle.Render(fmt.Sprintf("parse error: %v", err)))
		return nil
	}

	forms := result.(*dang.ModuleBlock).Forms

	if m.debug {
		for _, node := range forms {
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("%# v", pretty.Formatter(node))))
		}
	}

	// Type inference synchronously (fast)
	fresh := hm.NewSimpleFresher()
	_, err = dang.InferFormsWithPhases(m.ctx, forms, m.typeEnv, fresh)
	if err != nil {
		e.writeLogLine(errorStyle.Render(fmt.Sprintf("type error: %v", err)))
		return nil
	}

	// Set up cancellable context for evaluation
	evalCtx, cancel := context.WithCancel(m.ctx)
	m.evaluating = true
	m.cancelEval = cancel

	// Capture references needed by the goroutine
	evalEnv := m.evalEnv
	debug := m.debug

	// Start spinner and evaluation concurrently
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		var logs []string
		var results []string

		// Capture stdout from print() calls
		var stdoutBuf bytes.Buffer
		ctx := ioctx.StdoutToContext(evalCtx, &stdoutBuf)
		ctx = ioctx.StderrToContext(ctx, &stdoutBuf)

		for _, node := range forms {
			val, err := dang.EvalNode(ctx, evalEnv, node)

			// Check for cancellation
			if evalCtx.Err() != nil {
				return evalCancelledMsg{}
			}

			// Flush captured stdout into logs
			if stdoutBuf.Len() > 0 {
				for _, line := range strings.Split(strings.TrimRight(stdoutBuf.String(), "\n"), "\n") {
					logs = append(logs, line)
				}
				stdoutBuf.Reset()
			}

			if err != nil {
				results = append(results, errorStyle.Render(fmt.Sprintf("evaluation error: %v", err)))
				return evalResultMsg{logs: logs, results: results}
			}

			results = append(results, resultStyle.Render(fmt.Sprintf("=> %s", val.String())))

			if debug {
				results = append(results, dimStyle.Render(fmt.Sprintf("%# v", pretty.Formatter(val))))
			}
		}

		return evalResultMsg{logs: logs, results: results}
	})
}

// handleCommand handles REPL :commands.
func (m *replModel) handleCommand(cmdLine string) {
	e := m.lastEntry()

	parts := strings.Fields(cmdLine)
	if len(parts) == 0 {
		e.writeLogLine(errorStyle.Render("empty command"))
		return
	}

	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "help":
		e.writeLogLine("Available commands:")
		e.writeLogLine(dimStyle.Render("  :help      - Show this help"))
		e.writeLogLine(dimStyle.Render("  :exit      - Exit the REPL"))
		e.writeLogLine(dimStyle.Render("  :doc       - Interactive API browser"))
		e.writeLogLine(dimStyle.Render("  :env       - Show environment bindings"))
		e.writeLogLine(dimStyle.Render("  :type      - Show type of an expression"))
		e.writeLogLine(dimStyle.Render("  :find      - Find functions/types by pattern"))
		e.writeLogLine(dimStyle.Render("  :reset     - Reset the environment"))
		e.writeLogLine(dimStyle.Render("  :clear     - Clear the screen"))
		e.writeLogLine(dimStyle.Render("  :debug     - Toggle debug mode"))
		e.writeLogLine(dimStyle.Render("  :version   - Show version info"))
		e.writeLogLine(dimStyle.Render("  :quit      - Exit the REPL"))
		e.writeLogLine("")
		e.writeLogLine(dimStyle.Render("Type Dang expressions to evaluate them."))
		e.writeLogLine(dimStyle.Render("Tab for completion, ↑/↓ for history, Ctrl+L to clear."))

	case "exit", "quit":
		m.quitting = true

	case "clear":
		m.entries = nil
		m.scrollOffset = 0

	case "reset":
		m.typeEnv, m.evalEnv = buildEnvFromImports(m.importConfigs)
		m.refreshCompletions()
		e.writeLogLine(resultStyle.Render("Environment reset."))

	case "debug":
		m.debug = !m.debug
		status := "disabled"
		if m.debug {
			status = "enabled"
		}
		e.writeLogLine(resultStyle.Render(fmt.Sprintf("Debug mode %s.", status)))

	case "env":
		m.envCommand(e, args)

	case "version":
		e.writeLogLine(resultStyle.Render("Dang REPL v0.1.0"))
		if len(m.importConfigs) > 0 {
			var names []string
			for _, ic := range m.importConfigs {
				names = append(names, ic.Name)
			}
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("Imports: %s", strings.Join(names, ", "))))
		} else {
			e.writeLogLine(dimStyle.Render("No imports configured (create a dang.toml)"))
		}

	case "type":
		m.typeCommand(e, args)

	case "find", "search":
		m.findCommand(e, args)

	case "history":
		e.writeLogLine("Recent history:")
		start := 0
		if len(m.history) > 20 {
			start = len(m.history) - 20
		}
		for i := start; i < len(m.history); i++ {
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %d: %s", i+1, m.history[i])))
		}

	case "doc":
		db := newDocBrowser(m.typeEnv, m.width, m.height)
		m.docBrowser = &db

	default:
		e.writeLogLine(errorStyle.Render(fmt.Sprintf("unknown command: %s (type :help for available commands)", cmd)))
	}
}

func (m *replModel) envCommand(e *replEntry, args []string) {
	filter := ""
	showAll := false
	if len(args) > 0 {
		if args[0] == "all" {
			showAll = true
		} else {
			filter = args[0]
		}
	}

	e.writeLogLine("Current environment bindings:")
	e.writeLogLine("")

	count := 0
	for name, scheme := range m.typeEnv.Bindings(dang.PublicVisibility) {
		if filter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(filter)) {
			continue
		}
		if !showAll && count >= 20 {
			e.writeLogLine(dimStyle.Render("  ... use ':env all' to see all"))
			break
		}
		t, _ := scheme.Type()
		if t != nil {
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s : %s", name, t)))
		} else {
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s", name)))
		}
		count++
	}
	e.writeLogLine("")
	e.writeLogLine(dimStyle.Render("Use ':doc' for interactive API browsing"))
}

func (m *replModel) typeCommand(e *replEntry, args []string) {
	if len(args) == 0 {
		e.writeLogLine(dimStyle.Render("Usage: :type <expression>"))
		return
	}

	expr := strings.Join(args, " ")

	result, err := dang.Parse("type-check", []byte(expr))
	if err != nil {
		e.writeLogLine(errorStyle.Render(fmt.Sprintf("parse error: %v", err)))
		return
	}

	node := result.(*dang.Block)

	inferredType, err := dang.Infer(m.ctx, m.typeEnv, node, false)
	if err != nil {
		e.writeLogLine(errorStyle.Render(fmt.Sprintf("type error: %v", err)))
		return
	}

	e.writeLogLine(fmt.Sprintf("Expression: %s", expr))
	e.writeLogLine(resultStyle.Render(fmt.Sprintf("Type: %s", inferredType)))

	// Additional context for single symbols
	trimmed := strings.TrimSpace(expr)
	if !strings.Contains(trimmed, " ") {
		if scheme, found := m.typeEnv.SchemeOf(trimmed); found {
			if t, _ := scheme.Type(); t != nil {
				e.writeLogLine(dimStyle.Render(fmt.Sprintf("Scheme: %s", scheme)))
			}
		}
	}
}

func (m *replModel) findCommand(e *replEntry, args []string) {
	if len(args) == 0 {
		e.writeLogLine(dimStyle.Render("Usage: :find <pattern>"))
		return
	}

	pattern := strings.ToLower(args[0])
	e.writeLogLine(fmt.Sprintf("Searching for '%s'...", pattern))

	found := false

	// Search bindings
	for name, scheme := range m.typeEnv.Bindings(dang.PublicVisibility) {
		if strings.Contains(strings.ToLower(name), pattern) {
			t, _ := scheme.Type()
			if t != nil {
				e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s : %s", name, t)))
			} else {
				e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s", name)))
			}
			found = true
		}
	}

	// Search named types
	for name, env := range m.typeEnv.NamedTypes() {
		if strings.Contains(strings.ToLower(name), pattern) {
			doc := env.GetModuleDocString()
			if doc != "" {
				if len(doc) > 60 {
					doc = doc[:57] + "..."
				}
				e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s — %s", name, doc)))
			} else {
				e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s", name)))
			}
			found = true
		}
	}

	if !found {
		e.writeLogLine(dimStyle.Render(fmt.Sprintf("No matches found for '%s'", pattern)))
	}
}

// History management

func (m *replModel) addHistory(line string) {
	// Don't add duplicates of the last entry
	if len(m.history) > 0 && m.history[len(m.history)-1] == line {
		m.historyIndex = -1
		return
	}
	m.history = append(m.history, line)
	m.historyIndex = -1
	m.saveHistory()
}

func (m *replModel) navigateHistory(direction int) {
	if len(m.history) == 0 {
		return
	}

	if direction < 0 {
		// Going back in history
		if m.historyIndex == -1 {
			m.historyIndex = len(m.history) - 1
		} else if m.historyIndex > 0 {
			m.historyIndex--
		}
	} else {
		// Going forward in history
		if m.historyIndex == -1 {
			return
		}
		m.historyIndex++
		if m.historyIndex >= len(m.history) {
			m.historyIndex = -1
			m.textInput.SetValue("")
			return
		}
	}

	if m.historyIndex >= 0 && m.historyIndex < len(m.history) {
		m.textInput.SetValue(m.history[m.historyIndex])
		m.textInput.CursorEnd()
	}
}

func (m *replModel) loadHistory() {
	data, err := os.ReadFile(m.historyFile)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			m.history = append(m.history, line)
		}
	}
}

func (m *replModel) saveHistory() {
	// Keep last 1000 entries
	entries := m.history
	if len(entries) > 1000 {
		entries = entries[len(entries)-1000:]
	}
	data := strings.Join(entries, "\n") + "\n"
	_ = os.WriteFile(m.historyFile, []byte(data), 0644)
}

// replCommands returns the list of REPL command names.
func replCommands() []string {
	return []string{
		"help", "exit", "quit", "clear", "reset", "debug",
		"env", "version", "schema", "type", "inspect", "find", "history", "doc",
	}
}

// runREPLBubbletea runs the REPL using Bubbletea v2.
// buildEnvFromImports creates type and eval environments from import configs,
// mimicking what ImportDecl.Infer/Eval does for each configured import.
func buildEnvFromImports(configs []dang.ImportConfig) (dang.Env, dang.EvalEnv) {
	typeEnv := dang.NewPreludeEnv()

	for _, config := range configs {
		if config.Schema == nil {
			continue
		}

		// Create a schema module (same as ImportDecl.Infer)
		schemaModule := dang.NewEnv(config.Schema)

		// Register as a named type so it can be accessed qualified (e.g. Dagger.container)
		typeEnv.AddClass(config.Name, schemaModule)

		// Add as a binding so the type checker can find it
		typeEnv.Add(config.Name, hm.NewScheme(nil, dang.NonNull(schemaModule)))
		typeEnv.SetVisibility(config.Name, dang.PublicVisibility)

		// Import all symbols unqualified into the top-level env
		for name, scheme := range schemaModule.Bindings(dang.PublicVisibility) {
			if name == config.Name {
				continue
			}
			if _, exists := typeEnv.LocalSchemeOf(name); exists {
				continue // don't shadow existing bindings
			}
			typeEnv.Add(name, scheme)
			typeEnv.SetVisibility(name, dang.PublicVisibility)
		}

		// Import named types too
		for name, namedEnv := range schemaModule.NamedTypes() {
			if name == config.Name {
				continue
			}
			if _, exists := typeEnv.NamedType(name); exists {
				continue
			}
			typeEnv.AddClass(name, namedEnv)
		}
	}

	evalEnv := dang.NewEvalEnv(typeEnv)

	// Set up eval env with schema modules for each import
	for _, config := range configs {
		if config.Schema == nil {
			continue
		}
		// Create module eval env with the GraphQL client wired up
		schemaModule := dang.NewEnv(config.Schema)
		moduleEnv := dang.NewEvalEnvWithSchema(schemaModule, config.Client, config.Schema)

		// Set the module as a named value (for qualified access: Dagger.container)
		evalEnv.Set(config.Name, moduleEnv)

		// Import all runtime values unqualified (for: container())
		for _, binding := range moduleEnv.Bindings(dang.PublicVisibility) {
			if binding.Key == config.Name {
				continue
			}
			if _, exists := evalEnv.GetLocal(binding.Key); exists {
				continue
			}
			evalEnv.Set(binding.Key, binding.Value)
		}
	}

	return typeEnv, evalEnv
}

func runREPLBubbletea(ctx context.Context, importConfigs []dang.ImportConfig, debug bool, daggerLog *syncWriter) error {
	m := newREPLModel(ctx, importConfigs, debug)
	p := tea.NewProgram(m)
	daggerLog.SetProgram(p)
	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("REPL error: %w", err)
	}

	final := finalModel.(replModel)
	if final.quitting {
		fmt.Println("Goodbye!")
	}
	return nil
}
