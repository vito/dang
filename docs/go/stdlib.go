package dangdocs

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/vito/booklit"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
)

// The standard library reference is generated straight from the builtin
// registry in pkg/dang (see stdlib.go, stdlib_random.go, stdlib_regexp.go,
// assert.go). Each entry's signature comes from the registered parameter,
// block, and return types; its description comes from the builtin's .Doc(...),
// and its pre-seeded REPL from the builtin's .Example(...). Editing a builtin
// updates this page — there is nothing to hand-maintain.
//
// The layout mirrors vito/bass's stdlib page: every definition becomes an
// anchored card titled by its signature, and a long group is preceded by a
// quick-scan index. We deliberately leave out bass-isms that don't apply to
// Dang (per-line source links, type predicates).
//
// Signatures are highlighted at build time with the same Dang tree-sitter
// grammar and style as the site's code blocks. The highlighter re-parses
// declaration-style signatures — which aren't complete Dang programs —
// inside a synthetic interface body (see highlight.go), so they highlight
// like real declarations.

// indexThreshold is the group size above which a quick-scan index is rendered
// before the cards. Small groups read fine as bare cards.
const indexThreshold = 8

// tocGroup describes one section of the stdlib reference for the page-level
// table of contents: its heading title, the registry key used to build entry
// anchors, and how to enumerate its entries.
type tocGroup struct {
	title string // must match the section's `##`/`###` heading (sans code ticks)
	key   string // anchor namespace: "fn", or a receiver/module type name
	kind  string // "functions" | "methods" | "statics"
	// prefix is prepended to each entry's display name (".split", "JSON.encode").
}

// tocGroups lists the reference's sections in page order. The exhaustiveness
// guard in StdlibToc fails the build if a registry receiver or static module is
// missing here, so the ToC can't silently fall behind the page.
var tocGroups = []tocGroup{
	{title: "Top-level functions", key: "fn", kind: "functions"},
	{title: "String! methods", key: "String", kind: "methods"},
	{title: "Match object", key: "Match", kind: "methods"},
	{title: "[T]! methods", key: "List", kind: "methods"},
	{title: "Map[a]! methods", key: "Map", kind: "methods"},
	{title: "JSON module", key: "JSON", kind: "statics"},
	{title: "YAML module", key: "YAML", kind: "statics"},
	{title: "TOML module", key: "TOML", kind: "statics"},
	{title: "Random module", key: "Random", kind: "statics"},
	{title: "UUID module", key: "UUID", kind: "statics"},
}

// StdlibToc renders a compact, page-level table of contents: each section links
// to its heading, and every documented entry links to its card anchor — a
// word-cloud index so a reader can jump straight to a method. It is generated
// from the same builtin registry as the cards, so it stays exhaustive; the
// per-entry links are booklit.References, so a stale anchor fails the build.
//
//	\stdlib-toc
func (p Plugin) StdlibToc() (booklit.Content, error) {
	covered := map[string]bool{}
	for _, g := range tocGroups {
		covered[g.key] = true
	}
	for _, r := range dang.MethodReceivers() {
		if !covered[r.Named] {
			return nil, fmt.Errorf("stdlib-toc: receiver %q is missing from tocGroups", r.Named)
		}
	}
	for _, m := range dang.StaticModules() {
		if !covered[m.Named] {
			return nil, fmt.Errorf("stdlib-toc: module %q is missing from tocGroups", m.Named)
		}
	}

	groups := make(booklit.Sequence, 0, len(tocGroups))
	for _, g := range tocGroups {
		defs, prefix, err := p.tocGroupDefs(g)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })

		entries := make(booklit.Sequence, 0, len(defs))
		for _, d := range defs {
			entries = append(entries, &booklit.Reference{
				Section: p.section,
				TagName: stdlibTag(g.key, d.Name),
				Content: booklit.Styled{
					Style:   booklit.StyleVerbatim,
					Content: booklit.String(prefix + d.Name),
				},
			})
		}

		groups = append(groups, booklit.Styled{
			Style:   "stdlib-toc-group",
			Block:   true,
			Content: entries,
			Partials: booklit.Partials{
				"Title": &booklit.Reference{
					Section: p.section,
					TagName: sectionSlug(g.title),
					Content: booklit.String(g.title),
				},
			},
		})
	}

	return booklit.Styled{
		Style:   "stdlib-toc",
		Block:   true,
		Content: groups,
	}, nil
}

