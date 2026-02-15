package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Khan/genqlient/graphql"
	"github.com/kr/pretty"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
	"github.com/vito/dang/pkg/ioctx"
)

// replModel is the Bubbletea model for the Dang REPL.
type replModel struct {
	// Dang state
	schema  *introspection.Schema
	client  graphql.Client
	debug   bool
	typeEnv dang.Env
	evalEnv dang.EvalEnv

	// UI state
	textInput textinput.Model
	output    []styledLine // scrollback buffer
	width     int
	height    int
	quitting  bool

	// Completion state
	completions     []string // all available completions
	menuVisible     bool     // whether the completion menu is shown
	menuItems       []string // current filtered menu items
	menuIndex       int      // selected item in menu
	menuMaxVisible  int      // max items shown at once

	// History
	history      []string
	historyIndex int
	historyFile  string

	// Context for evaluation
	ctx context.Context
}

// styledLine holds a line of output with optional styling.
type styledLine struct {
	text  string
	style lipgloss.Style
}

// Styles
var (
	promptStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	resultStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	menuStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("237"))
	menuSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("63")).Bold(true)
	hintStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	welcomeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	dimStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

func newREPLModel(ctx context.Context, schema *introspection.Schema, client graphql.Client, debug bool) replModel {
	typeEnv := dang.NewEnv(schema)
	evalEnv := dang.NewEvalEnvWithSchema(typeEnv, client, schema)

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

	m := replModel{
		schema:         schema,
		client:         client,
		debug:          debug,
		typeEnv:        typeEnv,
		evalEnv:        evalEnv,
		textInput:      ti,
		menuMaxVisible: 8,
		historyIndex:   -1,
		historyFile:    "/tmp/dang_history",
		ctx:            ctx,
	}

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
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textInput.SetWidth(msg.Width - lipgloss.Width(promptStyle.Render("dang> ")) - 1)
		return m, nil

	case tea.KeyPressMsg:
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

			// Show the input in output
			m.appendOutput(promptStyle.Render("dang> ")+line, lipgloss.NewStyle())

			// Process the line
			m.processLine(line)

			// Check if a command requested quit
			if m.quitting {
				return m, tea.Quit
			}

			// Clear input
			m.textInput.SetValue("")
			m.menuVisible = false
			m.updateCompletionMenu()
			return m, nil

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
			m.output = nil
			return m, nil
		}
	}

	// Update text input
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)

	// Update completion menu based on current input
	m.updateCompletionMenu()

	return m, cmd
}

func (m replModel) View() tea.View {
	var b strings.Builder

	// Calculate available height for output
	// Reserve: 1 for input, 1 for status bar, menu height
	menuHeight := 0
	if m.menuVisible {
		menuHeight = min(len(m.menuItems), m.menuMaxVisible) + 1 // +1 for border
	}
	availHeight := m.height - 2 - menuHeight
	if availHeight < 0 {
		availHeight = 0
	}

	// Welcome message if no output yet
	if len(m.output) == 0 {
		welcome := []string{
			welcomeStyle.Render("Welcome to Dang REPL v0.1.0"),
			dimStyle.Render(fmt.Sprintf("Connected to GraphQL API with %d types", len(m.schema.Types))),
			"",
			dimStyle.Render("Type :help for commands, Tab for completion, Ctrl+C to exit"),
			"",
		}
		for _, line := range welcome {
			b.WriteString(line + "\n")
		}
		availHeight -= len(welcome)
	}

	// Show output (scrolled to bottom)
	startIdx := 0
	if len(m.output) > availHeight {
		startIdx = len(m.output) - availHeight
	}
	for i := startIdx; i < len(m.output); i++ {
		line := m.output[i]
		b.WriteString(line.style.Render(line.text) + "\n")
	}

	// Pad remaining space
	outputLines := len(m.output) - startIdx
	if len(m.output) == 0 {
		outputLines = 0 // welcome takes care of padding
	}
	for i := outputLines; i < availHeight; i++ {
		b.WriteString("\n")
	}

	// Input line
	b.WriteString(m.textInput.View())

	// Completion menu below input
	if m.menuVisible && len(m.menuItems) > 0 {
		b.WriteString("\n")
		b.WriteString(m.renderMenu())
	}

	v := tea.NewView(b.String())
	c := m.textInput.Cursor()
	if c != nil {
		// Adjust cursor Y for output lines above
		outputLineCount := 0
		if len(m.output) == 0 {
			outputLineCount = 5 // welcome message lines
		} else if len(m.output) > availHeight {
			outputLineCount = availHeight
		} else {
			outputLineCount = len(m.output)
		}
		// Add padding lines
		if len(m.output) > 0 {
			outputLineCount += max(0, availHeight-len(m.output)+startIdx)
		}
		c.Y += outputLineCount
	}
	v.Cursor = c
	return v
}

