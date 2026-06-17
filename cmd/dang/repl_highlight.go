package main

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/tuist"
)

// Syntax highlighting for the REPL. Source is classified by tree-sitter in
// pkg/dang (the same grammar and query the editors and docs use), and each
// token class is colored from the ANSI 16-color palette so the theme follows
// the user's terminal. Highlighting is best-effort: without CGo (or on a
// parse failure) dang.Highlight returns no spans and code renders plain.
//
// The class → color choices mirror the docs' Rosé Pine palette, collapsed
// onto the 16 colors: the two blues (strings vs. functions) can't both be
// blue here, so strings take green.
var classStyles = map[string]lipgloss.Style{
	dang.ClassKeyword:   lipgloss.NewStyle().Foreground(lipgloss.Color("5")), // magenta
	dang.ClassOperator:  lipgloss.NewStyle().Foreground(lipgloss.Color("5")), // magenta
	dang.ClassType:      lipgloss.NewStyle().Foreground(lipgloss.Color("3")), // yellow
	dang.ClassNumber:    lipgloss.NewStyle().Foreground(lipgloss.Color("3")), // yellow
	dang.ClassString:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")), // green
	dang.ClassEscape:    lipgloss.NewStyle().Foreground(lipgloss.Color("6")), // cyan
	dang.ClassComment:   lipgloss.NewStyle().Foreground(lipgloss.Color("8")), // bright black
	dang.ClassFunction:  lipgloss.NewStyle().Foreground(lipgloss.Color("4")), // blue
	dang.ClassBuiltin:   lipgloss.NewStyle().Foreground(lipgloss.Color("6")), // cyan
	dang.ClassDirective: lipgloss.NewStyle().Foreground(lipgloss.Color("8")), // bright black
	dang.ClassSelf:      lipgloss.NewStyle().Foreground(lipgloss.Color("1")), // red
	dang.ClassProperty:  lipgloss.NewStyle().Foreground(lipgloss.Color("1")), // red
	dang.ClassLabel:     lipgloss.NewStyle().Foreground(lipgloss.Color("1")), // red
	// variable and punct intentionally absent: they keep the default
	// foreground, matching the docs.
}

// classStylers caches a width-preserving styling closure per class so we don't
// re-wrap lipgloss.Style.Render (variadic) on every span.
var classStylers = func() map[string]func(string) string {
	m := make(map[string]func(string) string, len(classStyles))
	for class, style := range classStyles {
		s := style
		m[class] = func(text string) string { return s.Render(text) }
	}
	return m
}()

// isReplCommand reports whether a line is a REPL meta-command (":help", etc.)
// rather than Dang code; those aren't highlighted.
func isReplCommand(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), ":")
}

// highlightSpans builds tuist.StyleSpans for live input highlighting in the
// text editor. Returns nil for REPL commands and unhighlightable input.
func highlightSpans(value string) []tuist.StyleSpan {
	if isReplCommand(value) {
		return nil
	}
	var spans []tuist.StyleSpan
	for _, hs := range dang.Highlight(value) {
		styler, ok := classStylers[hs.Class]
		if !ok {
			continue
		}
		spans = append(spans, tuist.StyleSpan{Start: hs.Start, End: hs.End, Style: styler})
	}
	return spans
}

// highlightCode returns code with ANSI styling applied, for echoing submitted
// input into the scrollback. Styling is clipped at newlines so the result is
// safe to split into display lines (a style never bleeds across a line break).
func highlightCode(code string) string {
	if isReplCommand(code) {
		return code
	}
	spans := dang.Highlight(code)
	if len(spans) == 0 {
		return code
	}
	runes := []rune(code)
	var b strings.Builder
	prev := 0
	for _, sp := range spans {
		if sp.Start > prev {
			b.WriteString(string(runes[prev:sp.Start]))
		}
		seg := string(runes[sp.Start:sp.End])
		if styler, ok := classStylers[sp.Class]; ok {
			writeStyledLines(&b, seg, styler)
		} else {
			b.WriteString(seg)
		}
		prev = sp.End
	}
	if prev < len(runes) {
		b.WriteString(string(runes[prev:]))
	}
	return b.String()
}

// writeStyledLines styles seg one line at a time, leaving newlines unstyled,
// so the styling never spans a line break (e.g. inside a multiline string).
func writeStyledLines(b *strings.Builder, seg string, styler func(string) string) {
	for i, line := range strings.Split(seg, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		if line != "" {
			b.WriteString(styler(line))
		}
	}
}
