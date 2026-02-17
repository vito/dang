package main

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
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

func (e *replEntry) writeLog(s string)     { e.logs.WriteString(s) }
func (e *replEntry) writeLogLine(s string)  { e.logs.WriteString(s); e.logs.WriteByte('\n') }
func (e *replEntry) writeResult(s string)   { e.result.WriteString(s); e.result.WriteByte('\n') }

// Styles (shared between REPL and doc browser)
var (
	promptStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	resultStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	menuStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("237"))
	menuSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("63")).Bold(true)
	menuBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63"))
	detailBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("241"))
	detailTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("255")).Bold(true)
	welcomeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

// buildCompletionList builds the full list of completions from the environment.
func buildCompletionList(typeEnv dang.Env) []string {
	seen := map[string]bool{}
	var completions []string

	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			completions = append(completions, name)
		}
	}

	for _, cmd := range replCommands() {
		add(":" + cmd)
	}

	keywords := []string{
		"let", "if", "else", "for", "in", "true", "false", "null",
		"self", "type", "pub", "new", "import", "assert", "try",
		"catch", "raise", "print",
	}
	for _, kw := range keywords {
		add(kw)
	}

	for name, scheme := range typeEnv.Bindings(dang.PublicVisibility) {
		if dang.IsTypeDefBinding(scheme) || dang.IsIDTypeName(name) {
			continue
		}
		add(name)
	}

	sort.Strings(completions)
	return completions
}

// replCommands returns the list of REPL command names.
func replCommands() []string {
	return []string{
		"help", "exit", "quit", "clear", "reset", "debug",
		"env", "version", "schema", "type", "inspect", "find", "history", "doc",
	}
}

// Completion helpers

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

func lastIdent(s string) string {
	i := len(s) - 1
	for i >= 0 && isIdentByte(s[i]) {
		i--
	}
	return s[i+1:]
}

// buildEnvFromImports creates type and eval environments from import configs.
func buildEnvFromImports(configs []dang.ImportConfig) (dang.Env, dang.EvalEnv) {
	typeEnv := dang.NewPreludeEnv()

	for _, config := range configs {
		if config.Schema == nil {
			continue
		}

		schemaModule := dang.NewEnv(config.Schema)
		typeEnv.AddClass(config.Name, schemaModule)
		typeEnv.Add(config.Name, hm.NewScheme(nil, dang.NonNull(schemaModule)))
		typeEnv.SetVisibility(config.Name, dang.PublicVisibility)

		for name, scheme := range schemaModule.Bindings(dang.PublicVisibility) {
			if name == config.Name {
				continue
			}
			if _, exists := typeEnv.LocalSchemeOf(name); exists {
				continue
			}
			typeEnv.Add(name, scheme)
			typeEnv.SetVisibility(name, dang.PublicVisibility)
		}

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

	for _, config := range configs {
		if config.Schema == nil {
			continue
		}
		schemaModule := dang.NewEnv(config.Schema)
		moduleEnv := dang.NewEvalEnvWithSchema(schemaModule, config.Client, config.Schema)
		evalEnv.Set(config.Name, moduleEnv)
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