// renderMenu renders the completion dropdown menu.
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

	var lines []string
	for i := start; i < end && i < len(m.menuItems); i++ {
		item := m.menuItems[i]
		// Truncate long items
		maxWidth := 60
		if len(item) > maxWidth {
			item = item[:maxWidth-3] + "..."
		}
		// Pad to consistent width
		padded := fmt.Sprintf(" %-*s ", maxWidth, item)

		if i == m.menuIndex {
			lines = append(lines, menuSelectedStyle.Render(padded))
		} else {
			lines = append(lines, menuStyle.Render(padded))
		}
	}

	// Show scroll indicator
	if len(m.menuItems) > visible {
		info := fmt.Sprintf(" %d/%d ", m.menuIndex+1, len(m.menuItems))
		lines = append(lines, dimStyle.Render(info))
	}

	return strings.Join(lines, "\n")
}

// updateCompletionMenu updates the completion menu based on current input.
func (m *replModel) updateCompletionMenu() {
	val := m.textInput.Value()

	// Don't show menu for commands or empty input
	if val == "" || strings.HasPrefix(val, ":") {
		m.menuVisible = false
		m.menuItems = nil
		return
	}

	// Find the current word being typed (last token)
	word := lastWord(val)
	if word == "" {
		m.menuVisible = false
		m.menuItems = nil
		return
	}

	// Filter completions
	var matches []string
	wordLower := strings.ToLower(word)
	for _, c := range m.completions {
		if strings.HasPrefix(strings.ToLower(c), wordLower) && strings.ToLower(c) != wordLower {
			matches = append(matches, c)
		}
	}

	if len(matches) <= 1 {
		// 0 or 1 match: just use inline hint, no menu
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

// lastWord extracts the last word/identifier from the input for completion.
func lastWord(s string) string {
	// Walk backwards to find the start of the current identifier
	i := len(s) - 1
	for i >= 0 && (isIdentChar(s[i]) || s[i] == '.') {
		i--
	}
	return s[i+1:]
}

func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
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

	// Environment bindings (from type env)
	for name := range m.typeEnv.Bindings(dang.PublicVisibility) {
		add(name)
	}

	// GraphQL schema types
	for _, t := range m.schema.Types {
		if !isBuiltinType(t.Name) {
			add(t.Name)
		}
	}

	// Query type fields (top-level functions)
	if m.schema.QueryType.Name != "" {
		queryType := findType(m.schema, m.schema.QueryType.Name)
		if queryType != nil {
			for _, field := range queryType.Fields {
				add(field.Name)
			}
		}
	}

	sort.Strings(completions)
	return completions
}

// refreshCompletions rebuilds completions (called after env changes).
func (m *replModel) refreshCompletions() {
	m.completions = m.buildCompletions()
	m.textInput.SetSuggestions(m.completions)
}

// processLine processes a line of REPL input.
func (m *replModel) processLine(line string) {
	if strings.HasPrefix(line, ":") {
		m.handleCommand(line[1:])
		return
	}
	m.evaluateExpression(line)
}

// evaluateExpression evaluates a Dang expression.
func (m *replModel) evaluateExpression(expr string) {
	// Parse the expression
	result, err := dang.Parse("repl", []byte(expr))
	if err != nil {
		m.appendError(fmt.Sprintf("parse error: %v", err))
		return
	}

	forms := result.(*dang.ModuleBlock).Forms

	if m.debug {
		for _, node := range forms {
			m.appendOutput(fmt.Sprintf("%# v", pretty.Formatter(node)), dimStyle)
		}
	}

	// Type inference
	fresh := hm.NewSimpleFresher()
	_, err = dang.InferFormsWithPhases(m.ctx, forms, m.typeEnv, fresh)
	if err != nil {
		m.appendError(fmt.Sprintf("type error: %v", err))
		return
	}

	// Capture stdout from print() calls
	var stdoutBuf bytes.Buffer
	evalCtx := ioctx.StdoutToContext(m.ctx, &stdoutBuf)
	evalCtx = ioctx.StderrToContext(evalCtx, &stdoutBuf)

	for _, node := range forms {
		val, err := dang.EvalNode(evalCtx, m.evalEnv, node)

		// Flush any captured stdout
		if stdoutBuf.Len() > 0 {
			for _, line := range strings.Split(strings.TrimRight(stdoutBuf.String(), "\n"), "\n") {
				m.appendOutput(line, lipgloss.NewStyle())
			}
			stdoutBuf.Reset()
		}

		if err != nil {
			m.appendError(fmt.Sprintf("evaluation error: %v", err))
			return
		}
		m.appendOutput(fmt.Sprintf("=> %s", val.String()), resultStyle)

		if m.debug {
			m.appendOutput(fmt.Sprintf("%# v", pretty.Formatter(val)), dimStyle)
		}
	}

	// Refresh completions since env may have changed
	m.refreshCompletions()
}

// handleCommand handles REPL :commands.
func (m *replModel) handleCommand(cmdLine string) {
	parts := strings.Fields(cmdLine)
	if len(parts) == 0 {
		m.appendError("empty command")
		return
	}

	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "help":
		m.appendOutput("Available commands:", lipgloss.NewStyle())
		m.appendOutput("  :help      - Show this help message", dimStyle)
		m.appendOutput("  :exit      - Exit the REPL", dimStyle)
		m.appendOutput("  :quit      - Exit the REPL", dimStyle)
		m.appendOutput("  :clear     - Clear the screen", dimStyle)
		m.appendOutput("  :reset     - Reset the evaluation environment", dimStyle)
		m.appendOutput("  :debug     - Toggle debug mode", dimStyle)
		m.appendOutput("  :env       - Show current environment bindings", dimStyle)
		m.appendOutput("  :version   - Show version information", dimStyle)
		m.appendOutput("  :schema    - Show GraphQL schema information", dimStyle)
		m.appendOutput("  :type      - Show type of an expression", dimStyle)
		m.appendOutput("  :inspect   - Inspect a GraphQL type", dimStyle)
		m.appendOutput("  :find      - Find functions/types by pattern", dimStyle)
		m.appendOutput("", lipgloss.NewStyle())
		m.appendOutput("Type Dang expressions to evaluate them.", dimStyle)
		m.appendOutput("Tab for completion, ↑/↓ for history, Ctrl+L to clear.", dimStyle)

	case "exit", "quit":
		m.quitting = true

	case "clear":
		m.output = nil

	case "reset":
		m.typeEnv = dang.NewEnv(m.schema)
		m.evalEnv = dang.NewEvalEnvWithSchema(m.typeEnv, m.client, m.schema)
		m.refreshCompletions()
		m.appendOutput("Environment reset.", resultStyle)

	case "debug":
		m.debug = !m.debug
		status := "disabled"
		if m.debug {
			status = "enabled"
		}
		m.appendOutput(fmt.Sprintf("Debug mode %s.", status), resultStyle)

	case "env":
		m.envCommand(args)

	case "version":
		m.appendOutput("Dang REPL v0.1.0", resultStyle)
		m.appendOutput(fmt.Sprintf("Connected to GraphQL API with %d types", len(m.schema.Types)), dimStyle)

	case "schema":
		m.schemaCommand(args)

	case "type":
		m.typeCommand(args)

	case "inspect":
		if len(args) == 0 {
			m.appendOutput("Usage: :inspect <type-name>", dimStyle)
			return
		}
		m.inspectTypeByName(args[0])

	case "find":
		m.findCommand(args)

	case "history":
		m.appendOutput("Recent history:", lipgloss.NewStyle())
		start := 0
		if len(m.history) > 20 {
			start = len(m.history) - 20
		}
		for i := start; i < len(m.history); i++ {
			m.appendOutput(fmt.Sprintf("  %d: %s", i+1, m.history[i]), dimStyle)
		}

	default:
		m.appendError(fmt.Sprintf("unknown command: %s (type :help for available commands)", cmd))
	}
}