// tocGroupDefs enumerates a group's builtin definitions and the display prefix
// for its entries (matching the cards: "" for functions, "." for methods,
// "Module." for statics).
func (p Plugin) tocGroupDefs(g tocGroup) ([]dang.BuiltinDef, string, error) {
	var defs []dang.BuiltinDef
	switch g.kind {
	case "functions":
		dang.ForEachFunction(func(d dang.BuiltinDef) { defs = append(defs, d) })
		return defs, "", nil
	case "methods":
		recv := receiverByName(g.key)
		if recv == nil {
			return nil, "", fmt.Errorf("stdlib-toc: no builtin method receiver named %q", g.key)
		}
		dang.ForEachMethod(recv, func(d dang.BuiltinDef) { defs = append(defs, d) })
		return defs, ".", nil
	case "statics":
		mod := moduleByName(g.key)
		if mod == nil {
			return nil, "", fmt.Errorf("stdlib-toc: no builtin module named %q", g.key)
		}
		dang.ForEachStaticMethod(mod, func(d dang.BuiltinDef) { defs = append(defs, d) })
		return defs, g.key + ".", nil
	default:
		return nil, "", fmt.Errorf("stdlib-toc: unknown group kind %q", g.kind)
	}
}

var tocWhitespace = regexp.MustCompile(`\s+`)
var tocSpecialChars = regexp.MustCompile(`[^[:alnum:]_\-]`)

// sectionSlug reproduces booklit's Section.defaultTag so a group can link to
// its heading's auto-generated anchor (e.g. "String! methods" -> string-methods).
func sectionSlug(title string) string {
	s := strings.ReplaceAll(title, " & ", " and ")
	s = tocWhitespace.ReplaceAllString(s, "-")
	s = tocSpecialChars.ReplaceAllString(s, "")
	return strings.ToLower(s)
}

