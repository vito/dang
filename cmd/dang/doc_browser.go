package main

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

// docColumn represents one column in the Miller-column browser.
type docColumn struct {
	title        string
	doc          string    // doc string for the column header (type/module doc)
	items        []docItem // all items (unfiltered)
	filtered     []int     // indices into items matching filter (nil = show all)
	filter       string    // current filter text
	index        int       // selected index within visible() items
	offset       int       // scroll offset (for item lists)
	detailOffset int       // scroll offset (for detail panes)
	typeEnv      dang.Env  // the env this column lists members of (nil for detail)
}

// visible returns the items to display, respecting the filter.
func (c *docColumn) visible() []docItem {
	if c.filtered == nil {
		return c.items
	}
	vis := make([]docItem, len(c.filtered))
	for i, idx := range c.filtered {
		vis[i] = c.items[idx]
	}
	return vis
}

// selectedItem returns the currently selected item, if any.
func (c *docColumn) selectedItem() (docItem, bool) {
	vis := c.visible()
	if c.index >= 0 && c.index < len(vis) {
		return vis[c.index], true
	}
	return docItem{}, false
}

// applyFilter updates the filtered indices based on the current filter string.
func (c *docColumn) applyFilter() {
	if c.filter == "" {
		c.filtered = nil
		return
	}
	lower := strings.ToLower(c.filter)
	c.filtered = nil
	for i, item := range c.items {
		if strings.Contains(strings.ToLower(item.name), lower) {
			c.filtered = append(c.filtered, i)
		}
	}
	if len(c.filtered) == 0 {
		c.index = 0
	} else if c.index >= len(c.filtered) {
		c.index = len(c.filtered) - 1
	}
	c.offset = 0
}

// itemKind classifies a doc browser entry.
type itemKind int

const (
	kindField     itemKind = iota
	kindType
	kindInterface
	kindEnum
	kindScalar
	kindUnion
	kindInput
)

var kindOrder = [...]string{
	kindField:     "field",
	kindType:      "type",
	kindInterface: "interface",
	kindEnum:      "enum",
	kindScalar:    "scalar",
	kindUnion:     "union",
	kindInput:     "input",
}

var kindColors = [...]string{
	kindField:     "117",
	kindType:      "213",
	kindInterface: "141",
	kindEnum:      "221",
	kindScalar:    "114",
	kindUnion:     "209",
	kindInput:     "183",
}

func (k itemKind) label() string {
	if int(k) < len(kindOrder) {
		return kindOrder[k]
	}
	return "?"
}

func (k itemKind) color() string {
	if int(k) < len(kindColors) {
		return kindColors[k]
	}
	return "247"
}

// docItem is a single entry in a column.
type docItem struct {
	name      string
	kind      itemKind
	typeStr   string
	doc       string
	args      []docArg
	blockArgs []docArg
	blockRet  string
	retEnv    dang.Env
}

// docArg represents an argument to a function.
type docArg struct {
	name    string
	typeStr string
	doc     string
}

// classifyEnv determines the itemKind for a module/env based on its ModuleKind.
func classifyEnv(env dang.Env) itemKind {
	if mod, ok := env.(*dang.Module); ok {
		switch mod.Kind {
		case dang.EnumKind:
			return kindEnum
		case dang.ScalarKind:
			return kindScalar
		case dang.InterfaceKind:
			return kindInterface
		case dang.UnionKind:
			return kindUnion
		case dang.InputKind:
			return kindInput
		}
	}
	return kindType
}

