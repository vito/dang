package dangdocs

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vito/booklit"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

// The standard library reference is generated straight from the builtin
// registry in pkg/dang (see stdlib.go, stdlib_random.go, stdlib_regexp.go,
// assert.go). Each entry's signature comes from the registered parameter,
// block, and return types; its description comes from the builtin's .Doc(...).
// Editing a builtin updates this page — there is nothing to hand-maintain.

// StdlibFunctions renders the top-level builtin functions (alphabetical).
//
//	\stdlib-functions
func (p Plugin) StdlibFunctions() booklit.Content {
	var defs []dang.BuiltinDef
	dang.ForEachFunction(func(d dang.BuiltinDef) {
		defs = append(defs, d)
	})
	return stdlibList(defs, "")
}

// StdlibMethods renders the builtin methods of a receiver type, named by its
// Dang type name (e.g. String, List, Match). Entries are alphabetical and
// prefixed with a leading dot to read as method calls.
//
//	\stdlib-methods{String}
func (p Plugin) StdlibMethods(name booklit.Content) (booklit.Content, error) {
	typeName := name.String()
	recv := receiverByName(typeName)
	if recv == nil {
		return nil, fmt.Errorf("stdlib-methods: no builtin method receiver named %q", typeName)
	}
	var defs []dang.BuiltinDef
	dang.ForEachMethod(recv, func(d dang.BuiltinDef) {
		defs = append(defs, d)
	})
	return stdlibList(defs, "."), nil
}

// StdlibStatics renders the static methods of a module, named by its Dang type
// name (e.g. Random, UUID). Entries are alphabetical and prefixed with the
// module name to read as qualified calls.
//
//	\stdlib-statics{Random}
func (p Plugin) StdlibStatics(name booklit.Content) (booklit.Content, error) {
	moduleName := name.String()
	mod := moduleByName(moduleName)
	if mod == nil {
		return nil, fmt.Errorf("stdlib-statics: no builtin module named %q", moduleName)
	}
	var defs []dang.BuiltinDef
	dang.ForEachStaticMethod(mod, func(d dang.BuiltinDef) {
		defs = append(defs, d)
	})
	return stdlibList(defs, moduleName+"."), nil
}

func receiverByName(name string) *dang.Type {
	for _, t := range dang.MethodReceivers() {
		if t.Named == name {
			return t
		}
	}
	return nil
}

func moduleByName(name string) *dang.Type {
	for _, t := range dang.StaticModules() {
		if t.Named == name {
			return t
		}
	}
	return nil
}

// stdlibList renders builtin definitions as a bullet list of
// "`signature` — description" entries, sorted alphabetically by name.
func stdlibList(defs []dang.BuiltinDef, prefix string) booklit.Content {
	sort.SliceStable(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})

	items := make([]booklit.Content, 0, len(defs))
	for _, d := range defs {
		entry := booklit.Sequence{
			booklit.Styled{
				Style:   booklit.StyleVerbatim,
				Content: booklit.String(signature(d, prefix)),
			},
		}
		if d.Doc != "" {
			entry = append(entry, booklit.String(" — "+d.Doc))
		}
		items = append(items, entry)
	}

	return booklit.List{Items: items}
}

// signature renders a builtin's call signature, e.g.
// ".split(separator: String!, limit: Int = 0) -> [String!]!".
func signature(d dang.BuiltinDef, prefix string) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(d.Name)

	if len(d.ParamTypes) > 0 {
		b.WriteString("(")
		for i, param := range d.ParamTypes {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(param.Name)
			b.WriteString(": ")
			b.WriteString(renderType(param.Type))
			if param.DefaultValue != nil {
				b.WriteString(" = ")
				b.WriteString(renderDefault(param.DefaultValue))
			}
		}
		b.WriteString(")")
	}

	if d.BlockType != nil {
		b.WriteString(" ")
		b.WriteString(renderBlock(d.BlockType))
	}

	if d.ReturnType != nil {
		b.WriteString(" -> ")
		b.WriteString(renderType(d.ReturnType))
	}

	return b.String()
}

// renderBlock renders a block parameter, e.g. "{ item, index => b }". A block
// with no parameters renders as just its body type, e.g. "{ Boolean! }".
func renderBlock(ft *hm.FunctionType) string {
	var params []string
	if rec, ok := ft.Arg().(*dang.RecordType); ok {
		for _, f := range rec.Fields {
			params = append(params, f.Key)
		}
	}
	body := renderType(ft.ReturnType())
	if len(params) == 0 {
		return "{ " + body + " }"
	}
	return "{ " + strings.Join(params, ", ") + " => " + body + " }"
}

// renderType formats a type for a signature. The null-returning builtins are
// typed with a fresh type variable (NullValue.Type()); surface that as Null.
func renderType(t hm.Type) string {
	if tv, ok := t.(hm.TypeVariable); ok && tv == hm.TypeVariable('n') {
		return "Null"
	}
	return fmt.Sprintf("%s", t)
}

func renderDefault(v dang.Value) string {
	if s, ok := v.(dang.StringValue); ok {
		return fmt.Sprintf("%q", s.Val)
	}
	return v.String()
}