// StdlibFunctions renders the top-level builtin functions (alphabetical).
//
//	\stdlib-functions
func (p Plugin) StdlibFunctions() booklit.Content {
	var defs []dang.BuiltinDef
	dang.ForEachFunction(func(d dang.BuiltinDef) {
		defs = append(defs, d)
	})
	return p.stdlibModule(defs, "", "fn", "")
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
	return p.stdlibModule(defs, ".", typeName, typeName), nil
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
	return p.stdlibModule(defs, moduleName+".", moduleName, moduleName), nil
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
// displayed callable form (e.g. "." for methods, "Random." for statics); key
// namespaces the anchors so identically-named entries across groups stay
// unique. qualifier is the receiver/module type name ("" for free functions);
// it is used to fully qualify the search-index title so a result reads
// "List.uniq: [a]!" rather than the bare ".uniq: [a]!".
//
// Each card carries a booklit.Target so its signature and description land in
// the search index and become a linkable anchor; the index and the card's own
// title link to it via booklit.Reference.
func (p Plugin) stdlibModule(defs []dang.BuiltinDef, prefix, key, qualifier string) booklit.Content {
	sort.SliceStable(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})

	titlePrefix := ""
	if qualifier != "" {
		titlePrefix = qualifier + "."
	}

	cards := make(booklit.Sequence, 0, len(defs))
	rows := make(booklit.Sequence, 0, len(defs))
	for _, d := range defs {
		tag := stdlibTag(key, d.Name)

		// Target.Content (and the card/row Description) feed the search index's
		// text; Title feeds its title. StripAux dereferences these, so they must
		// be non-nil — use Empty when a builtin has no doc.
		var desc booklit.Content = booklit.Empty
		if d.Doc != "" {
			desc = booklit.String(d.Doc)
		}

		cardPartials := booklit.Partials{
			"Target": booklit.Target{
				TagName:  tag,
				Location: p.section.InvokeLocation,
				Title:    booklit.String(signature(d, titlePrefix)),
				Content:  desc,
			},
		}
		// The receiver/module context (".", "Random.") is display labeling,
		// not part of the declaration; the template renders it ahead of the
		// highlighted code.
		if prefix != "" {
			cardPartials["Prefix"] = booklit.String(prefix)
		}
		rowPartials := booklit.Partials{}
		if d.Doc != "" {
			cardPartials["Description"] = desc
			rowPartials["Description"] = desc
		}

		// A builtin's example becomes a pre-seeded, runnable REPL on the card
		// (see exampleRepl). The index rows stay terse — signature + one-liner.
		if d.Example != "" {
			cardPartials["Example"] = p.exampleRepl(d.Example)
		}

		cards = append(cards, booklit.Styled{
			Style:    "stdlib-entry",
			Block:    true,
			Content:  p.renderSignature(signature(d, ""), d.Name, tag),
			Partials: cardPartials,
		})
		rows = append(rows, booklit.Styled{
			Style: "stdlib-index-entry",
			Block: true,
			Content: &booklit.Reference{
				Section: p.section,
				TagName: tag,
				Content: booklit.Styled{
					Style:   booklit.StyleVerbatim,
					Content: booklit.String(prefix + d.Name),
				},
			},
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

// exampleRepl renders a builtin's example as a pre-seeded, runnable REPL. The
// snippet is highlighted (and stdlib auto-linked) at build time so it reads
// as a normal code block and stays useful without JavaScript;
// docs/js/playground.js upgrades it into a live REPL — seeded with this
// code — on first Run. See the "stdlib-example" template and \dang-repl.
func (p Plugin) exampleRepl(code string) booklit.Content {
	return booklit.Styled{
		Style:   "stdlib-example",
		Block:   true,
		Content: p.highlightDang(code),
	}
}

// signature renders a builtin's signature in real Dang declaration form: a
// block parameter is written `&block(args): Type`, last in the argument list,
// exactly as user code declares one — so signature cards double as examples
// of declaring block-taking functions, and the "dang" lexer can highlight
// them by parsing like any other snippet (via its interface-body fragment
// handling). TestSignaturesAreValidDeclarations pins the notation. prefix is
// display context (".", "List."), not part of the declaration — cards render
// it separately and lex only the unprefixed form.
func signature(d dang.BuiltinDef, prefix string) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(d.Name)

	if len(d.ParamTypes) > 0 || d.BlockType != nil {
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
		if d.BlockType != nil {
			if len(d.ParamTypes) > 0 {
				b.WriteString(", ")
			}
			b.WriteString(renderBlockParam(d.BlockType))
		}
		b.WriteString(")")
	}

	if d.ReturnType != nil {
		b.WriteString(": ")
		b.WriteString(renderType(d.ReturnType))
	}

	return b.String()
}

// renderBlockParam renders a block parameter in declaration form, e.g.
// "&block(item: a): Boolean!", or "&block: a" for a block taking no
// arguments.
func renderBlockParam(ft *hm.FunctionType) string {
	var b strings.Builder
	b.WriteString("&block")
	if rec, ok := ft.Arg().(*dang.RecordType); ok && len(rec.Fields) > 0 {
		b.WriteString("(")
		for i, f := range rec.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.Key)
			b.WriteString(": ")
			argType := f.Value.String()
			if t, ok := f.Value.Type(); ok {
				argType = renderType(t)
			}
			b.WriteString(argType)
		}
		b.WriteString(")")
	}
	b.WriteString(": ")
	b.WriteString(renderType(ft.ReturnType()))
	return b.String()
}

// renderType formats a type for a signature. The null-returning builtins are
// typed with a fresh type variable (NullValue.Type()); surface that as Null.
// Nullable type variables print as `a?` internally, but Dang's surface syntax
// has no `?` — nullable is the unmarked default — so strip it to keep
// signatures valid declarations.
func renderType(t hm.Type) string {
	if tv, ok := t.(hm.TypeVariable); ok && tv == hm.TypeVariable('n') {
		return "Null"
	}
	return strings.ReplaceAll(fmt.Sprintf("%s", t), "?", "")
}

func renderDefault(v dang.Value) string {
	if s, ok := v.(dang.StringValue); ok {
		return fmt.Sprintf("%q", s.Val)
	}
	return v.String()
}
