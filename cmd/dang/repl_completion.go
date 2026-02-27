package main

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vito/dang/pkg/dang"
	"codeberg.org/vito/tuist"
)

// buildCompletionProvider returns a tuist.CompletionProvider that wraps
// dang.Complete, command completions, and static fallback completions.
func (r *replComponent) buildCompletionProvider() tuist.CompletionProvider {
	return func(input string, cursorPos int) tuist.CompletionResult {
		if input == "" {
			return tuist.CompletionResult{}
		}

		// Command completions (":help", ":reset", etc.)
		if strings.HasPrefix(input, ":") {
			return r.commandCompletions(input)
		}

		// Convert byte offset to line/col for the tree-sitter based
		// completion engine. For single-line input line=0, col=cursorPos.
		// For multi-line input (Alt+Enter), count newlines.
		line, col := byteOffsetToLineCol(input, cursorPos)

		// Type-aware completions via the unified tree-sitter engine.
		result := dang.Complete(r.ctx, r.typeEnv, input, line, col)
		if len(result.Items) > 0 {
			return r.dangCompletions(result)
		}

		// Fallback: static keyword/name completions.
		return r.staticCompletions(input)
	}
}

// byteOffsetToLineCol converts a byte offset in text to 0-based line/col.
func byteOffsetToLineCol(text string, offset int) (line, col int) {
	if offset > len(text) {
		offset = len(text)
	}
	for i := 0; i < offset; i++ {
		if text[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return line, col
}

// commandCompletions returns completions for REPL commands.
func (r *replComponent) commandCompletions(input string) tuist.CompletionResult {
	partial := strings.TrimPrefix(input, ":")
	partialLower := strings.ToLower(partial)

	var items []tuist.Completion
	for _, cmd := range r.commands {
		if strings.HasPrefix(cmd.name, partialLower) && cmd.name != partialLower {
			items = append(items, tuist.Completion{
				Label:         ":" + cmd.name,
				Detail:        "command",
				Documentation: cmd.desc,
				Kind:          "command",
			})
		}
	}
	return tuist.CompletionResult{
		Items:       items,
		ReplaceFrom: 0, // replace entire input
	}
}

// dangCompletions converts a dang.CompletionResult into a tuist.CompletionResult,
// applying filtering, sorting, and REPL-specific formatting.
func (r *replComponent) dangCompletions(result dang.CompletionResult) tuist.CompletionResult {
	isArgCompletion := len(result.Items) > 0 && result.Items[0].IsArg

	var items []tuist.Completion
	for _, c := range result.Items {
		item := tuist.Completion{
			Label:         c.Label,
			Detail:        c.Detail,
			Documentation: c.Documentation,
		}
		if c.IsFunction {
			item.Kind = "function"
		} else if c.IsArg {
			item.Kind = "arg"
			item.InsertText = c.Label + ": "
			item.DisplayLabel = c.Label + ": " + c.Detail
		}
		items = append(items, item)
	}

	// Sort exact-case matches before case-insensitive (skip for args).
	if !isArgCompletion && len(result.Items) > 0 {
		// Extract the partial from the first completion's context.
		// The partial is already filtered by Complete, so we just
		// need to sort by case match.
		partial := commonPrefix(result.Items)
		if partial != "" {
			items = sortByCase(items, partial)
		}
	}

	return tuist.CompletionResult{
		Items:       items,
		ReplaceFrom: result.ReplaceFrom,
	}
}

// commonPrefix finds the longest common lowercase prefix of completion labels.
// Used to determine the partial for case-sorting.
func commonPrefix(completions []dang.Completion) string {
	if len(completions) == 0 {
		return ""
	}
	prefix := strings.ToLower(completions[0].Label)
	for _, c := range completions[1:] {
		label := strings.ToLower(c.Label)
		for len(prefix) > 0 && !strings.HasPrefix(label, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

// staticCompletions returns fallback completions from the static keyword/name list.
func (r *replComponent) staticCompletions(input string) tuist.CompletionResult {
	word := lastIdent(input)
	if word == "" {
		return tuist.CompletionResult{}
	}

	replaceFrom := len(input) - len(word)
	wordLower := strings.ToLower(word)

	var exactCase, otherCase []tuist.Completion
	for _, c := range r.completions {
		cLower := strings.ToLower(c)
		if cLower == wordLower {
			continue
		}
		item := tuist.Completion{Label: c}
		if strings.HasPrefix(c, word) {
			exactCase = append(exactCase, item)
		} else if strings.HasPrefix(cLower, wordLower) {
			otherCase = append(otherCase, item)
		}
	}

	return tuist.CompletionResult{
		Items:       append(exactCase, otherCase...),
		ReplaceFrom: replaceFrom,
	}
}

// sortByCase sorts completions so exact-case-prefix matches come first.
func sortByCase(items []tuist.Completion, partial string) []tuist.Completion {
	var exact, other []tuist.Completion
	for _, item := range items {
		if strings.HasPrefix(item.Label, partial) {
			exact = append(exact, item)
		} else {
			other = append(other, item)
		}
	}
	return append(exact, other...)
}

// buildDetailRenderer returns a DetailRenderer that resolves rich docItems
// from the dang type environment.
func (r *replComponent) buildDetailRenderer() tuist.DetailRenderer {
	return func(c tuist.Completion, width int) []string {
		// Try to resolve a full docItem from the type environment.
		item, found := docItemFromEnv(r.typeEnv, c.Label)
		if !found {
			item, found = r.resolveCompletionDocItem(c)
		}
		if !found {
			if c.Detail == "" && c.Documentation == "" {
				return nil
			}
			item = docItem{
				name:    c.Label,
				typeStr: c.Detail,
				doc:     c.Documentation,
			}
		}

		return r.renderDetailLines(item, width)
	}
}

// resolveCompletionDocItem tries to resolve a member completion's docItem
// by inferring the receiver type from the current input.
func (r *replComponent) resolveCompletionDocItem(c tuist.Completion) (docItem, bool) {
	val := r.textInput.Value()
	dotIdx := -1
	for i := len(val) - 1; i >= 0; i-- {
		if val[i] == '.' {
			dotIdx = i
			break
		}
	}
	if dotIdx < 0 {
		return docItem{}, false
	}
	receiverText := val[:dotIdx]
	receiverType := dang.InferReceiverType(r.ctx, r.typeEnv, receiverText)
	if receiverType == nil {
		return docItem{}, false
	}
	unwrapped := unwrapType(receiverType)
	env, ok := unwrapped.(dang.Env)
	if !ok {
		return docItem{}, false
	}
	return docItemFromEnv(env, c.Label)
}

// renderDetailLines renders a docItem for the detail panel.
func (r *replComponent) renderDetailLines(item docItem, width int) []string {
	contentW := max(8, width-2)

	docTextStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("249"))
	argNameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	argTypeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	dimSt := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	titleLine := detailTitleStyle.Render(item.name)
	lines := []string{titleLine}
	lines = append(lines, renderDocDetail(item, contentW, docTextStyle, argNameStyle, argTypeStyle, dimSt)...)
	return lines
}

func (r *replComponent) buildCompletions() []string {
	return r.buildCompletionList(r.typeEnv)
}

func (r *replComponent) refreshCompletions() {
	r.completions = r.buildCompletions()
}

// buildCompletionList builds the full list of completions from the environment.
func (r *replComponent) buildCompletionList(typeEnv dang.Env) []string {
	seen := map[string]bool{}
	var completions []string

	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			completions = append(completions, name)
		}
	}

	for _, cmd := range r.commands {
		add(":" + cmd.name)
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
