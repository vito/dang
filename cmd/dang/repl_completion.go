package main

import (
	"strings"

	"github.com/vito/dang/pkg/repl"
	"github.com/vito/tuist"
)

func (r *replComponent) buildCompletionProvider() tuist.CompletionProvider {
	base := repl.NewCompletionProvider(r.ctx, r.typeEnv, r.completions)
	return func(input string, cursorPos int) tuist.CompletionResult {
		if input == "" {
			return tuist.CompletionResult{}
		}
		if strings.HasPrefix(input, ":") {
			return r.commandCompletions(input)
		}
		return base(input, cursorPos)
	}
}

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

func (r *replComponent) buildDetailRenderer() tuist.DetailRenderer {
	return repl.NewDetailRenderer(r.ctx, r.typeEnv, func() string {
		return r.textInput.Value()
	})
}

func (r *replComponent) buildCompletions() []string {
	completions := repl.BuildStaticCompletions(r.typeEnv)
	for _, cmd := range r.commands {
		completions = append(completions, ":"+cmd.name)
	}
	return completions
}

func (r *replComponent) refreshCompletions() {
	r.completions = r.buildCompletions()
}
