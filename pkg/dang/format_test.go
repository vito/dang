package dang

import (
	"context"
	"os"
	"testing"

	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	os.Exit(oteltest.Main(m))
}

type FormatSuite struct{}

func TestFormat(tT *testing.T) {
	testctx.New(tT,
		oteltest.WithTracing[*testing.T](),
		oteltest.WithLogging[*testing.T](),
	).RunTests(FormatSuite{})
}

func (FormatSuite) TestChainFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "chain with multiline block stays on one line",
			input: `pub x = foo.map { a =>
  body
}`,
			expected: `pub x = foo.map { a =>
	body
}
`,
		},
		{
			name: "multiline chain preserves line breaks",
			input: `pub x = foo
  .bar
  .baz`,
			expected: `pub x = foo
	.bar
	.baz
`,
		},
		{
			name: "single-line chain stays single-line",
			input: `pub x = foo.bar.baz`,
			expected: `pub x = foo.bar.baz
`,
		},
		{
			name: "nested multiline block args stay on one line",
			input: `pub doubled_nested: [[Int!]!]! = nested.map { inner =>
  inner.map { x => x * 2 }
}`,
			expected: `pub doubled_nested: [[Int!]!]! = nested.map { inner =>
	inner.map { x => x * 2 }
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestBlankLines(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "no blank lines between simple assignments",
			input: `pub x = 1
pub y = 2
pub z = 3`,
			expected: `pub x = 1
pub y = 2
pub z = 3
`,
		},
		{
			name: "no blank lines between asserts",
			input: `assert { x == 1 }
assert { y == 2 }`,
			expected: `assert { x == 1 }
assert { y == 2 }
`,
		},
		{
			name: "blank lines around function definitions",
			input: `pub x = 1
pub foo: Int! { 42 }
pub y = 2`,
			expected: `pub x = 1

pub foo: Int! { 42 }

pub y = 2
`,
		},
		{
			name: "preserves user-added blank lines",
			input: `pub x = 1

pub y = 2`,
			expected: `pub x = 1

pub y = 2
`,
		},
		{
			name: "blank line between comment and code preserved",
			input: `# comment

pub x = 1`,
			expected: `# comment

pub x = 1
`,
		},
		{
			name: "comment hugging code stays hugging",
			input: `# comment
pub x = 1`,
			expected: `# comment
pub x = 1
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestStringFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single-quoted string stays single-quoted",
			input:    `pub x = "hello world"`,
			expected: "pub x = \"hello world\"\n",
		},
		{
			name:     "long single-quoted string stays single-quoted",
			input:    `pub x = "this is a very long string that should not be converted to triple quotes"`,
			expected: "pub x = \"this is a very long string that should not be converted to triple quotes\"\n",
		},
		{
			name: "triple-quoted string stays triple-quoted",
			input: `pub x = """
hello
world
"""`,
			expected: "pub x = \"\"\"\nhello\nworld\n\"\"\"\n",
		},
		{
			name: "indented triple-quoted string with empty lines has no trailing whitespace",
			input: `type Foo {
	pub x = """
	First paragraph.

	Second paragraph.
	"""
}`,
			expected: "type Foo {\n\tpub x = \"\"\"\n\tFirst paragraph.\n\n\tSecond paragraph.\n\t\"\"\"\n}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestFloatFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "preserves decimal notation",
			input:    `pub x = 3.14159`,
			expected: "pub x = 3.14159\n",
		},
		{
			name:     "preserves scientific notation",
			input:    `pub x = 1.5e10`,
			expected: "pub x = 1.5e10\n",
		},
		{
			name:     "preserves negative exponent",
			input:    `pub x = 1.0e-5`,
			expected: "pub x = 1.0e-5\n",
		},
		{
			name:     "preserves trailing zero",
			input:    `pub x = 42.0`,
			expected: "pub x = 42.0\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestBlockArgFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single-line block arg stays single-line",
			input:    `pub x = foo.map { x => x * 2 }`,
			expected: "pub x = foo.map { x => x * 2 }\n",
		},
		{
			name: "multiline block arg keeps chain on one line",
			input: `pub x = foo.map { x =>
  x * 2
}`,
			expected: `pub x = foo.map { x =>
	x * 2
}
`,
		},
		{
			name:     "no empty parens before block arg",
			input:    `pub x = foo.map { x => x }`,
			expected: "pub x = foo.map { x => x }\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestParameterDocstrings(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "parameter docstrings force multiline and stay with params",
			input: `pub readFile(
	"""
	Relative path within the workspace
	"""
	filePath: String!,
	"""
	Line offset to start reading from
	"""
	offset: Int! = 0
): String! {
	filePath
}`,
			expected: `pub readFile(
	"""
	Relative path within the workspace
	"""
	filePath: String!,
	"""
	Line offset to start reading from
	"""
	offset: Int! = 0,
): String! {
	filePath
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestIndentation(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "uses tabs for indentation",
			input: `type Foo {
  pub x: Int!
}`,
			expected: `type Foo {
	pub x: Int!
}
`,
		},
		{
			name: "nested indentation",
			input: `type Foo {
  pub bar: Int! {
    42
  }
}`,
			expected: `type Foo {
	pub bar: Int! {
		42
	}
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestCommentsInLists(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "comment before list element stays inside list",
			input: `pub x = [
	# comment inside list
	"a",
	"b"
]`,
			expected: `pub x = [
	# comment inside list
	"a",
	"b",
]
`,
		},
		{
			name: "trailing comment on list element",
			input: `pub x = [
	"a", # trailing comment
	"b"
]`,
			expected: `pub x = [
	"a", # trailing comment
	"b",
]
`,
		},
		{
			name: "comment in directive list arg",
			input: `pub source: Directory! @ignorePatterns(patterns: [
	# TODO: respecting .gitignore would be nice
	"Session.vim"
	"dang"
])`,
			expected: `pub source: Directory! @ignorePatterns(patterns: [
	# TODO: respecting .gitignore would be nice
	"Session.vim",
	"dang",
])
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestTrailingCommentsInChains(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "trailing comment preserved on chain element",
			input: `pub x = foo.
	bar(a). # comment on bar
	baz`,
			expected: `pub x = foo
	.bar(a) # comment on bar
	.baz
`,
		},
		{
			name: "multiple trailing comments in chain",
			input: `pub x = foo.
	bar. # first comment
	baz. # second comment
	qux`,
			expected: `pub x = foo
	.bar # first comment
	.baz # second comment
	.qux
`,
		},
		{
			name: "trailing comment on last chain element",
			input: `pub x = foo.
	bar.
	baz # final comment`,
			expected: `pub x = foo
	.bar
	.baz # final comment
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestDocstringFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "no trailing whitespace on empty lines in docstrings",
			input: `type Foo {
	"""
	Doc line 1

	Doc line 2
	"""
	pub x: Int!
}`,
			// Empty line in docstring should have no trailing whitespace
			// No extra blank line after opening brace when docstring follows
			expected: "type Foo {\n\t\"\"\"\n\tDoc line 1\n\n\tDoc line 2\n\t\"\"\"\n\tpub x: Int!\n}\n",
		},
		{
			name: "single blank line between functions with docstrings",
			input: `type Foo {
	"""
	Doc for a
	"""
	pub a: Int! { 1 }

	"""
	Doc for b
	"""
	pub b: Int! { 2 }
}`,
			// No extra blank line after opening brace when docstring follows
			// Blank line between function definitions is preserved
			expected: `type Foo {
	"""
	Doc for a
	"""
	pub a: Int! { 1 }

	"""
	Doc for b
	"""
	pub b: Int! { 2 }
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestNoExtraBlankLinesAtBlockStart(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "no blank line after opening brace with multiline args",
			input: `pub dev(
	source: Directory!,
	module: Module
): LLM! {
	let e = env.withWorkspace(source)
	e
}`,
			// Multiline args are preserved, with trailing comma
			expected: `pub dev(
	source: Directory!,
	module: Module,
): LLM! {
	let e = env.withWorkspace(source)
	e
}
`,
		},
		{
			name: "no blank line at start of simple function body",
			input: `pub foo: Int! {
	42
}`,
			expected: `pub foo: Int! {
	42
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestPreserveSameLineElements(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "list elements on same line stay together",
			input: `pub x = ["sh", "-c", """
	hello
	"""]`,
			expected: "pub x = [\"sh\", \"-c\", \"\"\"\nhello\n\"\"\"]\n",
		},
		{
			name: "list in chain call stays on one line even when chain splits",
			input: `pub x: String! {
	base
		.withExec(["sh", "-c", """
			echo hello
			"""])
		.directory(".")
}`,
			// Chain gets split, list elements stay together
			expected: "pub x: String! {\n\tbase\n\t\t.withExec([\"sh\", \"-c\", \"\"\"\n\t\techo hello\n\t\t\"\"\"])\n\t\t.directory(\".\")\n}\n",
		},
		{
			name: "args on same line stay together",
			input: `pub x = foo(a, b, c)`,
			expected: `pub x = foo(a, b, c)
`,
		},
		{
			name: "args on different lines stay on different lines",
			input: `pub x = foo(
	a,
	b,
	c
)`,
			expected: `pub x = foo(
	a,
	b,
	c,
)
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestNoFmtDirective(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "prefix nofmt preserves single node",
			input: `#nofmt
pub x = foo.bar()
pub y = foo.bar()`,
			expected: `#nofmt
pub x = foo.bar()
pub y = foo.bar
`,
		},
		{
			name: "trailing nofmt preserves single node",
			input: `pub x = foo.bar() #nofmt
pub y = foo.bar()`,
			expected: `pub x = foo.bar() #nofmt
pub y = foo.bar
`,
		},
		{
			name: "nofmt with explanation",
			input: `#nofmt testing syntax equivalence
pub x = foo.bar()`,
			expected: `#nofmt testing syntax equivalence
pub x = foo.bar()
`,
		},
		{
			name: "without nofmt file is formatted",
			input: `# regular comment
pub x = foo.bar()`,
			expected: `# regular comment
pub x = foo.bar
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestGroupedExpressions(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "grouped arithmetic is preserved",
			input:    `let x = (a + b) * c`,
			expected: "let x = (a + b) * c\n",
		},
		{
			name:     "grouped symbol is preserved",
			input:    `let x = (foo)`,
			expected: "let x = (foo)\n",
		},
		{
			name:     "grouped receiver is preserved",
			input:    `let x = (foo).bar`,
			expected: "let x = (foo).bar\n",
		},
		{
			name:     "grouped chain receiver is preserved",
			input:    `let x = (foo.bar).baz`,
			expected: "let x = (foo.bar).baz\n",
		},
		{
			name:     "zero-arg calls still elided",
			input:    `let x = foo.bar()`,
			expected: "let x = foo.bar\n",
		},
		{
			name:     "zero-arg chain calls still elided",
			input:    `let x = foo().bar().baz`,
			expected: "let x = foo.bar.baz\n",
		},
		{
			name:     "nested grouped preserved",
			input:    `let x = ((a))`,
			expected: "let x = ((a))\n",
		},
		{
			name: "multiline grouped is indented",
			input: `let x = (
foo
)`,
			expected: "let x = (\n\tfoo\n)\n",
		},
		{
			name: "multiline grouped with expression",
			input: `let x = (
a + b
)`,
			expected: "let x = (\n\ta + b\n)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func (FormatSuite) TestImportFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "imports are sorted alphabetically",
			input: `import Zebra
import Alpha
import Middle

pub x = 1`,
			expected: `import Alpha
import Middle
import Zebra

pub x = 1
`,
		},
		{
			name: "blank line after imports before non-import",
			input: `import Foo
pub x = 1`,
			expected: `import Foo

pub x = 1
`,
		},
		{
			name: "no extra blank lines between imports",
			input: `import Foo

import Bar`,
			expected: `import Bar
import Foo
`,
		},
		{
			name: "single import with no following code",
			input: `import Foo`,
			expected: `import Foo
`,
		},
		{
			name: "already sorted imports unchanged",
			input: `import Alpha
import Beta

pub x = 1`,
			expected: `import Alpha
import Beta

pub x = 1
`,
		},

	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}
