//go:build cgo

package dangdocs

import (
	"strings"
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/danglang"
)

// classRun is a maximal run of bytes sharing one token class, as renderCode
// would coalesce them into a single span ("" for unstyled runs).
type classRun struct {
	text  string
	class string
}

// classify runs the highlighter for language over source and returns its
// class runs, asserting that highlighting was available and that the runs
// concatenate back to the source.
func classify(t *testing.T, language, source string) []classRun {
	t.Helper()

	classes := classifyCode(language, source)
	if classes == nil {
		t.Fatalf("classifyCode(%q) unavailable", language)
	}

	var runs []classRun
	var rebuilt strings.Builder
	for i := 0; i < len(source); {
		j := i + 1
		for j < len(source) && classes[j] == classes[i] {
			j++
		}
		runs = append(runs, classRun{text: source[i:j], class: classes[i]})
		rebuilt.WriteString(source[i:j])
		i = j
	}
	if rebuilt.String() != source {
		t.Fatalf("runs do not reproduce source:\n%q\n!=\n%q", rebuilt.String(), source)
	}
	return runs
}

// classOf returns the class of the first run whose trimmed text equals value.
// Unstyled runs coalesce with surrounding whitespace, so plain runs are
// matched by their trimmed content.
func classOf(t *testing.T, runs []classRun, value string) string {
	t.Helper()
	for _, run := range runs {
		if run.text == value || strings.TrimSpace(run.text) == value {
			return run.class
		}
	}
	t.Fatalf("run %q not found in %v", value, runs)
	return ""
}

func assertClass(t *testing.T, runs []classRun, value, want string) {
	t.Helper()
	if got := classOf(t, runs, value); got != want {
		t.Errorf("run %q is %q, want %q", value, got, want)
	}
}

// Every link of a chained call highlights as a function, whether or not it
// has arguments, matching the editors' tree-sitter queries.
func TestChainedCallHighlighting(t *testing.T) {
	runs := classify(t, "dang", "foo.fizz(arg: 1).buzz.bar(x: 2).baz")

	for _, name := range []string{"fizz", "buzz", "bar", "baz"} {
		assertClass(t, runs, name, "tok-function")
	}

	// the chain head is a plain reference, not a call
	assertClass(t, runs, "foo", "tok-variable")

	// argument names are properties
	assertClass(t, runs, "arg", "tok-property")
}

// Builtins are distinguished from ordinary calls via the query's #match?
// text predicate, which the Go binding must apply.
func TestBuiltinPredicate(t *testing.T) {
	runs := classify(t, "dang", `print("hi")`)
	assertClass(t, runs, "print", "tok-builtin")
	assertClass(t, runs, `"hi"`, "tok-string")

	runs = classify(t, "dang", `shout("hi")`)
	assertClass(t, runs, "shout", "tok-function")
}

// Float literals highlight as numbers and survive intact.
func TestFloatLiteral(t *testing.T) {
	runs := classify(t, "dang", "let x = 1.5")
	assertClass(t, runs, "let", "tok-keyword")
	assertClass(t, runs, "1.5", "tok-number")
}

// Stdlib signature cards render bare field-declaration fragments; the
// highlighter re-parses them inside a synthetic interface body so they
// highlight like real declarations instead of falling back to plain text.
func TestSignatureFragment(t *testing.T) {
	runs := classify(t, "dang", "withExec(args: [String!]!): Container!")
	assertClass(t, runs, "withExec", "tok-function")
	assertClass(t, runs, "args", "tok-variable") // parameter, default color
	assertClass(t, runs, "String", "tok-type")
	assertClass(t, runs, "Container", "tok-type")

	// a declaration with a block parameter, as the stdlib cards render them
	runs = classify(t, "dang", "find(&block(item: a): Boolean!): a")
	assertClass(t, runs, "find", "tok-function")
	assertClass(t, runs, "&", "tok-operator")
	assertClass(t, runs, "Boolean", "tok-type")
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
		src := []byte(signaturePrefix + sig + signatureSuffix)
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
	runs := classify(t, "dang", "twice { 21 }                  # body takes no args\ntwice { let n = 21, n }       # multi-statement\n")
	assertClass(t, runs, "# body takes no args", "tok-comment")
	assertClass(t, runs, "# multi-statement", "tok-comment")
}

// Whole-program snippets keep highlighting: types, keywords, strings.
func TestProgramSnippet(t *testing.T) {
	runs := classify(t, "dang", "type Greeter {\n  greet(name: String!): String! {\n    \"hey, ${name}!\"\n  }\n}\n")
	assertClass(t, runs, "type", "tok-keyword")
	assertClass(t, runs, "Greeter", "tok-type")
	assertClass(t, runs, "greet", "tok-function")
	assertClass(t, runs, "String", "tok-type")
}

// ```sh fences highlight with the bash grammar (under any of its aliases).
func TestBashHighlighting(t *testing.T) {
	runs := classify(t, "sh", "export GITHUB_TOKEN=\"hunter2\" # so secret\n")
	assertClass(t, runs, "export", "tok-keyword")
	assertClass(t, runs, "GITHUB_TOKEN", "tok-property")
	assertClass(t, runs, `"hunter2"`, "tok-string")
	assertClass(t, runs, "# so secret", "tok-comment")

	runs = classify(t, "shell", "dagger init --sdk=dang\n")
	assertClass(t, runs, "dagger", "tok-function")
	assertClass(t, runs, "--sdk=dang", "tok-number")
}

// ```sh fences holding terminal transcripts highlight each "$ "-prompted
// command as bash and leave program output plain, rather than garbling the
// whole transcript through one bash parse.
func TestShellTranscriptHighlighting(t *testing.T) {
	runs := classify(t, "sh", "$ export GITHUB_TOKEN=\"$(gh auth token)\"\n$ dang\nWelcome to Dang REPL v0.1.0\n")
	assertClass(t, runs, "export", "tok-keyword")
	assertClass(t, runs, "GITHUB_TOKEN", "tok-property")
	assertClass(t, runs, "gh", "tok-function")
	assertClass(t, runs, "Welcome to Dang REPL v0.1.0", "")

	// the prompts and the bare `dang` command line: prompts stay unstyled,
	// the command highlights as a command
	assertClass(t, runs, "dang", "tok-function")
}

// ```toml fences highlight with the toml grammar; keys stay in the property
// color the site has always used for them.
func TestTomlHighlighting(t *testing.T) {
	runs := classify(t, "toml", "[imports.GitHub]\nendpoint = \"https://api.github.com/graphql\"\nretries = 3\ndagger = true\n")
	assertClass(t, runs, "imports", "tok-property")
	assertClass(t, runs, "endpoint", "tok-property")
	assertClass(t, runs, `"https://api.github.com/graphql"`, "tok-string")
	assertClass(t, runs, "=", "tok-operator")
	assertClass(t, runs, "3", "tok-number")
	assertClass(t, runs, "true", "tok-number")
}

// Languages with no registered grammar report nil, which renderCode renders
// as plain code.
func TestUnknownLanguagePlain(t *testing.T) {
	for _, language := range []string{"", "graphql", "console"} {
		if classes := classifyCode(language, "anything at all"); classes != nil {
			t.Errorf("classifyCode(%q) = %v, want nil", language, classes)
		}
	}
}
