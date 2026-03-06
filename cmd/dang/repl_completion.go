package main

import (
	"strings"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/tuist"
)

// buildCompletionProvider returns a tuist.CompletionProvider that wraps
// dang.Complete, command completions, and static fallback completions.
func (r *replComponent) buildCompletionProvider() tuist.CompletionProvider {
	base := dang.NewCompletionProvider(r.ctx, r.typeEnv, r.completions)
	return func(input string, cursorPos int) tuist.CompletionResult {
		if input == "" {
			return tuist.CompletionResult{}
		}

		// Command completions (":help", ":reset", etc.) — REPL-specific.
		if strings.HasPrefix(input, ":") {
			return r.commandCompletions(input)
		}

		return base(input, cursorPos)
	}
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
		ReplaceFrom: 0,
	}
}

// buildDetailRenderer returns a DetailRenderer that resolves rich docItems
// from the dang type environment.
func (r *replComponent) buildDetailRenderer() tuist.DetailRenderer {
	return dang.NewDetailRenderer(r.ctx, r.typeEnv, func() string {
		return r.textInput.Value()
	})
}

func (r *replComponent) buildCompletions() []string {
	completions := dang.BuildStaticCompletions(r.typeEnv)
	// Prepend REPL commands.
	for _, cmd := range r.commands {
		completions = append(completions, ":"+cmd.name)
	}
	return completions
}

func (r *replComponent) refreshCompletions() {
	r.completions = r.buildCompletions()
}
