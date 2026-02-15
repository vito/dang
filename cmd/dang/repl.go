package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"charm.land/bubbles/v2/spinner"
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
	textInput     textinput.Model
	pendingOutput []string // lines to flush via tea.Println
	width         int
	height        int
	quitting      bool
	clearScreen   bool

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

	// History
	history      []string
	historyIndex int
	historyFile  string

	// Context for evaluation
	ctx context.Context
}

// evalResultMsg is sent when a background evaluation completes.
type evalResultMsg struct {
	output []string // rendered lines to print
}

// evalCancelledMsg is sent when an evaluation is cancelled.
type evalCancelledMsg struct{}

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

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))

	m := replModel{
		schema:         schema,
		client:         client,
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

	m.completions = m.buildCompletions()
	m.textInput.SetSuggestions(m.completions)

	// Load history
	m.loadHistory()

	return m
}

func (m replModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		tea.Println(welcomeStyle.Render("Welcome to Dang REPL v0.1.0")),
		tea.Println(dimStyle.Render(fmt.Sprintf("Connected to GraphQL API with %d types", len(m.schema.Types)))),
		tea.Println(""),
		tea.Println(dimStyle.Render("Type :help for commands, Tab for completion, Ctrl+C to exit")),
		tea.Println(""),
	)
}

func (m replModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textInput.SetWidth(msg.Width - lipgloss.Width(promptStyle.Render("dang> ")) - 1)
		return m, nil

	case evalResultMsg:
		m.evaluating = false
		m.cancelEval = nil
		// Refresh completions since env may have changed
		m.refreshCompletions()
		// Print result lines above the prompt
		var cmds []tea.Cmd
		for _, line := range msg.output {
			cmds = append(cmds, tea.Println(line))
		}
		return m, tea.Batch(cmds...)

	case evalCancelledMsg:
		m.evaluating = false
		m.cancelEval = nil
		return m, tea.Println(errorStyle.Render("cancelled"))

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

			// Echo the input above the prompt
			echoCmd := tea.Println(promptStyle.Render("dang> ") + line)

			// Clear input immediately
			m.textInput.SetValue("")
			m.menuVisible = false

			// Commands run synchronously (they're fast)
			if strings.HasPrefix(line, ":") {
				m.handleCommand(line[1:])
				flushCmd := m.flushOutput()
				if m.quitting {
					return m, tea.Batch(echoCmd, flushCmd, tea.Quit)
				}
				if m.clearScreen {
					m.clearScreen = false
					return m, func() tea.Msg { return tea.ClearScreen() }
				}
				m.updateCompletionMenu()
				return m, tea.Batch(echoCmd, flushCmd)
			}

			// Expressions run asynchronously with a spinner
			evalCmd := m.startEval(line)
			// startEval may produce synchronous errors (parse/type)
			flushCmd := m.flushOutput()
			return m, tea.Batch(echoCmd, flushCmd, evalCmd)

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
			return m, func() tea.Msg { return tea.ClearScreen() }
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
	var b strings.Builder

	// Input line or spinner
	if m.evaluating {
		b.WriteString(m.spinner.View() + dimStyle.Render("Evaluating... (Ctrl+C to cancel)"))
	} else {
		b.WriteString(m.textInput.View())

		// Completion menu below input
		if m.menuVisible && len(m.menuItems) > 0 {
			b.WriteString("\n")
			b.WriteString(m.renderMenu())
		}
	}

	v := tea.NewView(b.String())
	if !m.evaluating {
		v.Cursor = m.textInput.Cursor()
	}
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

// isIDType returns true for GraphQL ID scalar types (e.g. "ContainerID",
// "DirectoryID") which are internal plumbing and not useful to complete.
func isIDType(name string) bool {
	return len(name) > 2 && strings.HasSuffix(name, "ID")
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

	// GraphQL schema types (exclude ID scalar types)
	for _, t := range m.schema.Types {
		if !isBuiltinType(t.Name) && !isIDType(t.Name) {
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

// startEval begins asynchronous evaluation of a Dang expression.
// Returns a tea.Cmd that runs the evaluation in a goroutine.
func (m *replModel) startEval(expr string) tea.Cmd {
	// Parse synchronously (fast) so we can show errors immediately
	result, err := dang.Parse("repl", []byte(expr))
	if err != nil {
		m.appendError(fmt.Sprintf("parse error: %v", err))
		return nil
	}

	forms := result.(*dang.ModuleBlock).Forms

	if m.debug {
		for _, node := range forms {
			m.appendOutput(fmt.Sprintf("%# v", pretty.Formatter(node)), dimStyle)
		}
	}

	// Type inference synchronously (fast)
	fresh := hm.NewSimpleFresher()
	_, err = dang.InferFormsWithPhases(m.ctx, forms, m.typeEnv, fresh)
	if err != nil {
		m.appendError(fmt.Sprintf("type error: %v", err))
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
		var output []string

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

			// Flush any captured stdout
			if stdoutBuf.Len() > 0 {
				for _, line := range strings.Split(strings.TrimRight(stdoutBuf.String(), "\n"), "\n") {
					output = append(output, line)
				}
				stdoutBuf.Reset()
			}

			if err != nil {
				output = append(output, errorStyle.Render(fmt.Sprintf("evaluation error: %v", err)))
				return evalResultMsg{output: output}
			}

			output = append(output, resultStyle.Render(fmt.Sprintf("=> %s", val.String())))

			if debug {
				output = append(output, dimStyle.Render(fmt.Sprintf("%# v", pretty.Formatter(val))))
			}
		}

		return evalResultMsg{output: output}
	})
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
		m.clearScreen = true

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

// appendOutput adds a styled line to the pending output buffer.
func (m *replModel) appendOutput(text string, style lipgloss.Style) {
	m.pendingOutput = append(m.pendingOutput, style.Render(text))
}

// appendError adds an error line to the pending output buffer.
func (m *replModel) appendError(text string) {
	m.pendingOutput = append(m.pendingOutput, errorStyle.Render(text))
}

// flushOutput returns a tea.Cmd that prints all pending output lines above
// the Bubbletea-managed area, then clears the buffer.
func (m *replModel) flushOutput() tea.Cmd {
	if len(m.pendingOutput) == 0 {
		return nil
	}
	var cmds []tea.Cmd
	for _, line := range m.pendingOutput {
		cmds = append(cmds, tea.Println(line))
	}
	m.pendingOutput = nil
	return tea.Batch(cmds...)
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


