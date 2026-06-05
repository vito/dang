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
//
// The layout mirrors vito/bass's stdlib page: every definition becomes an
// anchored card titled by its signature, and a long group is preceded by a
// quick-scan index. We deliberately leave out bass-isms that don't apply to
// Dang (per-line source links, type predicates).

// indexThreshold is the group size above which a quick-scan index is rendered
// before the cards. Small groups read fine as bare cards.
const indexThreshold = 8

// StdlibFunctions renders the top-level builtin functions (alphabetical).
//
//	\stdlib-functions
func (p Plugin) StdlibFunctions() booklit.Content {
	var defs []dang.BuiltinDef
	dang.ForEachFunction(func(d dang.BuiltinDef) {
		defs = append(defs, d)
	})
	return stdlibModule(defs, "", "fn")
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
	return stdlibModule(defs, ".", typeName), nil
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
	return stdlibModule(defs, moduleName+".", moduleName), nil
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

// stdlibModule renders a group of builtin definitions, sorted alphabetically,
// as anchored signature cards. prefix is prepended to each name to form its
// callable form (e.g. "." for methods, "Random." for statics); key namespaces
// the anchors so identically-named entries across groups stay unique.
func stdlibModule(defs []dang.BuiltinDef, prefix, key string) booklit.Content {
	sort.SliceStable(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})

	cards := make(booklit.Sequence, 0, len(defs))
	rows := make(booklit.Sequence, 0, len(defs))
	for _, d := range defs {
		tag := stdlibTag(key, d.Name)

		cardPartials := booklit.Partials{"Tag": booklit.String(tag)}
		rowPartials := booklit.Partials{"Tag": booklit.String(tag)}
		if d.Doc != "" {
			cardPartials["Description"] = booklit.String(d.Doc)
			rowPartials["Description"] = booklit.String(d.Doc)
		}

		cards = append(cards, booklit.Styled{
			Style:    "stdlib-entry",
			Block:    true,
			Content:  booklit.String(signature(d, prefix)),
			Partials: cardPartials,
		})
		rows = append(rows, booklit.Styled{
			Style:    "stdlib-index-entry",
			Block:    true,
			Content:  booklit.String(prefix + d.Name),
			Partials: rowPartials,
		})
	}

	partials := booklit.Partials{}
	if len(defs) > indexThreshold {
		partials["Index"] = booklit.Styled{
			Style:   "stdlib-index",
			Block:   true,
			Content: rows,
		}
	}

	return booklit.Styled{
		Style:    "stdlib-module",
		Block:    true,
		Content:  cards,
		Partials: partials,
	}
}

func stdlibTag(key, name string) string {
	return "stdlib-" + key + "-" + name
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
