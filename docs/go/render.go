package dangdocs

import (
	"html"
	"strings"

	"github.com/vito/booklit"
)

// CodeBlock renders every fenced code block with tree-sitter highlighting
// (see highlight.go for the registered grammars). ```dang fences additionally
// get stdlib auto-links: the snippet is typechecked against the prelude and
// every call-position name that resolves to a stdlib builtin becomes a
// reference into the stdlib docs. Languages without a grammar — and every
// language when built without cgo — render as plain code in the same wrapper.
// Booklit resolves plugin methods first-match across the section's plugin
// stack and baselit always sits first, so NewPlugin prepends this plugin to
// make this override reachable.
//
// Inside a \literate-fences scope (see literate.go) a ```dang fence instead
// becomes a literate block — evaluated at build time in the page's shared
// session, output baked in. ```dang-failure marks a fence that is REQUIRED
// to fail: it runs against forks of the same session and bakes the error it
// raises (see DangLiterateFailure). ```dang-static opts a single fence back
// out: it highlights (and auto-links) as Dang but is never evaluated, for
// fragments that can't run at all. Outside a literate scope both extra tags
// just highlight as Dang.
func (p Plugin) CodeBlock(language string, code booklit.Content, styleName ...string) (booklit.Content, error) {
	switch language {
	case "dang":
		if literateFencesEnabled(p.section) {
			return p.literateBlock(code, "```dang fence")
		}
	case "dang-failure":
		if literateFencesEnabled(p.section) {
			return p.literateFailureBlock(code, "```dang-failure fence")
		}
		language = "dang"
	case "dang-static":
		language = "dang"
	}

	source := code.String()

	var links []linkSpan
	if language == "dang" {
		links = stdlibLinks(source)
	}

	style := booklit.StyleCodeBlock
	if code.IsFlow() {
		style = booklit.StyleCodeFlow
	}

	return booklit.Styled{
		Style:   style,
		Block:   !code.IsFlow(),
		Content: renderCode(p.section, language, source, links, code.IsFlow()),
		Partials: booklit.Partials{
			"Language": booklit.String(language),
		},
	}, nil
}

// highlightDang renders a snippet of Dang as inline highlighted content with
// stdlib auto-links, themed by syntax.css like the site's code blocks. It
// backs the stdlib example REPLs and any other generated snippet.
func (p Plugin) highlightDang(src string) booklit.Content {
	return renderCode(p.section, "dang", src, stdlibLinks(src), true)
}

// renderSignature renders a stdlib card's declaration with exactly one link:
// the declared name, pointing at the card's own tag. Signatures are
// declaration positions, so they get no typecheck-driven auto-links.
func (p Plugin) renderSignature(sig, name, tag string) booklit.Content {
	links := []linkSpan{{start: 0, end: len(name), tag: tag}}
	return renderCode(p.section, "dang", sig, links, true)
}

// renderCode emits highlighted code as a sequence of raw HTML fragments
// interleaved with booklit references for the link spans. References render
// as plain <a> elements inside the surrounding token <span>, so links keep
// their token color (page.tmpl underlines them on hover). When the language
// has no grammar (or the build has no cgo) the code renders plain — same
// wrapper, no token spans — so every fence shares this one path.
func renderCode(section *booklit.Section, language, source string, links []linkSpan, inline bool) booklit.Content {
	classes := classifyCode(language, source)
	if classes == nil {
		classes = make([]string, len(source))
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
		raw.WriteString(`<code class="syntax">`)
	} else {
		raw.WriteString(`<pre class="syntax"><code>`)
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
