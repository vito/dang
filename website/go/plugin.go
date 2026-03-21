// Package dangdocs provides a Booklit plugin for the Dang documentation site.
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

// Plugin provides custom functions for the Dang documentation site.
type Plugin struct {
	section *booklit.Section
}

// Shell renders a shell install command block.
//
//	\shell{go install github.com/vito/dang}
func (p Plugin) Shell(content booklit.Content) booklit.Content {
	return booklit.Styled{
		Style:   "shell",
		Content: content,
		Block:   true,
	}
}

// HeaderLinks renders a horizontal row of navigation links.
//
//	\header-links{
//	  \link{GitHub}{https://github.com/vito/dang}
//	}
func (p Plugin) HeaderLinks(links ...booklit.Content) booklit.Content {
	return booklit.Styled{
		Style:   "header-links",
		Content: booklit.Sequence(links),
		Block:   true,
	}
}
