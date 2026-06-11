//go:build !cgo

package dangdocs

import "github.com/vito/booklit"

// Without cgo there is no tree-sitter grammar: snippets render as plain code
// (and ```dang fences fall through to baselit, since the CodeBlock override
// is cgo-only).

func (p Plugin) highlightDang(src string) booklit.Content {
	return booklit.Styled{Style: booklit.StyleCodeFlow, Content: booklit.String(src)}
}

func (p Plugin) renderSignature(sig, name, tag string) booklit.Content {
	return booklit.Styled{Style: booklit.StyleCodeFlow, Content: booklit.String(sig)}
}
