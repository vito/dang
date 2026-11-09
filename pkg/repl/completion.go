package repl

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
	"github.com/vito/tuist"
)

// NewCompletionProvider returns a tuist.CompletionProvider that uses
// tree-sitter-based type-aware completions (see analysis.Complete), falling
// back to static keyword/name completions.
func NewCompletionProvider(ctx context.Context, typeScope dang.TypeScope, staticCompletions []string) tuist.CompletionProvider {
	return func(input string, cursorPos int) tuist.CompletionResult {
		if input == "" {
			return tuist.CompletionResult{}
		}

		line, col := byteOffsetToLineCol(input, cursorPos)

		result := analysis.Complete(ctx, typeScope, input, analysis.Pos{Line: line + 1, Column: col + 1})
		if len(result.Items) > 0 {
			return dangCompletionResult(result)
		}

		return staticCompletionResult(input, staticCompletions)
	}
}

// NewDetailRenderer returns a tuist.DetailRenderer that resolves rich
// documentation from the type environment. getInput returns the current
// editor contents, used to infer receiver types for member completions.
func NewDetailRenderer(ctx context.Context, typeScope dang.TypeScope, getInput func() string) tuist.DetailRenderer {
	return func(c tuist.Completion, width int) []string {
		// After a dot, the label names a member of the receiver, whatever
		// the scope binds by the same name.
		input := getInput()
		var item DocItem
		var found bool
		if afterDot(input) {
			item, found = resolveCompletionDocItem(ctx, typeScope, input, c)
		} else {
			item, found = DocItemFromTypeScope(typeScope, c.Label)
			if !found {
				item, found = resolveCompletionDocItem(ctx, typeScope, input, c)
			}
		}
		if !found {
			if c.Detail == "" && c.Documentation == "" {
				return nil
			}
			item = DocItem{
				Name:    c.Label,
				TypeStr: c.Detail,
				Doc:     c.Documentation,
			}
		}
		return RenderDetailLines(item, width)
	}
}

// BuildStaticCompletions builds a sorted list of completion strings from
// a type environment, including keywords.
func BuildStaticCompletions(typeScope dang.TypeScope) []string {
	seen := map[string]bool{}
	var completions []string

	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			completions = append(completions, name)
		}
	}

	keywords := []string{
		"let", "if", "else", "true", "false", "null",
		"self", "type", "pub", "new", "import", "assert",
		"rescue", "raise", "print", "loop",
	}
	for _, kw := range keywords {
		add(kw)
	}

	for name, scheme := range typeScope.Bindings(dang.PublicVisibility) {
		if dang.IsTypeDefBinding(scheme) || dang.IsIDTypeName(name) || dang.IsInternalName(name) {
			continue
		}
		add(name)
	}

	sort.Strings(completions)
	return completions
}

// ── internal helpers ────────────────────────────────────────────────────────

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

