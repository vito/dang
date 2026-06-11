//go:build cgo

package dangdocs

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/danglang"
)

// tokenize runs the dang lexer over source and returns its tokens, asserting
// that they concatenate back to the source.
func tokenize(t *testing.T, source string) []chroma.Token {
	t.Helper()

	it, err := dangLexer.Tokenise(nil, source)
	if err != nil {
		t.Fatalf("tokenise: %v", err)
	}

	var tokens []chroma.Token
	var rebuilt strings.Builder
	for tok := it(); tok != chroma.EOF; tok = it() {
		rebuilt.WriteString(tok.Value)
		tokens = append(tokens, tok)
	}
	if rebuilt.String() != source {
		t.Fatalf("tokens do not reproduce source:\n%q\n!=\n%q", rebuilt.String(), source)
	}
	return tokens
}

// tokenType returns the type of the first token whose trimmed value equals
// value. Unstyled runs coalesce with surrounding whitespace, so plain tokens
// are matched by their trimmed content.
func tokenType(t *testing.T, tokens []chroma.Token, value string) chroma.TokenType {
	t.Helper()
	for _, tok := range tokens {
		if tok.Value == value || strings.TrimSpace(tok.Value) == value {
			return tok.Type
		}
	}
	t.Fatalf("token %q not found in %v", value, tokens)
	return 0
}

func assertToken(t *testing.T, tokens []chroma.Token, value string, want chroma.TokenType) {
	t.Helper()
	if got := tokenType(t, tokens, value); got != want {
		t.Errorf("token %q is %v, want %v", value, got, want)
	}
}

// Every link of a chained call highlights as a function, whether or not it
// has arguments, matching the editors' tree-sitter queries.
func TestChainedCallHighlighting(t *testing.T) {
	tokens := tokenize(t, "foo.fizz(arg: 1).buzz.bar(x: 2).baz")

	for _, name := range []string{"fizz", "buzz", "bar", "baz"} {
		assertToken(t, tokens, name, chroma.NameFunction)
	}

	// the chain head is a plain reference, not a call
	assertToken(t, tokens, "foo", chroma.Name)

	// argument names are properties, unstyled under the current palette
	assertToken(t, tokens, "arg", chroma.NameProperty)
}

// Builtins are distinguished from ordinary calls via the query's #match?
// text predicate, which the Go binding must apply.
func TestBuiltinPredicate(t *testing.T) {
	tokens := tokenize(t, `print("hi")`)
	assertToken(t, tokens, "print", chroma.NameBuiltin)
	assertToken(t, tokens, `"hi"`, chroma.LiteralString)

	tokens = tokenize(t, `shout("hi")`)
	assertToken(t, tokens, "shout", chroma.NameFunction)
}

// Float literals highlight as numbers and survive intact.
func TestFloatLiteral(t *testing.T) {
	tokens := tokenize(t, "let x = 1.5")
	assertToken(t, tokens, "let", chroma.Keyword)
	assertToken(t, tokens, "1.5", chroma.LiteralNumber)
}

// Stdlib signature cards render bare field-declaration fragments; the lexer
// re-parses them inside a synthetic interface body so they highlight like
// real declarations instead of falling back to plain text.
func TestSignatureFragment(t *testing.T) {
	tokens := tokenize(t, "withExec(args: [String!]!): Container!")
	assertToken(t, tokens, "withExec", chroma.NameFunction)
	assertToken(t, tokens, "args", chroma.Name) // parameter, plain like today
	assertToken(t, tokens, "String", chroma.KeywordType)
	assertToken(t, tokens, "Container", chroma.KeywordType)

	// a declaration with a block parameter, as the stdlib cards render them
	tokens = tokenize(t, "find(&block(item: a): Boolean!): a")
	assertToken(t, tokens, "find", chroma.NameFunction)
	assertToken(t, tokens, "&", chroma.Operator)
	assertToken(t, tokens, "Boolean", chroma.KeywordType)
}

// Every registered builtin's signature must be a valid Dang declaration —
// the stdlib cards double as examples of declaring block-taking functions,
// so the notation may not drift from the grammar.
func TestSignaturesAreValidDeclarations(t *testing.T) {
	var defs []dang.BuiltinDef
	dang.ForEachFunction(func(d dang.BuiltinDef) { defs = append(defs, d) })
	for _, recv := range dang.MethodReceivers() {
		dang.ForEachMethod(recv, func(d dang.BuiltinDef) { defs = append(defs, d) })
	}
	for _, mod := range dang.StaticModules() {
		dang.ForEachStaticMethod(mod, func(d dang.BuiltinDef) { defs = append(defs, d) })
	}
	if len(defs) == 0 {
		t.Fatal("no builtins registered")
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(danglang.Language()); err != nil {
		t.Fatalf("set language: %v", err)
	}

	for _, d := range defs {
		sig := signature(d, "")
		src := []byte("interface _ {\n" + sig + "\n}")
		tree := parser.Parse(src, nil)
		if tree == nil {
			t.Fatalf("parse %q: no tree", sig)
		}
		if n := errorBytes(tree.RootNode(), 0, len(src)); n > 0 {
			t.Errorf("signature %q is not a valid declaration (%d error bytes)", sig, n)
		}
		tree.Close()
	}
}

// A comment trailing code on the same line highlights as a comment. The
// external scanner used to refuse the `#` because the inline spaces before it
// were never skipped, turning the comment into a parse error.
func TestTrailingCommentHighlighting(t *testing.T) {
	tokens := tokenize(t, "twice { 21 }                  # body takes no args\ntwice { let n = 21, n }       # multi-statement\n")
	assertToken(t, tokens, "# body takes no args", chroma.Comment)
	assertToken(t, tokens, "# multi-statement", chroma.Comment)
}

// Whole-program snippets keep highlighting: types, keywords, strings.
func TestProgramSnippet(t *testing.T) {
	tokens := tokenize(t, "type Greeter {\n  greet(name: String!): String! {\n    \"hey, ${name}!\"\n  }\n}\n")
	assertToken(t, tokens, "type", chroma.Keyword)
	assertToken(t, tokens, "Greeter", chroma.KeywordType)
	assertToken(t, tokens, "greet", chroma.NameFunction)
	assertToken(t, tokens, "String", chroma.KeywordType)
}
