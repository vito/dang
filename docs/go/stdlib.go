package dangdocs

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/vito/booklit"
	"github.com/vito/booklit/baselit"
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
// Signatures are highlighted at build time with the same "dang" lexer and
// style as the site's code blocks. The lexer tokenizes with the Dang
// tree-sitter grammar and re-parses declaration-style signatures — which
// aren't complete Dang programs — inside a synthetic interface body (see
// lexer.go), so they highlight like real declarations.

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
		sigToks := signatureTokens(d, prefix)

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
			"Reference": &booklit.Reference{Section: p.section, TagName: tag},
		}
		rowPartials := booklit.Partials{}
		if d.Doc != "" {
			cardPartials["Description"] = desc
			rowPartials["Description"] = desc
		}

		// A builtin's example becomes a pre-seeded, runnable REPL on the card
		// (see exampleRepl). The index rows stay terse — signature + one-liner.
		if d.Example != "" {
			cardPartials["Example"] = exampleRepl(d.Example)
		}

		cards = append(cards, booklit.Styled{
			Style:    "stdlib-entry",
			Block:    true,
			Content:  highlightTokens(sigToks),
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
// snippet is chroma-highlighted at build time so it reads as a normal code
// block (and stays useful without JavaScript); docs/js/playground.js upgrades
// it into a live REPL — seeded with this code — on first Run. See the
// "stdlib-example" template and \dang-repl.
func exampleRepl(code string) booklit.Content {
	return booklit.Styled{
		Style:   "stdlib-example",
		Block:   true,
		Content: highlightDang(code),
	}
}

// highlightDang renders a snippet of Dang as inline, syntax-highlighted HTML
// using the same chroma "dang" lexer and style (styles.Fallback) as the site's
// code blocks. It mirrors the site's class/inline choice
// (baselit.HighlightWithClasses): in class mode the colors and code background
// come from chroma.css, so the snippet themes with the rest of the page. It
// backs both the signature cards and the example REPLs; the lexer handles
// partial declarations and whole expressions alike.
func highlightDang(src string) booklit.Content {
	plain := booklit.Styled{Style: booklit.StyleCodeFlow, Content: booklit.String(src)}

	lexer := lexers.Get("dang")
	if lexer == nil {
		return plain
	}
	iterator, err := lexer.Tokenise(nil, src)
	if err != nil {
		return plain
	}
	return formatHighlighted(iterator, plain)
}

// highlightTokens renders pre-classified tokens (stdlib signatures) through
// the same formatter and theming as lexed snippets.
func highlightTokens(tokens []chroma.Token) booklit.Content {
	plain := booklit.Styled{Style: booklit.StyleCodeFlow, Content: booklit.String(tokensString(tokens))}
	return formatHighlighted(chroma.Literator(tokens...), plain)
}

func formatHighlighted(iterator chroma.Iterator, plain booklit.Content) booklit.Content {
	opts := []chromahtml.Option{chromahtml.InlineCode(true)}
	if baselit.HighlightWithClasses {
		opts = append(opts, chromahtml.WithClasses(true))
	}
	var buf bytes.Buffer
	if err := chromahtml.New(opts...).Format(&buf, styles.Fallback, iterator); err != nil {
		return plain
	}
	return booklit.Styled{Style: "raw-html", Content: booklit.String(buf.String())}
}

// signature renders a builtin's call signature in Dang declaration form, e.g.
// ".split(separator: String!, limit: Int = 0): [String!]!".
func signature(d dang.BuiltinDef, prefix string) string {
	return tokensString(signatureTokens(d, prefix))
}

// signatureTokens renders a builtin's signature as pre-classified chroma
// tokens, in real declaration form: a block parameter is written `&block(args):
// Type`, last in the argument list, exactly as user code declares one — so
// signature cards double as examples of declaring block-taking functions.
// TestSignaturesAreValidDeclarations pins this. Tokens are still built from
// the structured definition rather than lexed, both for exactness and because
// the display prefix (".", "List.") isn't part of the declaration proper.
func signatureTokens(d dang.BuiltinDef, prefix string) []chroma.Token {
	var toks []chroma.Token
	if prefix != "" {
		if owner := strings.TrimSuffix(prefix, "."); owner != "" {
			toks = append(toks, typeNameToken(owner))
		}
		toks = append(toks, punctTok("."))
	}
	toks = append(toks, chroma.Token{Type: chroma.NameFunction, Value: d.Name})

	if len(d.ParamTypes) > 0 || d.BlockType != nil {
		toks = append(toks, punctTok("("))
		for i, param := range d.ParamTypes {
			if i > 0 {
				toks = append(toks, punctTok(","), textTok(" "))
			}
			toks = append(toks, chroma.Token{Type: chroma.Name, Value: param.Name}, punctTok(":"), textTok(" "))
			toks = append(toks, typeTokens(renderType(param.Type))...)
			if param.DefaultValue != nil {
				toks = append(toks, textTok(" "), chroma.Token{Type: chroma.Operator, Value: "="}, textTok(" "))
				toks = append(toks, defaultTokens(renderDefault(param.DefaultValue))...)
			}
		}
		if d.BlockType != nil {
			if len(d.ParamTypes) > 0 {
				toks = append(toks, punctTok(","), textTok(" "))
			}
			toks = append(toks, blockParamTokens(d.BlockType)...)
		}
		toks = append(toks, punctTok(")"))
	}

	if d.ReturnType != nil {
		toks = append(toks, punctTok(":"), textTok(" "))
		toks = append(toks, typeTokens(renderType(d.ReturnType))...)
	}

	return toks
}

// blockParamTokens renders a block parameter in declaration form, e.g.
// "&block(item: a!): Boolean!", or "&block: a" for a block taking no
// arguments.
func blockParamTokens(ft *hm.FunctionType) []chroma.Token {
	toks := []chroma.Token{
		{Type: chroma.Operator, Value: "&"},
		{Type: chroma.Name, Value: "block"},
	}
	if rec, ok := ft.Arg().(*dang.RecordType); ok && len(rec.Fields) > 0 {
		toks = append(toks, punctTok("("))
		for i, f := range rec.Fields {
			if i > 0 {
				toks = append(toks, punctTok(","), textTok(" "))
			}
			toks = append(toks, chroma.Token{Type: chroma.Name, Value: f.Key}, punctTok(":"), textTok(" "))
			argType := f.Value.String()
			if t, ok := f.Value.Type(); ok {
				argType = renderType(t)
			}
			toks = append(toks, typeTokens(argType)...)
		}
		toks = append(toks, punctTok(")"))
	}
	toks = append(toks, punctTok(":"), textTok(" "))
	toks = append(toks, typeTokens(renderType(ft.ReturnType()))...)
	return toks
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