func dangCompletionResult(result dang.CompletionResult) tuist.CompletionResult {
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

	if !isArgCompletion && len(result.Items) > 0 {
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

func staticCompletionResult(input string, completions []string) tuist.CompletionResult {
	word := lastIdent(input)
	if word == "" {
		return tuist.CompletionResult{}
	}

	replaceFrom := len(input) - len(word)
	wordLower := strings.ToLower(word)

	var exactCase, otherCase []tuist.Completion
	for _, c := range completions {
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

func lastIdent(s string) string {
	i := len(s) - 1
	for i >= 0 && isIdentByte(s[i]) {
		i--
	}
	return s[i+1:]
}

// afterDot reports whether the input ends in a member being typed: a dot,
// then maybe part of a name.
func afterDot(input string) bool {
	rest := input[:len(input)-len(lastIdent(input))]
	return strings.HasSuffix(rest, ".")
}

func isIdentByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// ── doc item types & resolution ─────────────────────────────────────────────

// ItemKind classifies a doc browser entry.
type ItemKind int

const (
	KindField ItemKind = iota
	KindType
	KindInterface
	KindEnum
	KindScalar
	KindUnion
	KindInput
)

var kindOrder = [...]string{
	KindField:     "field",
	KindType:      "type",
	KindInterface: "interface",
	KindEnum:      "enum",
	KindScalar:    "scalar",
	KindUnion:     "union",
	KindInput:     "input",
}

// kindColors uses the ANSI 16-color palette ("0".."15") so completion-kind
// tags follow the user's terminal theme.
var kindColors = [...]string{
	KindField:     "6",  // cyan
	KindType:      "3",  // yellow
	KindInterface: "5",  // magenta
	KindEnum:      "2",  // green
	KindScalar:    "4",  // blue
	KindUnion:     "1",  // red
	KindInput:     "13", // bright magenta
}

func (k ItemKind) Label() string {
	if int(k) < len(kindOrder) {
		return kindOrder[k]
	}
	return "?"
}

func (k ItemKind) Color() string {
	if int(k) < len(kindColors) {
		return kindColors[k]
	}
	return "7" // default foreground
}

// DocItem is a single entry in the doc browser or completion detail.
type DocItem struct {
	Name         string
	Kind         ItemKind
	TypeStr      string
	Doc          string
	Args         []DocArg
	BlockArgs    []DocArg
	BlockRet     string
	RetTypeScope dang.TypeScope
}

// DocArg represents an argument to a function.
type DocArg struct {
	Name    string
	TypeStr string
	Doc     string
}

// ClassifyTypeScope determines the ItemKind for a module/env based on its Kind.
func ClassifyTypeScope(env dang.TypeScope) ItemKind {
	if mod, ok := env.(*dang.Type); ok {
		// A type's name is bound to its meta-type (its namespace).
		if mod.IsMeta() {
			mod = mod.InstanceType()
		}
		switch mod.Kind {
		case dang.EnumKind:
			return KindEnum
		case dang.ScalarKind:
			return KindScalar
		case dang.InterfaceKind:
			return KindInterface
		case dang.UnionKind:
			return KindUnion
		case dang.InputKind:
			return KindInput
		}
	}
	return KindType
}

// DocItemFromTypeScope builds a DocItem for what name names in env (see
// analysis.Lookup).
func DocItemFromTypeScope(env dang.TypeScope, name string) (DocItem, bool) {
	m, ok := analysis.Lookup(env, name)
	if !ok {
		return DocItem{}, false
	}
	return docItem(m), true
}

// resolveCompletionDocItem documents a member completion: the label of a
// member of the receiver before the input's last dot.
func resolveCompletionDocItem(ctx context.Context, typeScope dang.TypeScope, input string, c tuist.Completion) (DocItem, bool) {
	dotIdx := strings.LastIndexByte(input, '.')
	if dotIdx < 0 {
		return DocItem{}, false
	}
	m, err := analysis.Doc(ctx, typeScope, input[:dotIdx]+"."+c.Label)
	if err != nil {
		return DocItem{}, false
	}
	return docItem(*m), true
}

// ── detail rendering ────────────────────────────────────────────────────────

var (
	DetailTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("15")).Bold(true) // bright white
	DocTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("7")) // white
	ArgNameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan
	ArgTypeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // bright black
	DimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // bright black
)

// RenderDetailLines renders a DocItem for the completion detail panel.
func RenderDetailLines(item DocItem, width int) []string {
	contentW := max(8, width-2)
	titleLine := DetailTitleStyle.Render(item.Name)
	lines := []string{titleLine}
	lines = append(lines, RenderDocDetail(item, contentW, DocTextStyle, ArgNameStyle, ArgTypeStyle, DimStyle)...)
	return lines
}

// RenderDocDetail renders structured documentation for a DocItem.
func RenderDocDetail(item DocItem, w int, docStyle, argNameStyle, argTypeStyle, dimSt lipgloss.Style) []string {
	var lines []string

	if item.TypeStr != "" {
		lines = append(lines, argTypeStyle.Render(Truncate(item.TypeStr, w)))
		lines = append(lines, "")
	}

	if item.Doc != "" {
		wrapped := WordWrap(item.Doc, w)
		for line := range strings.SplitSeq(wrapped, "\n") {
			lines = append(lines, docStyle.Render(line))
		}
		lines = append(lines, "")
	}

	if len(item.Args) > 0 {
		lines = append(lines, "Arguments:")
		for _, arg := range item.Args {
			lines = append(lines, fmt.Sprintf("  %s %s",
				argNameStyle.Render(arg.Name+":"),
				argTypeStyle.Render(arg.TypeStr),
			))
			if arg.Doc != "" {
				wrapped := WordWrap(arg.Doc, w-4)
				for line := range strings.SplitSeq(wrapped, "\n") {
					lines = append(lines, "    "+dimSt.Render(line))
				}
			}
		}
	}

	if len(item.BlockArgs) > 0 {
		lines = append(lines, "")
		blockHeader := "Block:"
		if item.BlockRet != "" {
			blockHeader = fmt.Sprintf("Block -> %s:", argTypeStyle.Render(item.BlockRet))
		}
		lines = append(lines, blockHeader)
		for _, arg := range item.BlockArgs {
			lines = append(lines, fmt.Sprintf("  %s %s",
				argNameStyle.Render(arg.Name+":"),
				argTypeStyle.Render(arg.TypeStr),
			))
			if arg.Doc != "" {
				wrapped := WordWrap(arg.Doc, w-4)
				for line := range strings.SplitSeq(wrapped, "\n") {
					lines = append(lines, "    "+dimSt.Render(line))
				}
			}
		}
	}

	if len(lines) <= 1 && item.Doc == "" && len(item.Args) == 0 && len(item.BlockArgs) == 0 {
		lines = append(lines, dimSt.Render("(no documentation)"))
	}

	return lines
}

// Truncate truncates a string to maxW, adding an ellipsis if needed.
func Truncate(s string, maxW int) string {
	if len(s) <= maxW {
		return s
	}
	if maxW <= 3 {
		return s[:maxW]
	}
	return s[:maxW-1] + "…"
}

// PadRight pads a string with spaces to width w.
func PadRight(s string, w int) string {
	visible := lipgloss.Width(s)
	if visible >= w {
		return s
	}
	return s + strings.Repeat(" ", w-visible)
}

// GetLine returns lines[i] or "" if out of bounds.
func GetLine(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

// WordWrap wraps text to the given width.
func WordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	lineLen := 0
	for i, w := range words {
		wLen := len(w)
		if i > 0 && lineLen+1+wLen > width {
			b.WriteByte('\n')
			lineLen = 0
		} else if i > 0 {
			b.WriteByte(' ')
			lineLen++
		}
		b.WriteString(w)
		lineLen += wLen
	}
	return b.String()
}
