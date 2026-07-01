package dangdocs

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/vito/booklit"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/tests/gqlserver"
)

// demoImport builds the bundled in-process "Demo" GraphQL import once and
// reuses it across slides. It's the same schema + canned resolvers the docs
// playground bundles into wasm (tests/gqlserver), so a slide's build-time
// output matches what running it client-side produces.
var (
	demoOnce sync.Once
	demoCfg  dang.ImportConfig
	demoErr  error
)

func demoImport() (dang.ImportConfig, error) {
	demoOnce.Do(func() {
		demoCfg, demoErr = gqlserver.ImportConfig("Demo")
	})
	return demoCfg, demoErr
}

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
// An optional third argument is prose rendered beneath the code block (tips,
// the corresponding dang.toml, a link to the schema behind an import):
//
//	\dang-feature{Schema-native types}{{{
//	import Demo
//	users.{{ name }}
//	}}}{
//	  `Demo` is a small bundled schema — \link{see its SDL}{...}.
//	}
func (p Plugin) DangFeature(title, code booklit.Content, notes ...booklit.Content) (booklit.Content, error) {
	source := strings.TrimRight(code.String(), "\n")

	// A fresh session per slide keeps slides independent — one slide's
	// declarations must not leak into the next.
	typeScope, valueScope := dang.BuildScopesFromImports("", nil)
	sess := &literateSession{typeScope: typeScope, valueScope: valueScope}

	// Make `import Demo` resolvable, so slides can demonstrate GraphQL against
	// the bundled in-process schema. The same import is wired into the wasm
	// playground (cmd/dang-playground), so the baked output matches a live run.
	cfg, err := demoImport()
	if err != nil {
		return nil, fmt.Errorf(`\dang-feature %q: building bundled GraphQL import: %w`, title.String(), err)
	}
	ctx := dang.ContextWithImportConfigs(context.Background(), cfg)

	stdout, value, err := literateEvalCtx(ctx, source, sess)
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

	return p.carouselSlide(title, literate, notes), nil
}

// DangGithubFeature renders a carousel slide whose snippet is a live
// \dang-github-playground (see plugin.go): a "Sign in with GitHub" control plus
// an editor that resolves `import GitHub` against GitHub's real schema from the
// browser. Unlike DangFeature it is NOT evaluated at build time — it needs the
// live API and a token — so it carries no baked output and runs only on demand.
// This is the home of what used to be a standalone GitHub-playground page: the
// example was always the point, and here it sits among the offline ones.
//
//	\dang-github-feature{Live GraphQL}{{{
//	import GitHub
//	viewer.{{ login, name }}
//	}}}{
//	  Querying api.github.com straight from your browser; the token stays in
//	  this tab.
//	}
func (p Plugin) DangGithubFeature(title, code booklit.Content, notes ...booklit.Content) booklit.Content {
	playground := booklit.Styled{
		Style:   "dang-github-playground",
		Content: p.highlightDang(code.String()),
		Block:   true,
	}
	return p.carouselSlide(title, playground, notes)
}

// carouselSlide wraps a slide's inner block (a literate block or a github
// playground) with its feature title and optional prose notes. docs/js/carousel.js
// reads the title for the tab strip and hides it in the body once enhanced (the
// active tab doubles as the header).
func (p Plugin) carouselSlide(title, inner booklit.Content, notes []booklit.Content) booklit.Content {
	partials := booklit.Partials{"Title": title}
	if len(notes) > 0 && notes[0] != nil {
		partials["Notes"] = notes[0]
	}
	return booklit.Styled{
		Style:    "carousel-slide",
		Content:  inner,
		Partials: partials,
		Block:    true,
	}
}
