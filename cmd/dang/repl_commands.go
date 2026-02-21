package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/pitui"
)

// ── commands ────────────────────────────────────────────────────────────────

func (r *replComponent) handleCommand(ctx pitui.EventContext, cmdLine string) {
	ev := r.activeEntryView()
	e := ev.entry

	parts := strings.Fields(cmdLine)
	if len(parts) == 0 {
		e.writeLogLine(errorStyle.Render("empty command"))
		ev.Update()
		return
	}

	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "help":
		e.writeLogLine("Available commands:")
		maxName := 0
		for _, cmd := range replCommandDefs {
			if len(cmd.name) > maxName {
				maxName = len(cmd.name)
			}
		}
		for _, cmd := range replCommandDefs {
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("  :%-*s - %s", maxName, cmd.name, cmd.desc)))
		}
		e.writeLogLine("")
		e.writeLogLine(dimStyle.Render("Type Dang expressions to evaluate them."))
		multilineHint := "Alt+Enter"
		if ctx.HasKittyKeyboard() {
			multilineHint = "Shift+Enter"
		}
		e.writeLogLine(dimStyle.Render(fmt.Sprintf("Tab for completion, Up/Down for history, %s for multiline, Ctrl+L to clear.", multilineHint)))

	case "exit", "quit":
		r.requestQuit()
		return

	case "clear":
		r.entryContainer.Clear()
		return

	case "reset":
		r.typeEnv, r.evalEnv = buildEnvFromImports(r.importConfigs)
		r.refreshCompletions()
		e.writeLogLine(resultStyle.Render("Environment reset."))

	case "debug":
		r.debug = !r.debug
		status := "disabled"
		if r.debug {
			status = "enabled"
		}
		e.writeLogLine(resultStyle.Render(fmt.Sprintf("Debug mode %s.", status)))

	case "debug-render":
		r.debugRender = !r.debugRender
		if r.debugRender {
			logPath := "/tmp/dang_render_debug.log"
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				e.writeLogLine(errorStyle.Render(fmt.Sprintf("failed to open debug log: %v", err)))
				r.debugRender = false
			} else {
				r.debugRenderFile = f
				ctx.SetDebugWriter(f)
				e.writeLogLine(resultStyle.Render(fmt.Sprintf("Render debug enabled. Logging to %s", logPath)))
				e.writeLogLine(dimStyle.Render("  Use 'tail -f " + logPath + "' in another terminal to watch."))
			}
		} else {
			ctx.SetDebugWriter(nil)
			if r.debugRenderFile != nil {
				r.debugRenderFile.Close()
				r.debugRenderFile = nil
			}
			e.writeLogLine(resultStyle.Render("Render debug disabled."))
		}

	case "env":
		r.envCommand(e, args)

	case "version":
		e.writeLogLine(resultStyle.Render("Dang REPL v0.1.0"))
		if len(r.importConfigs) > 0 {
			var names []string
			for _, ic := range r.importConfigs {
				names = append(names, ic.Name)
			}
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("Imports: %s", strings.Join(names, ", "))))
		} else {
			e.writeLogLine(dimStyle.Render("No imports configured (create a dang.toml)"))
		}

	case "type":
		r.typeCommand(e, args)

	case "find", "search":
		r.findCommand(e, args)

	case "history":
		e.writeLogLine("Recent history:")
		entries := r.history.entries
		start := 0
		if len(entries) > 20 {
			start = len(entries) - 20
		}
		for i := start; i < len(entries); i++ {
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %d: %s", i+1, entries[i])))
		}

	case "doc":
		ev.Update()
		r.showDocBrowser(ctx)
		return

	default:
		e.writeLogLine(errorStyle.Render(fmt.Sprintf("unknown command: %s (type :help for available commands)", cmd)))
	}
	ev.Update()
}

func (r *replComponent) envCommand(e *replEntry, args []string) {
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
	for name, scheme := range r.typeEnv.Bindings(dang.PublicVisibility) {
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

func (r *replComponent) typeCommand(e *replEntry, args []string) {
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
	inferredType, err := dang.Infer(r.ctx, r.typeEnv, node, false)
	if err != nil {
		e.writeLogLine(errorStyle.Render(fmt.Sprintf("type error: %v", err)))
		return
	}
	e.writeLogLine(fmt.Sprintf("Expression: %s", expr))
	e.writeLogLine(resultStyle.Render(fmt.Sprintf("Type: %s", inferredType)))
	trimmed := strings.TrimSpace(expr)
	if !strings.Contains(trimmed, " ") {
		if scheme, found := r.typeEnv.SchemeOf(trimmed); found {
			if t, _ := scheme.Type(); t != nil {
				e.writeLogLine(dimStyle.Render(fmt.Sprintf("Scheme: %s", scheme)))
			}
		}
	}
}

func (r *replComponent) findCommand(e *replEntry, args []string) {
	if len(args) == 0 {
		e.writeLogLine(dimStyle.Render("Usage: :find <pattern>"))
		return
	}
	pattern := strings.ToLower(args[0])
	e.writeLogLine(fmt.Sprintf("Searching for '%s'...", pattern))
	found := false
	for name, scheme := range r.typeEnv.Bindings(dang.PublicVisibility) {
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
	for name, env := range r.typeEnv.NamedTypes() {
		if strings.Contains(strings.ToLower(name), pattern) {
			doc := env.GetModuleDocString()
			if doc != "" {
				if len(doc) > 60 {
					doc = doc[:57] + "..."
				}
				e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s - %s", name, doc)))
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