func (m *replModel) envCommand(args []string) {
	filter := ""
	showAll := false
	if len(args) > 0 {
		if args[0] == "all" {
			showAll = true
		} else {
			filter = args[0]
		}
	}

	m.appendOutput("Current environment bindings:", lipgloss.NewStyle())
	m.appendOutput(fmt.Sprintf("Connected to GraphQL API with %d types", len(m.schema.Types)), dimStyle)
	m.appendOutput("", lipgloss.NewStyle())

	m.appendOutput("Built-in functions:", lipgloss.NewStyle())
	m.appendOutput("  print(value: a) -> Null", dimStyle)
	m.appendOutput("", lipgloss.NewStyle())

	if m.schema.QueryType.Name != "" {
		queryType := findType(m.schema, m.schema.QueryType.Name)
		if queryType != nil && len(queryType.Fields) > 0 {
			m.appendOutput("Global functions (Query type):", lipgloss.NewStyle())
			count := 0
			for _, field := range queryType.Fields {
				if filter != "" && !strings.Contains(strings.ToLower(field.Name), strings.ToLower(filter)) {
					continue
				}
				if !showAll && count >= 10 {
					m.appendOutput(fmt.Sprintf("  ... and %d more (use ':env all' to see all)", len(queryType.Fields)-count), dimStyle)
					break
				}
				m.appendOutput(fmt.Sprintf("  %s", formatFieldSignature(field)), dimStyle)
				count++
			}
			m.appendOutput("", lipgloss.NewStyle())
		}
	}
}

