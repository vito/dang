package repl

import (
	"sort"
	"strings"

	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

// DocColumn represents one column in the Miller-column browser.
type DocColumn struct {
	Title        string
	Doc          string         // doc string for the column header
	Items        []DocItem      // all items (unfiltered)
	Filtered     []int          // indices into Items matching filter (nil = show all)
	Filter       string         // current filter text
	Index        int            // selected index within Visible() items
	Offset       int            // scroll offset (for item lists)
	DetailOffset int            // scroll offset (for detail panes)
	TypeScope    dang.TypeScope // the env this column lists members of (nil for detail)
}

// Visible returns the items to display, respecting the filter.
func (c *DocColumn) Visible() []DocItem {
	if c.Filtered == nil {
		return c.Items
	}
	vis := make([]DocItem, len(c.Filtered))
	for i, idx := range c.Filtered {
		vis[i] = c.Items[idx]
	}
	return vis
}

// SelectedItem returns the currently selected item, if any.
func (c *DocColumn) SelectedItem() (DocItem, bool) {
	vis := c.Visible()
	if c.Index >= 0 && c.Index < len(vis) {
		return vis[c.Index], true
	}
	return DocItem{}, false
}

// ApplyFilter updates the filtered indices based on the current filter string.
func (c *DocColumn) ApplyFilter() {
	if c.Filter == "" {
		c.Filtered = nil
		return
	}
	lower := strings.ToLower(c.Filter)
	c.Filtered = nil
	for i, item := range c.Items {
		if strings.Contains(strings.ToLower(item.Name), lower) {
			c.Filtered = append(c.Filtered, i)
		}
	}
	if len(c.Filtered) == 0 {
		c.Index = 0
	} else if c.Index >= len(c.Filtered) {
		c.Index = len(c.Filtered) - 1
	}
	c.Offset = 0
}

// BuildRootColumn creates the column listing what's in scope at the top
// level (see analysis.ScopeMembers).
func BuildRootColumn(env dang.TypeScope) DocColumn {
	return buildColumn("(root)", "Top-level scope", env, analysis.ScopeMembers(env))
}

// BuildColumn creates a column listing the public members of a TypeScope (see
// analysis.NamedTypeMembers).
func BuildColumn(title, doc string, env dang.TypeScope) DocColumn {
	return buildColumn(title, doc, env, analysis.NamedTypeMembers(env))
}

func buildColumn(title, doc string, env dang.TypeScope, members []analysis.Member) DocColumn {
	col := DocColumn{
		Title:     title,
		Doc:       doc,
		TypeScope: env,
	}
	for _, m := range members {
		col.Items = append(col.Items, docItem(m))
	}

	sort.SliceStable(col.Items, func(i, j int) bool {
		if col.Items[i].Kind != col.Items[j].Kind {
			return col.Items[i].Kind < col.Items[j].Kind
		}
		return strings.ToLower(col.Items[i].Name) < strings.ToLower(col.Items[j].Name)
	})

	return col
}

// docItem renders an analysis member as a doc browser entry.
func docItem(m analysis.Member) DocItem {
	item := DocItem{
		Name:         m.Name,
		Kind:         itemKind(m),
		TypeStr:      m.Type,
		Doc:          m.Doc,
		RetTypeScope: m.Scope,
	}
	if m.Kind == analysis.KindFunction {
		item.TypeStr = "-> " + m.Type
	}
	for _, arg := range m.Args {
		item.Args = append(item.Args, DocArg{Name: arg.Name, TypeStr: arg.Type, Doc: arg.Doc})
	}
	if m.Block != nil {
		for _, arg := range m.Block.Args {
			item.BlockArgs = append(item.BlockArgs, DocArg{Name: arg.Name, TypeStr: arg.Type, Doc: arg.Doc})
		}
		item.BlockRet = m.Block.Type
	}
	return item
}

// itemKind classifies a member for the browser: a value whose type has
// members (a namespace, an object) browses like the type it holds.
func itemKind(m analysis.Member) ItemKind {
	switch m.Kind {
	case analysis.KindFunction:
		return KindField
	case analysis.KindValue:
		if m.Scope != nil {
			return ClassifyTypeScope(m.Scope)
		}
		return KindField
	case analysis.KindInterface:
		return KindInterface
	case analysis.KindEnum:
		return KindEnum
	case analysis.KindScalar:
		return KindScalar
	case analysis.KindUnion:
		return KindUnion
	case analysis.KindInput:
		return KindInput
	default:
		return KindType
	}
}

// BuildDetailColumn creates a non-interactive detail pane for an item.
func BuildDetailColumn(item DocItem) DocColumn {
	return DocColumn{
		Title: item.Name,
		Doc:   item.TypeStr,
		Items: nil,
	}
}
