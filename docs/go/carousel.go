package dangdocs

import (
	"fmt"
	"strings"

	"github.com/vito/booklit"
	"github.com/vito/dang/v2/pkg/dang"
)

// DangCarousel renders a feature carousel: a strip of \dang-feature slides,
// each a titled, distinctive-feature showcase. docs/js/carousel.js
// progressively enhances it into a tabbed, arrow-navigated reel; without
// JavaScript every slide renders stacked and fully readable.
//
//	\dang-carousel{
//	  \dang-feature{Prototype objects}{{{
//	  ...code...
//	  }}}
//	}{
//	  \dang-feature{Copy-on-write}{{{
//	  ...code...
//	  }}}
//	}
func (p Plugin) DangCarousel(slides ...booklit.Content) booklit.Content {
	return booklit.Styled{
		Style:   "carousel",
		Content: booklit.Sequence(slides),
		Block:   true,
	}
}

// DangFeature renders one carousel slide: a feature title above a Dang snippet
// rendered exactly as a \dang-literate block — same template, styling, baked
// output, and client-side editor (see literate.go / docs/js/playground.js), so
// the carousel reuses the site's one code-snippet mechanism rather than
// inventing another.
//
// The one difference from a page literate block is isolation: each slide
// evaluates in its own fresh standard-library session (not the page-shared
// chain), since slides are independent showcases that each (re)declare their
// own types. The slide wrapper carries data-dang-literate-chain so playground.js
// replays it as a standalone chain client-side too. As with any literate block
// the snippet is evaluated at build time and a parse/type/eval failure fails
// the docs build, so the examples can't rot.
//
//	\dang-feature{Prototype objects}{{{
//	type Greeter { name: String!  greet: String! { `hi ${name}` } }
//	Greeter("world").greet
//	}}}
func (p Plugin) DangFeature(title, code booklit.Content) (booklit.Content, error) {
	source := strings.TrimRight(code.String(), "\n")

	// A fresh session per slide keeps slides independent — one slide's
	// declarations must not leak into the next.
	typeScope, valueScope := dang.BuildScopesFromImports("", nil)
	sess := &literateSession{typeScope: typeScope, valueScope: valueScope}

	stdout, value, err := literateEval(source, sess)
	if err != nil {
		return nil, fmt.Errorf(`\dang-feature %q in %s: %w`, title.String(), p.section.FilePath(), err)
	}

	// Build the same Styled "dang-literate" content a \dang-literate block
	// produces (literate.go's literateBlock), so it renders through
	// dang-literate.tmpl identically.
	partials := booklit.Partials{}
	if stdout != "" {
		partials["Stdout"] = booklit.String(stdout)
	}
	if value != "" {
		partials["Value"] = highlightResult(value)
	}
	literate := booklit.Styled{
		Style:    "dang-literate",
		Content:  p.highlightDang(source),
		Partials: partials,
		Block:    true,
	}

	return booklit.Styled{
		Style:    "carousel-slide",
		Content:  literate,
		Partials: booklit.Partials{"Title": title},
		Block:    true,
	}, nil
}
