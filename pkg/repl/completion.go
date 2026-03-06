package repl

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/tuist"
)

// NewCompletionProvider returns a tuist.CompletionProvider that uses
// tree-sitter-based type-aware completions, falling back to static
// keyword/name completions.
func NewCompletionProvider(ctx context.Context, typeEnv dang.Env, staticCompletions []string) tuist.CompletionProvider {
	return func(input string, cursorPos int) tuist.CompletionResult {
		if input == "" {
			return tuist.CompletionResult{}
		}

		line, col := byteOffsetToLineCol(input, cursorPos)

		result := dang.Complete(ctx, typeEnv, input, line, col)
		if len(result.Items) > 0 {
			return dangCompletionResult(result)
		}

		return staticCompletionResult(input, staticCompletions)
	}
}

// NewDetailRenderer returns a tuist.DetailRenderer that resolves rich
// documentation from the type environment. getInput returns the current
// editor contents, used to infer receiver types for member completions.
func NewDetailRenderer(ctx context.Context, typeEnv dang.Env, getInput func() string) tuist.DetailRenderer {
	return func(c tuist.Completion, width int) []string {
		item, found := DocItemFromEnv(typeEnv, c.Label)
		if !found {
			item, found = resolveCompletionDocItem(ctx, typeEnv, getInput(), c)
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
func BuildStaticCompletions(typeEnv dang.Env) []string {
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

	for name, scheme := range typeEnv.Bindings(dang.PublicVisibility) {
		if dang.IsTypeDefBinding(scheme) || dang.IsIDTypeName(name) {
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

var kindColors = [...]string{
	KindField:     "117",
	KindType:      "213",
	KindInterface: "141",
	KindEnum:      "221",
	KindScalar:    "114",
	KindUnion:     "209",
	KindInput:     "183",
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
	return "247"
}

// DocItem is a single entry in the doc browser or completion detail.
type DocItem struct {
	Name      string
	Kind      ItemKind
	TypeStr   string
	Doc       string
	Args      []DocArg
	BlockArgs []DocArg
	BlockRet  string
	RetEnv    dang.Env
}

// DocArg represents an argument to a function.
type DocArg struct {
	Name    string
	TypeStr string
	Doc     string
}

// ClassifyEnv determines the ItemKind for a module/env based on its ModuleKind.
func ClassifyEnv(env dang.Env) ItemKind {
	if mod, ok := env.(*dang.Module); ok {
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

// DocItemFromEnv builds a DocItem for a named binding in env.
func DocItemFromEnv(env dang.Env, name string) (DocItem, bool) {
	if env == nil {
		return DocItem{}, false
	}

	for bName, scheme := range env.Bindings(dang.PublicVisibility) {
		if bName != name {
			continue
		}
		t, _ := scheme.Type()
		if t == nil {
			return DocItem{}, false
		}
		item := DocItem{
			Name:    name,
			TypeStr: t.String(),
		}
		if d, found := env.GetDocString(name); found {
			item.Doc = d
		}
		if fn, ok := t.(*hm.FunctionType); ok {
			item.Kind = KindField
			item.Args = ExtractArgs(fn)
			item.TypeStr = FormatReturnType(fn)
			ExtractBlockInfo(fn, &item)
		} else {
			inner := UnwrapType(t)
			if mod, ok := inner.(dang.Env); ok {
				item.Kind = ClassifyEnv(mod)
			} else {
				item.Kind = KindField
			}
		}
		return item, true
	}

	if mod, ok := env.(*dang.Module); ok {
		var found DocItem
		var matched bool
		dang.ForEachMethod(mod, func(def dang.BuiltinDef) {
			if matched || def.Name != name {
				return
			}
			matched = true
			found = DocItem{
				Name: def.Name,
				Kind: KindField,
				Doc:  def.Doc,
			}
			for _, p := range def.ParamTypes {
				found.Args = append(found.Args, DocArg{
					Name:    p.Name,
					TypeStr: FormatType(p.Type),
				})
			}
			if def.ReturnType != nil {
				found.TypeStr = "-> " + FormatType(def.ReturnType)
			}
			if def.BlockType != nil {
				found.BlockArgs = ExtractArgs(def.BlockType)
				found.BlockRet = FormatType(def.BlockType.Ret(true))
			}
		})
		if matched {
			return found, true
		}
	}

	for tName, namedEnv := range env.NamedTypes() {
		if tName == name {
			item := DocItem{
				Name:    name,
				TypeStr: namedEnv.Name(),
				Kind:    ClassifyEnv(namedEnv),
			}
			if d := namedEnv.GetModuleDocString(); d != "" {
				item.Doc = d
			}
			return item, true
		}
	}

	return DocItem{}, false
}

func resolveCompletionDocItem(ctx context.Context, typeEnv dang.Env, input string, c tuist.Completion) (DocItem, bool) {
	dotIdx := -1
	for i := len(input) - 1; i >= 0; i-- {
		if input[i] == '.' {
			dotIdx = i
			break
		}
	}
	if dotIdx < 0 {
		return DocItem{}, false
	}
	receiverText := input[:dotIdx]
	receiverType := dang.InferReceiverType(ctx, typeEnv, receiverText)
	if receiverType == nil {
		return DocItem{}, false
	}
	unwrapped := UnwrapType(receiverType)
	env, ok := unwrapped.(dang.Env)
	if !ok {
		return DocItem{}, false
	}
	return DocItemFromEnv(env, c.Label)
}

// ExtractArgs extracts function arguments as DocArgs.
func ExtractArgs(fn *hm.FunctionType) []DocArg {
	arg := fn.Arg()
	rec, ok := arg.(*dang.RecordType)
	if !ok {
		return nil
	}
	var args []DocArg
	for _, field := range rec.Fields {
		t, _ := field.Value.Type()
		a := DocArg{
			Name:    field.Key,
			TypeStr: FormatType(t),
		}
		if rec.DocStrings != nil {
			if doc, found := rec.DocStrings[field.Key]; found {
				a.Doc = doc
			}
		}
		args = append(args, a)
	}
	return args
}

// ExtractBlockInfo populates block args/ret on a DocItem.
func ExtractBlockInfo(fn *hm.FunctionType, item *DocItem) {
	block := fn.Block()
	if block == nil {
		return
	}
	item.BlockArgs = ExtractArgs(block)
	item.BlockRet = FormatType(block.Ret(true))
}

// FormatReturnType formats a function's return type.
func FormatReturnType(fn *hm.FunctionType) string {
	ret := fn.Ret(true)
	return "-> " + FormatType(ret)
}

// FormatType formats a type for display.
func FormatType(t hm.Type) string {
	if t == nil {
		return "?"
	}
	return t.String()
}

// UnwrapType strips NonNull/List wrappers from a type.
func UnwrapType(t hm.Type) hm.Type {
	for {
		switch inner := t.(type) {
		case hm.NonNullType:
			t = inner.Type
		case dang.ListType:
			t = inner.Type
		case dang.GraphQLListType:
			t = inner.Type
		default:
			return t
		}
	}
}

// ── detail rendering ────────────────────────────────────────────────────────

var (
	DetailTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("255")).Bold(true)
	DocTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("249"))
	ArgNameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	ArgTypeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	DimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
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
