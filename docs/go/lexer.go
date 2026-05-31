package dangdocs

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

func init() {
	lexers.Register(dangLexer)
}

var dangLexer = chroma.MustNewLexer(
	&chroma.Config{
		Name:      "Dang",
		Aliases:   []string{"dang"},
		Filenames: []string{"*.dang"},
		MimeTypes: []string{"text/x-dang"},
	},
	func() chroma.Rules {
		return chroma.Rules{
			"root": {
				// whitespace
				{Pattern: `\s+`, Type: chroma.TextWhitespace},

				// comments
				{Pattern: `#[^\r\n]*`, Type: chroma.Comment},

				// triple-quoted strings (incl. docstrings)
				{Pattern: `"""[\s\S]*?"""`, Type: chroma.LiteralString},

				// backtick templates: single-line; multi-line fences handled as longer match first
				{Pattern: "`{3,}[\\s\\S]*?`{3,}", Type: chroma.LiteralString},
				{Pattern: "`[^`\n]*`", Type: chroma.LiteralString},

				// double-quoted string with escapes
				{Pattern: `"(\\.|[^"\\])*"`, Type: chroma.LiteralString},

				// numeric literals
				{Pattern: `-?\d+\.\d+([eE][+\-]?\d+)?`, Type: chroma.LiteralNumberFloat},
				{Pattern: `-?\d+[eE][+\-]?\d+`, Type: chroma.LiteralNumberFloat},
				{Pattern: `-?\d+`, Type: chroma.LiteralNumberInteger},

				// directives (@foo)
				{Pattern: `@[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*`, Type: chroma.NameDecorator},

				// declaration keywords
				{Pattern: `\b(pub|let|type|interface|union|enum|scalar|new|implements|directive|import|on)\b`, Type: chroma.KeywordDeclaration},

				// control-flow keywords
				{Pattern: `\b(if|else|case|for|break|continue|return|try|catch|raise)\b`, Type: chroma.Keyword},

				// logical keywords
				{Pattern: `\b(and|or)\b`, Type: chroma.OperatorWord},

				// constant literals
				{Pattern: `\b(true|false|null)\b`, Type: chroma.KeywordConstant},

				// `self`
				{Pattern: `\bself\b`, Type: chroma.NameBuiltinPseudo},

				// built-in types
				{Pattern: `\b(Int|Float|String|Boolean|ID|Void)\b`, Type: chroma.KeywordType},

				// user-defined types (Capitalized identifiers)
				{Pattern: `\b[A-Z][A-Za-z0-9_]*\b`, Type: chroma.NameClass},

				// function call: lower-cased ident immediately followed by `(`
				{Pattern: `\b[a-z_][A-Za-z0-9_]*(?=\s*\()`, Type: chroma.NameFunction},

				// regular identifiers
				{Pattern: `\b[a-z_][A-Za-z0-9_]*\b`, Type: chroma.Name},

				// operators
				{Pattern: `(::|\?\?|==|!=|<=|>=|\+=|-=|=>|->|&&|\|\||[+\-*/%<>=!&])`, Type: chroma.Operator},

				// punctuation
				{Pattern: `[(){}\[\],.:;|?]`, Type: chroma.Punctuation},
			},
		}
	},
)