func (m *replModel) schemaCommand(args []string) {
	if len(args) > 0 {
		m.inspectTypeByName(args[0])
		return
	}

	m.appendOutput("GraphQL Schema Overview:", lipgloss.NewStyle())
	m.appendOutput(fmt.Sprintf("Query Type: %s", m.schema.QueryType.Name), dimStyle)
	if m.schema.Mutation() != nil && m.schema.Mutation().Name != "" {
		m.appendOutput(fmt.Sprintf("Mutation Type: %s", m.schema.Mutation().Name), dimStyle)
	}
	m.appendOutput(fmt.Sprintf("Total Types: %d", len(m.schema.Types)), dimStyle)
	m.appendOutput("", lipgloss.NewStyle())

	objects, _, enums, scalars, _ := categorizeTypes(m.schema.Types)

	m.appendOutput(fmt.Sprintf("Object Types (%d):", len(objects)), lipgloss.NewStyle())
	for i, t := range objects {
		if i >= 10 {
			m.appendOutput(fmt.Sprintf("  ... and %d more", len(objects)-10), dimStyle)
			break
		}
		m.appendOutput(fmt.Sprintf("  %s (%d fields)", t.Name, len(t.Fields)), dimStyle)
	}

	if len(enums) > 0 {
		m.appendOutput(fmt.Sprintf("\nEnum Types (%d):", len(enums)), lipgloss.NewStyle())
		for i, t := range enums {
			if i >= 5 {
				m.appendOutput(fmt.Sprintf("  ... and %d more", len(enums)-5), dimStyle)
				break
			}
			m.appendOutput(fmt.Sprintf("  %s", t.Name), dimStyle)
		}
	}

	if len(scalars) > 0 {
		m.appendOutput(fmt.Sprintf("\nScalar Types (%d):", len(scalars)), lipgloss.NewStyle())
		for _, t := range scalars {
			if !isBuiltinType(t.Name) {
				m.appendOutput(fmt.Sprintf("  %s", t.Name), dimStyle)
			}
		}
	}

	m.appendOutput("\nUse ':schema <type>' to inspect a specific type", dimStyle)
}

func (m *replModel) typeCommand(args []string) {
	if len(args) == 0 {
		m.appendOutput("Usage: :type <expression>", dimStyle)
		return
	}

	expr := strings.Join(args, " ")

	result, err := dang.Parse("type-check", []byte(expr))
	if err != nil {
		m.appendError(fmt.Sprintf("parse error: %v", err))
		return
	}

	node := result.(*dang.Block)

	inferredType, err := dang.Infer(m.ctx, m.typeEnv, node, false)
	if err != nil {
		m.appendError(fmt.Sprintf("type error: %v", err))
		return
	}

	m.appendOutput(fmt.Sprintf("Expression: %s", expr), lipgloss.NewStyle())
	m.appendOutput(fmt.Sprintf("Type: %s", inferredType), resultStyle)

	// Additional context for single symbols
	trimmed := strings.TrimSpace(expr)
	if !strings.Contains(trimmed, " ") {
		if scheme, found := m.typeEnv.SchemeOf(trimmed); found {
			if t, _ := scheme.Type(); t != nil {
				m.appendOutput(fmt.Sprintf("Scheme: %s", scheme), dimStyle)
			}
		}
	}
}

