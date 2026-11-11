package dangdocs

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vito/booklit"
)

// Release-notes building blocks. The release notes are prose pages like any
// other, but they lean on a handful of illustrations the rest of the docs
// don't need: before/after pairs, verbatim program output quoted from an
// older Dang (which can't be evaluated at build time, since the docs run the
// current Dang), "applies to" pills, an upgrade checklist, fix-it boxes, and a
// call-to-action for a longer write-up. Each is a small Styled wrapper with a
// template under docs/html/ and styles in page.tmpl.

// pillKinds maps a \pill kind to what it marks. The kind picks the color
// (see .pill-* in page.tmpl); the text is the label.
var pillKinds = map[string]string{
	"lang":  "gated on a language version",
	"all":   "applies at every language version",
	"new":   "new feature",
	"patch": "added in a patch release",
	"info":  "neutral note",
	"go":    "Go API",
}

// Pill renders an inline badge saying who a change applies to, e.g. a
// section title's "every version" or "2.4 only":
//
//	## Values print as Dang literals \pill{all}{every version} {#v2.4-display}
//
// The kind is one of lang, all, new, patch, info or go; an unknown kind
// fails the build, so a typo can't silently lose its color.
func (p Plugin) Pill(kind, text booklit.Content) (booklit.Content, error) {
	k := strings.TrimSpace(kind.String())
	if _, ok := pillKinds[k]; !ok {
		var kinds []string
		for name := range pillKinds {
			kinds = append(kinds, name)
		}
		sort.Strings(kinds)
		return nil, fmt.Errorf(`\pill kind %q in %s: want one of %s`, k, p.section.FilePath(), strings.Join(kinds, ", "))
	}
	// Aux: the heading shows the pill, but tables of contents (stripAux in
	// toc.tmpl) list the bare title.
	return booklit.Aux{Content: booklit.Styled{
		Style:    "pill",
		Content:  text,
		Partials: booklit.Partials{"Kind": booklit.String(k)},
	}}, nil
}

// Lede renders a page's opening paragraph in a larger size: the summary a
// reader takes away if they read nothing else.
//
//	\lede{
//	  Dang 2.4 is a new language version. ...
//	}
func (p Plugin) Lede(content booklit.Content) booklit.Content {
	return booklit.Styled{Style: "lede", Content: content, Block: true}
}

// Steps renders a numbered checklist, one argument per step. A step may
// hold block content, such as a ```sh fence of the commands it runs.
//
//	\steps{
//	  **Migrate, or pin.** ...
//	}{
//	  **Run it.** ...
//	}
func (p Plugin) Steps(steps ...booklit.Content) booklit.Content {
	return booklit.Styled{
		Style:   "steps",
		Content: booklit.List{Items: steps, Ordered: true},
		Block:   true,
	}
}

// Pair renders two panes side by side (stacked on narrow screens): a
// program and its output, or the same code before and after a change.
//
//	\pair{
//	  \pane{Before}{v2.3}{
//	  ```dang
//	  ...
//	  ```
//	  }
//	}{
//	  \pane{After}{2.4}{...}
//	}
func (p Plugin) Pair(left, right booklit.Content) booklit.Content {
	return booklit.Styled{
		Style: "pair",
		// The template places the sides from the partials; the content
		// carries them too, since a Styled's text (what the search index
		// reads) is its content alone.
		Content:  booklit.Sequence{left, right},
		Partials: booklit.Partials{"Left": left, "Right": right},
		Block:    true,
	}
}

// Pane renders a labeled block: a small caps label, an optional note on
// the right of the label (which version, where it came from), and the
// content beneath. Use it inside \pair, or alone to caption a single block.
//
//	\pane{Output}{...}
//	\pane{Before}{v2.3 / pinned 2.1}{...}
func (p Plugin) Pane(label booklit.Content, rest ...booklit.Content) (booklit.Content, error) {
	partials := booklit.Partials{"Label": label}
	var content booklit.Content
	switch len(rest) {
	case 1:
		content = rest[0]
	case 2:
		partials["Note"] = rest[0]
		content = rest[1]
	default:
		return nil, fmt.Errorf(`\pane in %s: want \pane{label}{content} or \pane{label}{note}{content}, got %d arguments`, p.section.FilePath(), len(rest)+1)
	}
	return booklit.Styled{Style: "pane", Content: content, Partials: partials, Block: true}, nil
}

// Fix renders the remedy for a breaking change, set off in a box that
// starts with "Fix".
//
//	\fix{Where you meant JSON, say so: `JSON.encode(x)`.}
func (p Plugin) Fix(content booklit.Content) booklit.Content {
	return booklit.Styled{Style: "fix", Content: content, Block: true}
}

// Sourced renders a small note saying where an example's code or output
// came from when it wasn't run for the page: a test, a golden file, a pull
// request's description.
//
//	\sourced{Needs a GraphQL schema, so quoted from the test suite.}
func (p Plugin) Sourced(content booklit.Content) booklit.Content {
	return booklit.Styled{Style: "sourced", Content: content, Block: true}
}

// WriteUp renders a call to action for a longer write-up that lives
// elsewhere: its title, a sentence on what's in it, and a button.
//
//	\write-up{Closing soundness holes in Dang's type checker}{https://...}{
//	  A before/after gallery of every case, ...
//	}
func (p Plugin) WriteUp(title, url, description booklit.Content) booklit.Content {
	return booklit.Styled{
		Style: "write-up",
		// Title and description both go in the content too, so the search
		// index (which reads only a Styled's content) finds the write-up.
		Content: booklit.Sequence{title, description},
		Partials: booklit.Partials{
			"Title":       title,
			"Description": description,
			"URL":         booklit.String(strings.TrimSpace(url.String())),
		},
		Block: true,
	}
}

// Cards renders a grid of small titled cards, two to a row, for a group of
// related changes that each need only a sentence and an example.
//
//	\cards{
//	  \card{Strict decoding}{...}
//	}{
//	  \card{Map targets}{...}
//	}
func (p Plugin) Cards(cards ...booklit.Content) booklit.Content {
	return booklit.Styled{Style: "cards", Content: booklit.Sequence(cards), Block: true}
}

// Card renders one card of a \cards grid. Like \write-up, its title is in
// the content as well as a partial, so search finds it.
func (p Plugin) Card(title, body booklit.Content) booklit.Content {
	return booklit.Styled{
		Style:    "card",
		Content:  booklit.Sequence{title, body},
		Partials: booklit.Partials{"Title": title, "Body": body},
		Block:    true,
	}
}

// outputLanguages are the fence languages that quote a program's result
// rather than code: ```output for what it printed, ```error for what it
// failed with. They render verbatim, unhighlighted, marked like the
// literate blocks' result and error lines.
var outputLanguages = map[string]string{
	"output": "ok",
	"error":  "err",
}

// outputBlock renders an ```output or ```error fence.
func outputBlock(kind string, code booklit.Content) booklit.Content {
	return booklit.Styled{
		Style:    "code-output",
		Content:  booklit.String(strings.TrimRight(code.String(), "\n")),
		Partials: booklit.Partials{"Kind": booklit.String(kind)},
		Block:    true,
	}
}
