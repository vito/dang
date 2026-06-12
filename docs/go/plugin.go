// Package dangdocs provides the Booklit plugin for the dang documentation
// site: custom functions, the stdlib reference generator, and tree-sitter
// syntax highlighting for every code block (see highlight.go).
package dangdocs

import (
	"github.com/vito/booklit"
)

func init() {
	booklit.RegisterPlugin("dang", NewPlugin)
}

// NewPlugin constructs a new dang docs plugin for the given section.
//
// The plugin is also prepended to the section's plugin stack: booklit
// resolves invoked functions first-match across the stack and baselit always
// sits first, so without the prepend our CodeBlock override (which renders
// every fence with tree-sitter highlighting and gives ```dang fences stdlib
// auto-links; see render.go) would be shadowed by baselit's chroma-based one.
func NewPlugin(section *booklit.Section) booklit.Plugin {
	p := Plugin{
		section: section,
	}
	section.Plugins = append([]booklit.Plugin{p}, section.Plugins...)
	return p
}

// Plugin provides custom functions for the dang documentation site.
type Plugin struct {
	section *booklit.Section
}

// Install renders a shell install command block.
//
//	\shell{go install github.com/vito/dang/v2/cmd/dang@latest}
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