func (m *replModel) inspectTypeByName(typeName string) {
	gqlType := findType(m.schema, typeName)
	if gqlType == nil {
		m.appendError(fmt.Sprintf("type '%s' not found", typeName))
		return
	}

	m.appendOutput(fmt.Sprintf("Type: %s (%s)", gqlType.Name, gqlType.Kind), lipgloss.NewStyle())
	if gqlType.Description != "" {
		m.appendOutput(fmt.Sprintf("  %s", gqlType.Description), dimStyle)
	}

	if len(gqlType.Fields) > 0 {
		m.appendOutput(fmt.Sprintf("\nFields (%d):", len(gqlType.Fields)), lipgloss.NewStyle())
		for _, field := range gqlType.Fields {
			m.appendOutput(fmt.Sprintf("  %s", formatFieldSignature(field)), dimStyle)
			if field.Description != "" {
				// Truncate long descriptions
				desc := field.Description
				if len(desc) > 80 {
					desc = desc[:77] + "..."
				}
				m.appendOutput(fmt.Sprintf("    %s", desc), hintStyle)
			}
		}
	}

	if len(gqlType.EnumValues) > 0 {
		m.appendOutput(fmt.Sprintf("\nEnum Values (%d):", len(gqlType.EnumValues)), lipgloss.NewStyle())
		for _, val := range gqlType.EnumValues {
			m.appendOutput(fmt.Sprintf("  %s", val.Name), dimStyle)
		}
	}
}

func (m *replModel) findCommand(args []string) {
	if len(args) == 0 {
		m.appendOutput("Usage: :find <pattern>", dimStyle)
		return
	}

	pattern := strings.ToLower(args[0])
	m.appendOutput(fmt.Sprintf("Searching for '%s'...", pattern), lipgloss.NewStyle())

	found := false

	// Search in global functions
	if m.schema.QueryType.Name != "" {
		queryType := findType(m.schema, m.schema.QueryType.Name)
		if queryType != nil {
			var matches []string
			for _, field := range queryType.Fields {
				if strings.Contains(strings.ToLower(field.Name), pattern) {
					matches = append(matches, fmt.Sprintf("  %s", formatFieldSignature(field)))
				}
			}
			if len(matches) > 0 {
				m.appendOutput("\nGlobal Functions:", lipgloss.NewStyle())
				for _, match := range matches {
					m.appendOutput(match, dimStyle)
				}
				found = true
			}
		}
	}

	// Search in types
	var typeMatches []string
	for _, t := range m.schema.Types {
		if strings.Contains(strings.ToLower(t.Name), pattern) && !isBuiltinType(t.Name) {
			typeMatches = append(typeMatches, fmt.Sprintf("  %s (%s, %d fields)", t.Name, t.Kind, len(t.Fields)))
		}
	}
	if len(typeMatches) > 0 {
		m.appendOutput("\nTypes:", lipgloss.NewStyle())
		for _, match := range typeMatches {
			m.appendOutput(match, dimStyle)
		}
		found = true
	}

	// Search in type fields
	var fieldMatches []string
	for _, t := range m.schema.Types {
		if isBuiltinType(t.Name) {
			continue
		}
		for _, field := range t.Fields {
			if strings.Contains(strings.ToLower(field.Name), pattern) {
				fieldMatches = append(fieldMatches, fmt.Sprintf("  %s.%s", t.Name, formatFieldSignature(field)))
			}
		}
	}
	if len(fieldMatches) > 0 {
		m.appendOutput("\nType Fields:", lipgloss.NewStyle())
		for i, match := range fieldMatches {
			if i >= 20 {
				m.appendOutput(fmt.Sprintf("  ... and %d more matches", len(fieldMatches)-20), dimStyle)
				break
			}
			m.appendOutput(match, dimStyle)
		}
		found = true
	}

	if !found {
		m.appendOutput(fmt.Sprintf("No matches found for '%s'", pattern), dimStyle)
	}
}

// appendOutput adds a line to the output buffer.
func (m *replModel) appendOutput(text string, style lipgloss.Style) {
	m.output = append(m.output, styledLine{text: text, style: style})
}

// appendError adds an error line to the output buffer.
func (m *replModel) appendError(text string) {
	m.output = append(m.output, styledLine{text: text, style: errorStyle})
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
		"env", "version", "schema", "type", "inspect", "find", "history",
	}
}

// runREPLBubbletea runs the REPL using Bubbletea v2.
func runREPLBubbletea(ctx context.Context, schema *introspection.Schema, client graphql.Client, debug bool) error {
	m := newREPLModel(ctx, schema, client, debug)
	p := tea.NewProgram(m)
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


