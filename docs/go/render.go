//go:build cgo

package dangdocs

import (
	"html"
	"strings"

	"github.com/vito/booklit"
)

// CodeBlock intercepts ```dang fences and renders them with the tree-sitter
// lexer plus stdlib auto-links: the snippet is typechecked against the
// prelude and every call-position name that resolves to a stdlib builtin
// becomes a reference into the stdlib docs. Other languages keep baselit's
// chroma path. Booklit resolves plugin methods first-match across the
// section's plugin stack and baselit always sits first, so NewPlugin prepends
// this plugin to make this override reachable; without cgo this method
// doesn't exist at all and fences fall through to baselit.
func (p Plugin) CodeBlock(language string, code booklit.Content, styleName ...string) (booklit.Content, error) {
	if language != "dang" {
		return p.base.CodeBlock(language, code, styleName...)
	}

	source := code.String()

	style := booklit.StyleCodeBlock
	if code.IsFlow() {
		style = booklit.StyleCodeFlow
	}

	return booklit.Styled{
		Style:   style,
		Block:   !code.IsFlow(),
		Content: renderDang(p.section, source, stdlibLinks(source), code.IsFlow()),
		Partials: booklit.Partials{
			"Language": booklit.String(language),
		},
	}, nil
}

// highlightDang renders a snippet of Dang as inline highlighted content with
// stdlib auto-links, themed by chroma.css like the site's code blocks. It
// backs the stdlib example REPLs and any other generated snippet.
func (p Plugin) highlightDang(src string) booklit.Content {
	return renderDang(p.section, src, stdlibLinks(src), true)
}

// renderSignature renders a stdlib card's declaration with exactly one link:
// the declared name, pointing at the card's own tag. Signatures are
// declaration positions, so they get no typecheck-driven auto-links.
func (p Plugin) renderSignature(sig, name, tag string) booklit.Content {
	links := []linkSpan{{start: 0, end: len(name), tag: tag}}
	return renderDang(p.section, sig, links, true)
}

// renderDang emits highlighted Dang as a sequence of raw HTML fragments
// interleaved with booklit references for the link spans. References render
// as plain <a> elements inside the surrounding token <span>, so links keep
// their token color (chroma.css underlines them on hover). Falls back to a
// plain code style when the highlight query is unavailable.
func renderDang(section *booklit.Section, source string, links []linkSpan, inline bool) booklit.Content {
	classes := dangLexer.classify(source)
	if classes == nil {
		return booklit.Styled{Style: booklit.StyleVerbatim, Content: booklit.String(source)}
	}

	// linkAt[i] is the index into links covering byte i, or -1.
	linkAt := make([]int, len(source))
	for i := range linkAt {
		linkAt[i] = -1
	}
	for li, l := range links {
		for b := l.start; b < l.end && b < len(source); b++ {
			linkAt[b] = li
		}
	}

	var seq booklit.Sequence
	var raw strings.Builder

	flush := func() {
		if raw.Len() > 0 {
			seq = append(seq, booklit.Styled{Style: "raw-html", Content: booklit.String(raw.String())})
			raw.Reset()
		}
	}

	if inline {
		raw.WriteString(`<code class="chroma">`)
	} else {
		raw.WriteString(`<pre class="chroma"><code>`)
	}

	for i := 0; i < len(source); {
		j := i + 1
		for j < len(source) && classes[j] == classes[i] && linkAt[j] == linkAt[i] {
			j++
		}
		text := source[i:j]
		cls := classes[i]

		if li := linkAt[i]; li >= 0 {
			if cls != "" {
				raw.WriteString(`<span class="` + cls + `">`)
			}
			flush()
			seq = append(seq, &booklit.Reference{
				Section:  section,
				TagName:  links[li].tag,
				Content:  booklit.String(text),
				Optional: true,
				Location: section.InvokeLocation,
			})
			if cls != "" {
				raw.WriteString(`</span>`)
			}
		} else if cls != "" {
			raw.WriteString(`<span class="` + cls + `">` + html.EscapeString(text) + `</span>`)
		} else {
			raw.WriteString(html.EscapeString(text))
		}
		i = j
	}

	if inline {
		raw.WriteString(`</code>`)
	} else {
		raw.WriteString(`</code></pre>`)
	}
	flush()

	return seq
}
