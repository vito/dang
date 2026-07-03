package dangdocs

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vito/booklit"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
)

// The standard library reference is generated straight from the builtin
// registry in pkg/dang (see stdlib.go, stdlib_path.go, stdlib_random.go,
// stdlib_regexp.go, assert.go). Each entry's signature comes from the registered parameter,
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
		// text. Title is the qualified name (e.g. List.uniq); it feeds the search
		// result title and the page table of contents, which lists each section's
		// anchor tags by their title (\table-of-contents). The full signature
		// still appears on the card itself, via renderSignature below. StripAux
		// dereferences these, so they must be non-nil — use Empty when a builtin
		// has no doc.
		var desc booklit.Content = booklit.Empty
		if d.Doc != "" {
			desc = booklit.String(d.Doc)
		}

		// For an instance method the receiver is implied by its section, so wrap
		// it in Aux: the ToC's stripAux drops it (showing `.uniq`) while the
		// search title's String() keeps it (`List.uniq`). Static modules and free
		// functions stay plain — `Random.float` is literally how it's called.
		var title booklit.Content
		if prefix == "." {
			title = booklit.Sequence{
				booklit.Aux{Content: booklit.String(qualifier)},
				booklit.String("." + d.Name),
			}
		} else {
			title = booklit.String(titlePrefix + d.Name)
		}

		cardPartials := booklit.Partials{
			"Target": booklit.Target{
				TagName:  tag,
				Location: p.section.InvokeLocation,
				Title:    title,
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

		// A deprecated builtin gets a right-aligned "@deprecated" badge in its
		// card header, linking to the replacement's own card when one is known.
		if d.Deprecated != "" {
			cardPartials["Deprecated"] = p.deprecatedBadge(d.Replacement)
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

// deprecatedBadge renders the "@deprecated" marker shown on a deprecated
// builtin's card header. When replacement names a superseding callable, it is
// appended as a link to that entry's own card (resolved by tag, so it works
// across the page's module sections).
func (p Plugin) deprecatedBadge(replacement string) booklit.Content {
	badge := booklit.Sequence{booklit.String("@deprecated")}
	if replacement != "" {
		badge = append(badge,
			booklit.String(" => "),
			&booklit.Reference{
				Section: p.section,
				TagName: replacementTag(replacement),
				Content: booklit.Styled{
					Style:   booklit.StyleVerbatim,
					Content: booklit.String(replacement),
				},
			},
		)
	}
	return badge
}

// replacementTag maps a replacement callable name to the anchor tag of its
// stdlib card. A qualified name like "JSON.encode" or "String.toBase64" splits
// into the section key and member ("JSON"/"encode"); a bare name is a top-level
// function, whose cards use the "fn" key.
func replacementTag(replacement string) string {
	if i := strings.LastIndex(replacement, "."); i >= 0 {
		return stdlibTag(replacement[:i], replacement[i+1:])
	}
	return stdlibTag("fn", replacement)
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
