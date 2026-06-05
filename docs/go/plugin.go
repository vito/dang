// Package dangdocs provides a Booklit plugin for the dang documentation site.
//
// It registers both the "dang" plugin (custom functions for the site) and the
// "chroma" plugin (syntax highlighting), so the build only needs a single
// import.
package dangdocs

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/vito/booklit"
	chromap "github.com/vito/booklit/chroma"
)

func init() {
	booklit.RegisterPlugin("chroma", chromap.NewPlugin)
	booklit.RegisterPlugin("dang", NewPlugin)

	// A dark color scheme that matches the dang docs aesthetic.
	styles.Fallback = chroma.MustNewStyle("dang", chroma.StyleEntries{
		chroma.Background:      "#c9d1d9 bg:#0d1117",
		chroma.Keyword:         "#ff7b72 bold",
		chroma.KeywordConstant: "#ff7b72",
		chroma.KeywordType:     "#79c0ff nobold",
		chroma.NameFunction:    "#d2a8ff",
		chroma.NameBuiltin:     "#d2a8ff",
		chroma.NameOther:       "#ffa657",
		chroma.NameTag:         "#7ee787",
		chroma.LiteralString:   "#a5d6ff",
		chroma.LiteralNumber:   "#79c0ff",
		chroma.Operator:        "#ff7b72",
		chroma.Punctuation:     "#c9d1d9",
		chroma.Comment:         "#6e7681 italic",
		chroma.CommentPreproc:  "#ff7b72 noitalic",
		chroma.GenericEmph:     "italic",
		chroma.GenericStrong:   "bold",
	})
}

// NewPlugin constructs a new dang docs plugin for the given section.
func NewPlugin(section *booklit.Section) booklit.Plugin {
	return Plugin{section: section}
}

// Plugin provides custom functions for the dang documentation site.
type Plugin struct {
	section *booklit.Section
}

// Install renders a shell install command block.
//
//	\shell{go install github.com/vito/dang/cmd/dang@latest}
func (p Plugin) Shell(content booklit.Content) booklit.Content {
	return booklit.Styled{
		Style:   "shell",
		Content: content,
		Block:   true,
	}
}

// Screenshot renders an image that fits within its parent container.
//
//	\screenshot{img/debugui.png}{debug UI dashboard}
func (p Plugin) Screenshot(src, alt booklit.Content) booklit.Content {
	return booklit.Styled{
		Style:   "screenshot",
		Content: alt,
		Partials: booklit.Partials{
			"Src": src,
		},
		Block: true,
	}
}

// ThematicBreak renders a markdown horizontal rule (`---`).
func (p Plugin) ThematicBreak() booklit.Content {
	return booklit.Styled{
		Style:   "thematic-break",
		Content: booklit.Empty,
		Block:   true,
	}
}

// DangPlayground renders an interactive, editable Dang snippet that runs
// client-side via the WebAssembly module (see cmd/dang-playground). The code
// is passed verbatim so braces and quotes survive:
//
//	\dang-playground{{{
//	[1, 2, 3].map { x => x * 2 }
//	}}}
//
// Without JavaScript it degrades to a plain (non-highlighted) code block.
func (p Plugin) DangPlayground(code booklit.Content) booklit.Content {
	return booklit.Styled{
		Style:   "dang-playground",
		Content: code,
		Block:   true,
	}
}

// DangGitHubPlayground renders a playground that can `import GitHub`. It behaves
// like \dang-playground but adds a "Sign in with GitHub" control; once the
// reader authorizes (OAuth web flow, see docs/functions/github), the snippet's
// `import GitHub` resolves against the live GitHub GraphQL schema, queried
// straight from the browser.
//
//	\dang-github-playground{{{
//	import GitHub
//	viewer.{ login, name }
//	}}}
func (p Plugin) DangGithubPlayground(code booklit.Content) booklit.Content {
	return booklit.Styled{
		Style:   "dang-github-playground",
		Content: code,
		Block:   true,
	}
}

// DangRepl renders an interactive Dang REPL that evaluates entries
// client-side via the WebAssembly module (see cmd/dang-playground). State
// accumulates across entries within a session, just like the CLI REPL. The
// code is the seed for the first input, passed verbatim so braces and quotes
// survive:
//
//	\dang-repl{{{
//	[1, 2, 3].map { x => x * 2 }
//	}}}
//
// Without JavaScript it degrades to a plain (non-highlighted) code block.
func (p Plugin) DangRepl(code booklit.Content) booklit.Content {
	return booklit.Styled{
		Style:   "dang-repl",
		Content: code,
		Block:   true,
	}
}

// HeaderLinks renders a horizontal row of navigation links.
//
//	\header-links{
//	  \link{GitHub}{https://github.com/vito/dang}
//	}{
//	  \link{pkg.go.dev}{https://pkg.go.dev/github.com/vito/dang}
//	}
func (p Plugin) HeaderLinks(links ...booklit.Content) booklit.Content {
	return booklit.Styled{
		Style:   "header-links",
		Content: booklit.Sequence(links),
		Block:   true,
	}
}