// buildColumn creates a column listing public members of an Env.
func buildColumn(title, doc string, env dang.Env) docColumn {
	col := docColumn{
		title:   title,
		doc:     doc,
		typeEnv: env,
	}

	if env == nil {
		return col
	}

	for name, scheme := range env.Bindings(dang.PublicVisibility) {
		t, _ := scheme.Type()
		if t == nil {
			continue
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

			ret := unwrapType(fn.Ret(true))
			if mod, ok := ret.(dang.Env); ok {
				item.retEnv = mod
			}
		} else {
			inner := unwrapType(t)
			if mod, ok := inner.(dang.Env); ok {
				item.retEnv = mod
				item.kind = classifyEnv(mod)
			} else {
				item.kind = kindField
			}
		}

		col.items = append(col.items, item)
	}

	seen := make(map[string]bool, len(col.items))
	for _, item := range col.items {
		seen[item.name] = true
	}
	if mod, ok := env.(*dang.Module); ok {
		dang.ForEachMethod(mod, func(def dang.BuiltinDef) {
			if seen[def.Name] {
				return
			}
			seen[def.Name] = true

			item := docItem{
				name: def.Name,
				kind: kindField,
				doc:  def.Doc,
			}
			for _, p := range def.ParamTypes {
				item.args = append(item.args, docArg{
					name:    p.Name,
					typeStr: formatType(p.Type),
				})
			}
			if def.ReturnType != nil {
				item.typeStr = "-> " + formatType(def.ReturnType)
			}
			if def.BlockType != nil {
				item.blockArgs = extractArgs(def.BlockType)
				item.blockRet = formatType(def.BlockType.Ret(true))
			}
			if def.ReturnType != nil {
				ret := unwrapType(def.ReturnType)
				if retEnv, ok := ret.(dang.Env); ok {
					item.retEnv = retEnv
				}
			}
			col.items = append(col.items, item)
		})
	}

	for name, namedEnv := range env.NamedTypes() {
		if seen[name] {
			continue
		}
		item := docItem{
			name:    name,
			typeStr: namedEnv.Name(),
			retEnv:  namedEnv,
			kind:    classifyEnv(namedEnv),
		}
		if d := namedEnv.GetModuleDocString(); d != "" {
			item.doc = d
		}
		col.items = append(col.items, item)
	}

	sort.Slice(col.items, func(i, j int) bool {
		if col.items[i].kind != col.items[j].kind {
			return col.items[i].kind < col.items[j].kind
		}
		return strings.ToLower(col.items[i].name) < strings.ToLower(col.items[j].name)
	})

	return col
}

func extractArgs(fn *hm.FunctionType) []docArg {
	arg := fn.Arg()
	rec, ok := arg.(*dang.RecordType)
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
		case dang.ListType:
			t = inner.Type
		case dang.GraphQLListType:
			t = inner.Type
		default:
			return t
		}
	}
}

// buildDetailColumn creates a non-interactive detail pane for an item.
func buildDetailColumn(item docItem) docColumn {
	return docColumn{
		title: item.name,
		doc:   item.typeStr,
		items: nil,
	}
}

// Helper functions shared by doc browser implementations.

func truncate(s string, maxW int) string {
	if len(s) <= maxW {
		return s
	}
	if maxW <= 3 {
		return s[:maxW]
	}
	return s[:maxW-1] + "…"
}

func padRight(s string, w int) string {
	visible := lipgloss.Width(s)
	if visible >= w {
		return s
	}
	return s + strings.Repeat(" ", w-visible)
}

func getLine(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

func wordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
		} else {
			line += " " + w
		}
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}

// docItemFromEnv builds a docItem for a named binding in env. Returns the item
// and true if found; zero value and false otherwise. Used by the completion
// detail bubble to get structured info (args, block, doc) for a completion.
func docItemFromEnv(env dang.Env, name string) (docItem, bool) {
	if env == nil {
		return docItem{}, false
	}

	// Try bindings first.
	for bName, scheme := range env.Bindings(dang.PublicVisibility) {
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
			if mod, ok := inner.(dang.Env); ok {
				item.kind = classifyEnv(mod)
			} else {
				item.kind = kindField
			}
		}
		return item, true
	}

	// Try builtin methods.
	if mod, ok := env.(*dang.Module); ok {
		var found docItem
		var matched bool
		dang.ForEachMethod(mod, func(def dang.BuiltinDef) {
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

	// Try named types.
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


