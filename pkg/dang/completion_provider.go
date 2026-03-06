package dang

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/tuist"
)

// NewCompletionProvider returns a tuist.CompletionProvider that uses
// tree-sitter-based type-aware completions, falling back to static
// keyword/name completions.
func NewCompletionProvider(ctx context.Context, typeEnv Env, staticCompletions []string) tuist.CompletionProvider {
	return func(input string, cursorPos int) tuist.CompletionResult {
		if input == "" {
			return tuist.CompletionResult{}
		}

		line, col := byteOffsetToLineCol(input, cursorPos)

		result := Complete(ctx, typeEnv, input, line, col)
		if len(result.Items) > 0 {
			return dangCompletionResult(result)
		}

		return staticCompletionResult(input, staticCompletions)
	}
}

// NewDetailRenderer returns a tuist.DetailRenderer that resolves rich
// documentation from the type environment. getInput returns the current
// editor contents, used to infer receiver types for member completions.
func NewDetailRenderer(ctx context.Context, typeEnv Env, getInput func() string) tuist.DetailRenderer {
	return func(c tuist.Completion, width int) []string {
		item, found := docItemFromEnv(typeEnv, c.Label)
		if !found {
			item, found = resolveCompletionDocItem(ctx, typeEnv, getInput(), c)
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
		return renderDetailLines(item, width)
	}
}

// BuildStaticCompletions builds a sorted list of completion strings from
// a type environment, including keywords.
func BuildStaticCompletions(typeEnv Env) []string {
	seen := map[string]bool{}
	var completions []string

	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			completions = append(completions, name)
		}
	}

	keywords := []string{
		"let", "if", "else", "for", "in", "true", "false", "null",
		"self", "type", "pub", "new", "import", "assert", "try",
		"catch", "raise", "print",
	}
	for _, kw := range keywords {
		add(kw)
	}

	for name, scheme := range typeEnv.Bindings(PublicVisibility) {
		if IsTypeDefBinding(scheme) || IsIDTypeName(name) {
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

func dangCompletionResult(result CompletionResult) tuist.CompletionResult {
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

func commonPrefix(completions []Completion) string {
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

// ── doc item types & resolution ─────────────────────────────────────────────

type itemKind int

const (
	kindField itemKind = iota
	kindType
	kindInterface
	kindEnum
	kindScalar
	kindUnion
	kindInput
)

type docItem struct {
	name      string
	kind      itemKind
	typeStr   string
	doc       string
	args      []docArg
	blockArgs []docArg
	blockRet  string
	retEnv    Env
}

type docArg struct {
	name    string
	typeStr string
	doc     string
}

func classifyEnv(env Env) itemKind {
	if mod, ok := env.(*Module); ok {
		switch mod.Kind {
		case EnumKind:
			return kindEnum
		case ScalarKind:
			return kindScalar
		case InterfaceKind:
			return kindInterface
		case UnionKind:
			return kindUnion
		case InputKind:
			return kindInput
		}
	}
	return kindType
}

func docItemFromEnv(env Env, name string) (docItem, bool) {
	if env == nil {
		return docItem{}, false
	}

	for bName, scheme := range env.Bindings(PublicVisibility) {
		if bName != name {
			continue
		}
		t, _ := scheme.Type()
		if t == nil {
			return docItem{}, false
		}
		item := docItem{
			name:    name,
			typeStr: t.String(),
		}
		if d, found := env.GetDocString(name); found {
			item.doc = d
		}
		if fn, ok := t.(*hm.FunctionType); ok {
			item.kind = kindField
			item.args = extractArgs(fn)
			item.typeStr = formatReturnType(fn)
			extractBlockInfo(fn, &item)
		} else {
			inner := unwrapType(t)
			if mod, ok := inner.(Env); ok {
				item.kind = classifyEnv(mod)
			} else {
				item.kind = kindField
			}
		}
		return item, true
	}

	if mod, ok := env.(*Module); ok {
		var found docItem
		var matched bool
		ForEachMethod(mod, func(def BuiltinDef) {
			if matched || def.Name != name {
				return
			}
			matched = true
			found = docItem{
				name: def.Name,
				kind: kindField,
				doc:  def.Doc,
			}
			for _, p := range def.ParamTypes {
				found.args = append(found.args, docArg{
					name:    p.Name,
					typeStr: formatType(p.Type),
				})
			}
			if def.ReturnType != nil {
				found.typeStr = "-> " + formatType(def.ReturnType)
			}
			if def.BlockType != nil {
				found.blockArgs = extractArgs(def.BlockType)
				found.blockRet = formatType(def.BlockType.Ret(true))
			}
		})
		if matched {
			return found, true
		}
	}

	for tName, namedEnv := range env.NamedTypes() {
		if tName == name {
			item := docItem{
				name:    name,
				typeStr: namedEnv.Name(),
				kind:    classifyEnv(namedEnv),
			}
			if d := namedEnv.GetModuleDocString(); d != "" {
				item.doc = d
			}
			return item, true
		}
	}

	return docItem{}, false
}

func resolveCompletionDocItem(ctx context.Context, typeEnv Env, input string, c tuist.Completion) (docItem, bool) {
	dotIdx := -1
	for i := len(input) - 1; i >= 0; i-- {
		if input[i] == '.' {
			dotIdx = i
			break
		}
	}
	if dotIdx < 0 {
		return docItem{}, false
	}
	receiverText := input[:dotIdx]
	receiverType := InferReceiverType(ctx, typeEnv, receiverText)
	if receiverType == nil {
		return docItem{}, false
	}
	unwrapped := unwrapType(receiverType)
	env, ok := unwrapped.(Env)
	if !ok {
		return docItem{}, false
	}
	return docItemFromEnv(env, c.Label)
}

func extractArgs(fn *hm.FunctionType) []docArg {
	arg := fn.Arg()
	rec, ok := arg.(*RecordType)
	if !ok {
		return nil
	}
	var args []docArg
	for _, field := range rec.Fields {
		t, _ := field.Value.Type()
		a := docArg{
			name:    field.Key,
			typeStr: formatType(t),
		}
		if rec.DocStrings != nil {
			if doc, found := rec.DocStrings[field.Key]; found {
				a.doc = doc
			}
		}
		args = append(args, a)
	}
	return args
}

func extractBlockInfo(fn *hm.FunctionType, item *docItem) {
	block := fn.Block()
	if block == nil {
		return
	}
	item.blockArgs = extractArgs(block)
	item.blockRet = formatType(block.Ret(true))
}

func formatReturnType(fn *hm.FunctionType) string {
	ret := fn.Ret(true)
	return "-> " + formatType(ret)
}

func formatType(t hm.Type) string {
	if t == nil {
		return "?"
	}
	return t.String()
}

func unwrapType(t hm.Type) hm.Type {
	for {
		switch inner := t.(type) {
		case hm.NonNullType:
			t = inner.Type
		case ListType:
			t = inner.Type
		case GraphQLListType:
			t = inner.Type
		default:
			return t
		}
	}
}

// ── detail rendering ────────────────────────────────────────────────────────

var (
	detailTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("255")).Bold(true)
	docTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("249"))
	argNameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	argTypeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

func renderDetailLines(item docItem, width int) []string {
	contentW := max(8, width-2)

	titleLine := detailTitleStyle.Render(item.name)
	lines := []string{titleLine}
	lines = append(lines, renderDocDetail(item, contentW)...)
	return lines
}

func renderDocDetail(item docItem, w int) []string {
	var lines []string

	if item.typeStr != "" {
		lines = append(lines, argTypeStyle.Render(truncate(item.typeStr, w)))
		lines = append(lines, "")
	}

	if item.doc != "" {
		wrapped := wordWrap(item.doc, w)
		for line := range strings.SplitSeq(wrapped, "\n") {
			lines = append(lines, docTextStyle.Render(line))
		}
		lines = append(lines, "")
	}

	if len(item.args) > 0 {
		lines = append(lines, "Arguments:")
		for _, arg := range item.args {
			lines = append(lines, fmt.Sprintf("  %s %s",
				argNameStyle.Render(arg.name+":"),
				argTypeStyle.Render(arg.typeStr),
			))
			if arg.doc != "" {
				wrapped := wordWrap(arg.doc, w-4)
				for line := range strings.SplitSeq(wrapped, "\n") {
					lines = append(lines, "    "+dimStyle.Render(line))
				}
			}
		}
	}

	if len(item.blockArgs) > 0 {
		lines = append(lines, "")
		blockHeader := "Block:"
		if item.blockRet != "" {
			blockHeader = fmt.Sprintf("Block -> %s:", argTypeStyle.Render(item.blockRet))
		}
		lines = append(lines, blockHeader)
		for _, arg := range item.blockArgs {
			lines = append(lines, fmt.Sprintf("  %s %s",
				argNameStyle.Render(arg.name+":"),
				argTypeStyle.Render(arg.typeStr),
			))
			if arg.doc != "" {
				wrapped := wordWrap(arg.doc, w-4)
				for line := range strings.SplitSeq(wrapped, "\n") {
					lines = append(lines, "    "+dimStyle.Render(line))
				}
			}
		}
	}

	if len(lines) <= 1 && item.doc == "" && len(item.args) == 0 && len(item.blockArgs) == 0 {
		lines = append(lines, dimStyle.Render("(no documentation)"))
	}

	return lines
}

func truncate(s string, maxW int) string {
	if len(s) <= maxW {
		return s
	}
	if maxW <= 3 {
		return s[:maxW]
	}
	return s[:maxW-1] + "…"
}

func wordWrap(s string, width int) string {
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
