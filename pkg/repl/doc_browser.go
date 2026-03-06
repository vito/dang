package repl

import (
	"sort"
	"strings"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

// DocColumn represents one column in the Miller-column browser.
type DocColumn struct {
	Title        string
	Doc          string     // doc string for the column header
	Items        []DocItem  // all items (unfiltered)
	Filtered     []int      // indices into Items matching filter (nil = show all)
	Filter       string     // current filter text
	Index        int        // selected index within Visible() items
	Offset       int        // scroll offset (for item lists)
	DetailOffset int        // scroll offset (for detail panes)
	TypeEnv      dang.Env   // the env this column lists members of (nil for detail)
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

// BuildColumn creates a column listing public members of an Env.
func BuildColumn(title, doc string, env dang.Env) DocColumn {
	col := DocColumn{
		Title:   title,
		Doc:     doc,
		TypeEnv: env,
	}

	if env == nil {
		return col
	}

	for name, scheme := range env.Bindings(dang.PublicVisibility) {
		t, _ := scheme.Type()
		if t == nil {
			continue
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

			ret := UnwrapType(fn.Ret(true))
			if mod, ok := ret.(dang.Env); ok {
				item.RetEnv = mod
			}
		} else {
			inner := UnwrapType(t)
			if mod, ok := inner.(dang.Env); ok {
				item.RetEnv = mod
				item.Kind = ClassifyEnv(mod)
			} else {
				item.Kind = KindField
			}
		}

		col.Items = append(col.Items, item)
	}

	seen := make(map[string]bool, len(col.Items))
	for _, item := range col.Items {
		seen[item.Name] = true
	}
	if mod, ok := env.(*dang.Module); ok {
		dang.ForEachMethod(mod, func(def dang.BuiltinDef) {
			if seen[def.Name] {
				return
			}
			seen[def.Name] = true

			item := DocItem{
				Name: def.Name,
				Kind: KindField,
				Doc:  def.Doc,
			}
			for _, p := range def.ParamTypes {
				item.Args = append(item.Args, DocArg{
					Name:    p.Name,
					TypeStr: FormatType(p.Type),
				})
			}
			if def.ReturnType != nil {
				item.TypeStr = "-> " + FormatType(def.ReturnType)
			}
			if def.BlockType != nil {
				item.BlockArgs = ExtractArgs(def.BlockType)
				item.BlockRet = FormatType(def.BlockType.Ret(true))
			}
			if def.ReturnType != nil {
				ret := UnwrapType(def.ReturnType)
				if retEnv, ok := ret.(dang.Env); ok {
					item.RetEnv = retEnv
				}
			}
			col.Items = append(col.Items, item)
		})
	}

	for name, namedEnv := range env.NamedTypes() {
		if seen[name] {
			continue
		}
		item := DocItem{
			Name:    name,
			TypeStr: namedEnv.Name(),
			RetEnv:  namedEnv,
			Kind:    ClassifyEnv(namedEnv),
		}
		if d := namedEnv.GetModuleDocString(); d != "" {
			item.Doc = d
		}
		col.Items = append(col.Items, item)
	}

	sort.Slice(col.Items, func(i, j int) bool {
		if col.Items[i].Kind != col.Items[j].Kind {
			return col.Items[i].Kind < col.Items[j].Kind
		}
		return strings.ToLower(col.Items[i].Name) < strings.ToLower(col.Items[j].Name)
	})

	return col
}

// BuildDetailColumn creates a non-interactive detail pane for an item.
func BuildDetailColumn(item DocItem) DocColumn {
	return DocColumn{
		Title: item.Name,
		Doc:   item.TypeStr,
		Items: nil,
	}
}
