package dangdocs

import (
	"testing"

	"github.com/alecthomas/chroma/v2"
)

// tokenize runs the dang lexer over source and returns the non-whitespace
// tokens.
func tokenize(t *testing.T, source string) []chroma.Token {
	t.Helper()

	it, err := dangLexer.Tokenise(nil, source)
	if err != nil {
		t.Fatalf("tokenise: %v", err)
	}

	var tokens []chroma.Token
	for tok := it(); tok != chroma.EOF; tok = it() {
		if tok.Type == chroma.TextWhitespace {
			continue
		}
		tokens = append(tokens, tok)
	}
	return tokens
}

func tokenType(tokens []chroma.Token, value string) (chroma.TokenType, bool) {
	for _, tok := range tokens {
		if tok.Value == value {
			return tok.Type, true
		}
	}
	return 0, false
}

// Every link of a chained call highlights as a function, whether or not it
// has arguments, matching the editors' tree-sitter queries.
func TestChainedCallHighlighting(t *testing.T) {
	tokens := tokenize(t, "foo.fizz(arg: 1).buzz.bar(x: 2).baz")

	for _, name := range []string{"fizz", "buzz", "bar", "baz"} {
		typ, ok := tokenType(tokens, name)
		if !ok {
			t.Fatalf("token %q not found in %v", name, tokens)
		}
		if typ != chroma.NameFunction {
			t.Errorf("token %q is %v, want %v", name, typ, chroma.NameFunction)
		}
	}

	// the chain head is a plain reference, not a call
	if typ, _ := tokenType(tokens, "foo"); typ != chroma.Name {
		t.Errorf("token \"foo\" is %v, want %v", typ, chroma.Name)
	}

	// argument names keep their plain highlighting
	if typ, _ := tokenType(tokens, "arg"); typ != chroma.Name {
		t.Errorf("token \"arg\" is %v, want %v", typ, chroma.Name)
	}
}

// Float literals must not be split by the field-selection rule.
func TestFloatLiteralUnaffectedByDotRule(t *testing.T) {
	tokens := tokenize(t, "let x = 1.5")

	if typ, ok := tokenType(tokens, "1.5"); !ok || typ != chroma.LiteralNumberFloat {
		t.Errorf("token \"1.5\" is %v (found=%v), want %v", typ, ok, chroma.LiteralNumberFloat)
	}
}
