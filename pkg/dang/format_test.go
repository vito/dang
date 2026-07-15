package dang

import (
	"context"
	"os"
	"testing"

	"github.com/dagger/otel-go/oteltestctx"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	os.Exit(oteltestctx.Main(m))
}

type FormatSuite struct{}

func TestFormat(tT *testing.T) {
	testctx.New(tT,
		oteltestctx.WithTracing[*testing.T](),
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
			name:  "single-line chain stays single-line",
			input: `pub x = foo.bar.baz`,
			expected: `pub x = foo.bar.baz
`,
		},
		{
			name: "field access in chain arg is not split",
			input: `pub x = foo.
  withExec(["is-semver", version], expect: ReturnType.ANY).
  exitCode`,
			expected: `pub x = foo
  .withExec(["is-semver", version], expect: ReturnType.ANY)
  .exitCode
`,
		},
		{
			name: "nested multiline block args stay on one line",
			input: `doubled_nested: [[Int!]!]! = nested.map { inner =>
  inner.map { x => x * 2 }
}`,
			expected: `doubled_nested: [[Int!]!]! = nested.map { inner =>
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

func (FormatSuite) TestDotBlockChainFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:  "single-line dot-block chain stays single-line",
			input: `pub x = 3.{ _ + 1 }.{ _ * 10 }`,
			expected: `pub x = 3.{ _ + 1 }.{ _ * 10 }
`,
		},
		{
			name: "multiline dot-block body splits the whole chain",
			input: `pub x = Container("app").{ mountCache(_, "/registry", "regCache") }.{
  prepareToolchain(
    _,
    "x86_64",
  )
}.withEnvVariable("CARGO_TARGET_DIR", "/work/target").{
  mountCache(
    _,
    "/target",
    "tgtCache",
  )
}`,
			expected: `pub x = Container("app")
  .{ mountCache(_, "/registry", "regCache") }
  .{
    prepareToolchain(
      _,
      "x86_64",
    )
  }
  .withEnvVariable("CARGO_TARGET_DIR", "/work/target")
  .{
    mountCache(
      _,
      "/target",
      "tgtCache",
    )
  }
`,
		},
		{
			name: "dot-block on next line splits the chain",
			input: `pub x = foo
  .{ bar(_) }
  .baz`,
			expected: `pub x = foo
  .{ bar(_) }
  .baz
`,
		},
		{
			name: "regular block arg chain keeps closing brace attached",
			input: `pub x = foo.store { x =>
  x + 10
}.get(5)`,
			expected: `pub x = foo.store { x =>
  x + 10
}.get(5)
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

func (FormatSuite) TestVisibilityFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "pub stripped from typed field",
			input:    "pub x: Int! = 1",
			expected: "x: Int! = 1\n",
		},
		{
			name:     "pub stripped from type-only field",
			input:    "type T {\n  pub x: Int!\n}",
			expected: "type T {\n  x: Int!\n}\n",
		},
		{
			name:     "pub stripped from method",
			input:    "type T {\n  pub greet: String! { \"hi\" }\n}",
			expected: "type T {\n  greet: String! { \"hi\" }\n}\n",
		},
		{
			name:     "pub stripped from method with args",
			input:    "type T {\n  pub add(x: Int!): Int! { x }\n}",
			expected: "type T {\n  add(x: Int!): Int! { x }\n}\n",
		},
		{
			// The bare name = value form has no type annotation, so dropping the
			// keyword would re-parse as a reassignment. pub must be preserved.
			name:     "pub kept on value-only field",
			input:    "pub x = 1",
			expected: "pub x = 1\n",
		},
		{
			name:     "let preserved as private marker",
			input:    "type T {\n  let secret: String! = \"x\"\n}",
			expected: "type T {\n  let secret: String! = \"x\"\n}\n",
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

func (FormatSuite) TestTemplateFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single-line literal",
			input:    "pub x = `hello world`",
			expected: "pub x = `hello world`\n",
		},
		{
			name:     "single-line with interpolation",
			input:    "pub name = \"Ada\"\npub x = `hello ${name}!`",
			expected: "pub name = \"Ada\"\npub x = `hello ${name}!`\n",
		},
		{
			name: "single-line interpolation comment is preserved",
			input: "pub name = \"Ada\"\npub greeting = `hello ${ # keep the explanation\n" +
				"  name\n}`",
			expected: "pub name = \"Ada\"\npub greeting = `hello ${ # keep the explanation\n" +
				"  name\n}`\n",
		},
		{
			name:     "lone dollar stays literal",
			input:    "pub x = `issue $5`",
			expected: "pub x = `issue $5`\n",
		},
		{
			name:     "escaped dollar braces stays literal",
			input:    "pub x = `issue \\${foo}`",
			expected: "pub x = `issue \\${foo}`\n",
		},
		{
			name:     "backslash stays literal",
			input:    "pub x = `\\d+`",
			expected: "pub x = `\\d+`\n",
		},
		{
			name:     "literal dollar-brace via escape",
			input:    `pub x = ` + "`prefix \\${name} suffix`",
			expected: `pub x = ` + "`prefix \\${name} suffix`\n",
		},
		{
			name: "multi-line flush content stays flush",
			input: "pub x = ```\n" +
				"hello\n" +
				"```",
			expected: "pub x = ```\n" +
				"hello\n" +
				"```\n",
		},
		{
			name: "multi-line indented content keeps one step indent, closing flush",
			input: "pub x = ```\n" +
				"  indented\n" +
				"  body\n" +
				"  ```",
			expected: "pub x = ```\n" +
				"  indented\n" +
				"  body\n" +
				"```\n",
		},
		{
			name: "multi-line content above outer scope gets one step indent, closing at outer scope",
			input: "type Foo {\n" +
				"\tpub x = ```\n" +
				"\t\tindented\n" +
				"\t\tbody\n" +
				"\t\t```\n" +
				"}",
			expected: "type Foo {\n" +
				"  pub x = ```\n" +
				"    indented\n" +
				"    body\n" +
				"  ```\n" +
				"}\n",
		},
		{
			name: "multi-line flush content stays flush when nested",
			input: "type Foo {\n" +
				"\tpub x = ```\n" +
				"\tflush\n" +
				"\t```\n" +
				"}",
			expected: "type Foo {\n" +
				"  pub x = ```\n" +
				"  flush\n" +
				"  ```\n" +
				"}\n",
		},
		{
			name: "multi-line with lang tag",
			input: "pub x = ```go\n" +
				"  func main() {}\n" +
				"  ```",
			expected: "pub x = ```go\n" +
				"  func main() {}\n" +
				"```\n",
		},
		{
			name: "multi-line with interpolation",
			input: "pub who = \"world\"\n" +
				"pub x = ```\n" +
				"  hello ${who}!\n" +
				"  ```",
			expected: "pub who = \"world\"\n" +
				"pub x = ```\n" +
				"  hello ${who}!\n" +
				"```\n",
		},
		{
			name: "multi-line interpolation comment is preserved",
			input: "pub who = \"world\"\n" +
				"pub x = ```\n" +
				"  hello ${ # who to greet\n" +
				"    who\n" +
				"  }!\n" +
				"  ```",
			expected: "pub who = \"world\"\n" +
				"pub x = ```\n" +
				"  hello ${ # who to greet\n" +
				"    who\n" +
				"  }!\n" +
				"```\n",
		},
		{
			name: "multi-line fence bumping preserved",
			input: "pub x = ````\n" +
				"```go\n" +
				"func main() {}\n" +
				"```\n" +
				"````",
			expected: "pub x = ````\n" +
				"```go\n" +
				"func main() {}\n" +
				"```\n" +
				"````\n",
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
foo: Int! { 42 }
pub y = 2`,
			expected: `pub x = 1

foo: Int! { 42 }

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
		{
			name: "blank lines before closing brace are removed",
			input: `pub foo: String {
  let x = 1

  x

}`,
			expected: `foo: String {
  let x = 1

  x
}
`,
		},
		{
			name: "blank lines before nested closing braces are removed",
			input: `pub bar: String {
  let y = 2

  if (y) {
    y

  }

}`,
			expected: `bar: String {
  let y = 2

  if (y) {
    y
  }
}
`,
		},
		{
			name: "blank lines before closing bracket are removed",
			input: `pub items = [
  1,
  2,

]`,
			expected: `pub items = [
  1,
  2,
]
`,
		},
		{
			name: "blank line before comment kept, blank before brace removed",
			input: `pub foo: String {
  x

  # tail

}`,
			expected: `foo: String {
  x

  # tail
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
			name: "triple-quoted flush content stays flush",
			input: `pub x = """
hello
world
"""`,
			expected: "pub x = \"\"\"\nhello\nworld\n\"\"\"\n",
		},
		{
			name: "triple-quoted indented content keeps one step indent, closing flush",
			input: `pub x = """
  indented
  body
  """`,
			expected: "pub x = \"\"\"\n  indented\n  body\n\"\"\"\n",
		},
		{
			name: "triple-quoted content above outer scope gets one step indent, closing at outer scope",
			input: "type Foo {\n" +
				"\tpub x = \"\"\"\n" +
				"\t\tindented\n" +
				"\t\tbody\n" +
				"\t\t\"\"\"\n" +
				"}",
			expected: "type Foo {\n" +
				"  pub x = \"\"\"\n" +
				"    indented\n" +
				"    body\n" +
				"  \"\"\"\n" +
				"}\n",
		},
		{
			name: "triple-quoted with empty lines has no trailing whitespace",
			input: `type Foo {
	pub x = """
	First paragraph.

	Second paragraph.
	"""
}`,
			expected: "type Foo {\n  pub x = \"\"\"\n  First paragraph.\n\n  Second paragraph.\n  \"\"\"\n}\n",
		},
		{
			name:     "triple-quoted inline stays inline",
			input:    `pub x = """hello"""`,
			expected: "pub x = \"\"\"hello\"\"\"\n",
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

func (FormatSuite) TestUnaryMinusFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "negative int literal",
			input:    `pub x = -5`,
			expected: "pub x = -5\n",
		},
		{
			name:     "negative float literal",
			input:    `pub x = -3.14`,
			expected: "pub x = -3.14\n",
		},
		{
			name:     "negation of identifier",
			input:    `pub x = -y`,
			expected: "pub x = -y\n",
		},
		{
			name:     "negation of parenthesized expression",
			input:    `pub x = -(a + b)`,
			expected: "pub x = -(a + b)\n",
		},
		{
			name:     "subtraction of negative literal",
			input:    `pub x = a - -1`,
			expected: "pub x = a - -1\n",
		},
		{
			name:     "list of negative literals",
			input:    `pub x = [-1, -2, -3]`,
			expected: "pub x = [-1, -2, -3]\n",
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

func (FormatSuite) TestMapFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single-line map stays single-line",
			input:    `let foo: Map[String!]! = ["key": "value"]`,
			expected: "let foo: Map[String!]! = [\"key\": \"value\"]\n",
		},
		{
			name: "single-entry multiline map gets trailing comma",
			input: `let foo: Map[String!]! = [
	"key": "value"
]`,
			expected: `let foo: Map[String!]! = [
  "key": "value",
]
`,
		},
		{
			name: "multi-entry multiline map gets one entry per line",
			input: `let foo = [
	"a": 1,
	"b": 2
]`,
			expected: `let foo = [
  "a": 1,
  "b": 2,
]
`,
		},
		{
			name: "comments stay inside multiline map",
			input: `let foo = [
	# comment inside map
	"key": "value" # trailing comment
]`,
			expected: `let foo = [
  # comment inside map
  "key": "value", # trailing comment
]
`,
		},
		{
			name: "opening comment stays on multiline map opening line",
			input: `let foo = [# some comment
"key": "value"]`,
			expected: `let foo = [ # some comment
  "key": "value",
]
`,
		},
		{
			name: "same-line entries in multiline map stay together",
			input: `let foo = [
	"a": 1, "b": 2
	"c": 3
]`,
			expected: `let foo = [
  "a": 1, "b": 2,
  "c": 3,
]
`,
		},
		{
			name:  "long single-line map is split",
			input: `let foo = ["alpha": "one", "beta": "two", "gamma": "three", "delta": "four", "epsilon": "five", "zeta": "six"]`,
			expected: `let foo = [
  "alpha": "one",
  "beta": "two",
  "gamma": "three",
  "delta": "four",
  "epsilon": "five",
  "zeta": "six",
]
`,
		},
		{
			name:  "trailing comment on long single-line map stays after map",
			input: `let foo = ["alpha": "one", "beta": "two", "gamma": "three", "delta": "four", "epsilon": "five", "zeta": "six"] # keep this with the map`,
			expected: `let foo = [
  "alpha": "one",
  "beta": "two",
  "gamma": "three",
  "delta": "four",
  "epsilon": "five",
  "zeta": "six",
] # keep this with the map
`,
		},
		{
			name:  "nested long map is split with parent",
			input: `let foo = ["outer": ["alpha": "one", "beta": "two", "gamma": "three", "delta": "four", "epsilon": "five", "zeta": "six"]]`,
			expected: `let foo = [
  "outer": [
    "alpha": "one",
    "beta": "two",
    "gamma": "three",
    "delta": "four",
    "epsilon": "five",
    "zeta": "six",
  ],
]
`,
		},
		{
			name:  "list containing long map splits both collections",
			input: `let foo = [["alpha": "one", "beta": "two", "gamma": "three", "delta": "four", "epsilon": "five", "zeta": "six"]]`,
			expected: `let foo = [
  [
    "alpha": "one",
    "beta": "two",
    "gamma": "three",
    "delta": "four",
    "epsilon": "five",
    "zeta": "six",
  ],
]
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)

			result2, err := FormatFile([]byte(result))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result2, "formatting should be idempotent")
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

func (FormatSuite) TestConstructorBlockParamFormatting(ctx context.Context, t *testctx.T) {
	input := `type Loop {
  new(&condition(x: Int!): Boolean!) {
    self
  }
}`
	expected := `type Loop {
  new(&condition(x: Int!): Boolean!) {
    self
  }
}
`

	result, err := FormatFile([]byte(input))
	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func (FormatSuite) TestFunctionRefFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "symbol",
			input:    `pub x = &foo`,
			expected: "pub x = &foo\n",
		},
		{
			name:     "field selection",
			input:    `pub y = &self.foo`,
			expected: "pub y = &self.foo\n",
		},
		{
			name: "constructor assignment",
			input: `type DeferredCondition {
  let condition: Boolean! { true }

  new(&condition: Boolean!) {
    self.condition = &condition
    self
  }
}`,
			expected: `type DeferredCondition {
  let condition: Boolean! { true }

  new(&condition: Boolean!) {
    self.condition = &condition
    self
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

func (FormatSuite) TestParameterDocstrings(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "parameter docstrings force multiline and stay with params",
			input: `readFile(
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
			expected: `readFile(
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
			name: "uses two spaces for indentation",
			input: `type Foo {
  x: Int!
}`,
			expected: `type Foo {
  x: Int!
}
`,
		},
		{
			name: "nested indentation",
			input: `type Foo {
  bar: Int! {
    42
  }
}`,
			expected: `type Foo {
  bar: Int! {
    42
  }
}
`,
		},
		{
			name: "nested enum closing brace stays indented",
			input: `check: Void {
  enum Outcome {
    PASSED
    FAILED
  }
  null
}`,
			expected: `check: Void {
  enum Outcome {
    PASSED
    FAILED
  }
  null
}
`,
		},
		{
			name: "nested type declarations keep source order",
			input: `check: Void {
  type ParseResultChild {
    name: String!
  }
  type ParseResult {
    children: [ParseResultChild!]
  }
  null
}`,
			expected: `check: Void {
  type ParseResultChild {
    name: String!
  }
  type ParseResult {
    children: [ParseResultChild!]
  }
  null
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
			input: `source: Directory! @ignorePatterns(patterns: [
	# TODO: respecting .gitignore would be nice
	"Session.vim"
	"dang"
])`,
			expected: `source: Directory! @ignorePatterns(patterns: [
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

func (FormatSuite) TestCommentsInCallArgs(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "standalone comment stays between multiline call args",
			input: `pub x = foo.search(
	pattern: "a",
	# pattern: ` + "`a`" + `,
	globs: ["b"],
)`,
			expected: `pub x = foo.search(
  pattern: "a",
  # pattern: ` + "`a`" + `,
  globs: ["b"],
)
`,
		},
		{
			name: "trailing comment stays on multiline call arg",
			input: `pub x = foo(
	a: 1, # comment on a
	b: 2,
)`,
			expected: `pub x = foo(
  a: 1, # comment on a
  b: 2,
)
`,
		},
		{
			name: "comment before single chained call arg keeps args multiline",
			input: `pub x = foo
  .bar(
    # comment on arg
    a
  )`,
			expected: `pub x = foo
  .bar(
    # comment on arg
    a,
  )
`,
		},
		{
			name: "trailing comment on chained call opening paren stays before args",
			input: `pub x = foo
  .bar( # opening comment
    a
  )`,
			expected: `pub x = foo
  .bar( # opening comment
    a,
  )
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)

			result2, err := FormatFile([]byte(result))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result2, "formatting should be idempotent")
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

func (FormatSuite) TestStandaloneCommentsInChains(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "comment before first chain element",
			input: `pub x = container
  # set the image
  .from("alpine")
  .imageRef`,
			expected: `pub x = container
  # set the image
  .from("alpine")
  .imageRef
`,
		},
		{
			name: "comment before middle chain element",
			input: `pub x = container
  .from("alpine")
  # get the ref
  .imageRef`,
			expected: `pub x = container
  .from("alpine")
  # get the ref
  .imageRef
`,
		},
		{
			name: "multiple comments before a chain element",
			input: `pub x = container
  # first comment
  # second comment
  .from("alpine")
  .imageRef`,
			expected: `pub x = container
  # first comment
  # second comment
  .from("alpine")
  .imageRef
`,
		},
		{
			name: "comments before each chain element",
			input: `pub x = container
  # comment on from
  .from("alpine")
  # comment on imageRef
  .imageRef`,
			expected: `pub x = container
  # comment on from
  .from("alpine")
  # comment on imageRef
  .imageRef
`,
		},
		{
			name: "mixed standalone and trailing comments in chain",
			input: `pub x = container
  # standalone before from
  .from("alpine") # trailing on from
  # standalone before imageRef
  .imageRef`,
			expected: `pub x = container
  # standalone before from
  .from("alpine") # trailing on from
  # standalone before imageRef
  .imageRef
`,
		},
		{
			name: "comment in select-only chain",
			input: `pub x = foo
  # comment
  .bar
  .baz`,
			expected: `pub x = foo
  # comment
  .bar
  .baz
`,
		},
		{
			name: "idempotent after formatting",
			input: `pub x = container
  # set the image
  .from("alpine")
  # another comment
  .imageRef`,
			expected: `pub x = container
  # set the image
  .from("alpine")
  # another comment
  .imageRef
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)

			// Verify idempotency: formatting the output again should produce the same result
			result2, err := FormatFile([]byte(result))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result2, "formatting should be idempotent")
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
	x: Int!
}`,
			// Empty line in docstring should have no trailing whitespace
			// No extra blank line after opening brace when docstring follows
			expected: "type Foo {\n  \"\"\"\n  Doc line 1\n\n  Doc line 2\n  \"\"\"\n  x: Int!\n}\n",
		},
		{
			name: "single blank line between functions with docstrings",
			input: `type Foo {
	"""
	Doc for a
	"""
	a: Int! { 1 }

	"""
	Doc for b
	"""
	b: Int! { 2 }
}`,
			// No extra blank line after opening brace when docstring follows
			// Blank line between function definitions is preserved
			expected: `type Foo {
  """
  Doc for a
  """
  a: Int! { 1 }

  """
  Doc for b
  """
  b: Int! { 2 }
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
			input: `dev(
	source: Directory!,
	module: Module
): LLM! {
	let e = env.withWorkspace(source)
	e
}`,
			// Multiline args are preserved, with trailing comma
			expected: `dev(
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
			input: `foo: Int! {
	42
}`,
			expected: `foo: Int! {
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
			// Body indented one step deeper than the opening fence's scope;
			// closing fence sits at the scope of the opening fence.
			expected: "pub x = [\"sh\", \"-c\", \"\"\"\n  hello\n\"\"\"]\n",
		},
		{
			name: "list in chain call stays on one line even when chain splits",
			input: `x: String! {
	base
		.withExec(["sh", "-c", """
			echo hello
			"""])
		.directory(".")
}`,
			// Chain gets split, list elements stay together; body indented
			// one step deeper than the opening fence's scope; closing fence
			// sits at the scope of the opening fence.
			expected: "x: String! {\n  base\n    .withExec([\"sh\", \"-c\", \"\"\"\n      echo hello\n    \"\"\"])\n    .directory(\".\")\n}\n",
		},
		{
			name:  "long single-line list is split",
			input: `let xs = ["alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta", "iota"]`,
			expected: `let xs = [
  "alpha",
  "beta",
  "gamma",
  "delta",
  "epsilon",
  "zeta",
  "eta",
  "theta",
  "iota",
]
`,
		},
		{
			name:  "trailing comment on long single-line list stays after list",
			input: `let xs = ["alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta", "iota"] # keep this with the list`,
			expected: `let xs = [
  "alpha",
  "beta",
  "gamma",
  "delta",
  "epsilon",
  "zeta",
  "eta",
  "theta",
  "iota",
] # keep this with the list
`,
		},
		{
			name: "method args not split by multiline receiver",
			input: `let strs = [
  "hello",
  "world",
  "!",
].join("\n")`,
			expected: `let strs = [
  "hello",
  "world",
  "!",
].join("\n")
`,
		},
		{
			name:  "args on same line stay together",
			input: `pub x = foo(a, b, c)`,
			expected: `pub x = foo(a, b, c)
`,
		},
		{
			name: "multiple args past column 80 are split",
			input: `testBase: Void @check {
  testSkip(skip: ["TestProvision", "TestTelemetry", "TestModule"], pkg: "./...", race: true)
}`,
			expected: `testBase: Void @check {
  testSkip(
    skip: ["TestProvision", "TestTelemetry", "TestModule"],
    pkg: "./...",
    race: true,
  )
}
`,
		},
		{
			name:  "short multi-arg calls stay on one line",
			input: `pub x = foo(a: 1, b: 2)`,
			expected: `pub x = foo(a: 1, b: 2)
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
			expected: "let x = (\n  foo\n)\n",
		},
		{
			name: "multiline grouped with expression",
			input: `let x = (
a + b
)`,
			expected: "let x = (\n  a + b\n)\n",
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

func (FormatSuite) TestLogicalOpFormatting(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single-line and stays single-line",
			input:    `let x = a and b`,
			expected: "let x = a and b\n",
		},
		{
			name:     "single-line or stays single-line",
			input:    `let x = a or b`,
			expected: "let x = a or b\n",
		},
		{
			name: "multiline and splits with leading operator",
			input: `let x = a
  and b`,
			expected: `let x = a
  and b
`,
		},
		{
			name: "multiline or splits with leading operator",
			input: `let x = a
  or b`,
			expected: `let x = a
  or b
`,
		},
		{
			name: "chained multiline and",
			input: `let x = a
  and b
  and c`,
			expected: `let x = a
  and b
  and c
`,
		},
		{
			name: "chained multiline or",
			input: `let x = a
  or b
  or c`,
			expected: `let x = a
  or b
  or c
`,
		},
		{
			name:     "chained single-line and stays single-line",
			input:    `let x = a and b and c`,
			expected: "let x = a and b and c\n",
		},
		{
			name: "multiline and with complex operands",
			input: `let x = foo.bar == 1
  and baz.qux == 2`,
			expected: `let x = foo.bar == 1
  and baz.qux == 2
`,
		},
		{
			name: "multiline and inside if condition",
			input: `if (a
  and b) {
  x
}`,
			expected: `if (a
  and b) {
  x
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)

			// Verify idempotency
			result2, err := FormatFile([]byte(result))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result2, "formatting should be idempotent")
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
			name:  "single import with no following code",
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

func (FormatSuite) TestCommentsInBlockArgs(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "comment inside multiline block arg stays inside",
			input: `pub x = items.each { item =>
  # process item
  handle(item)
}`,
			expected: `pub x = items.each { item =>
  # process item
  handle(item)
}
`,
		},
		{
			name: "multiple comments inside multiline block arg",
			input: `pub x = items.each { item =>
  # first comment
  a(item)
  # second comment
  b(item)
}`,
			expected: `pub x = items.each { item =>
  # first comment
  a(item)
  # second comment
  b(item)
}
`,
		},
		{
			name: "comment inside block arg without params",
			input: `pub x = foo.bar {
  # a comment
  baz
}`,
			expected: `pub x = foo.bar {
  # a comment
  baz
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			result, err := FormatFile([]byte(tt.input))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)

			// Verify idempotency
			result2, err := FormatFile([]byte(result))
			require.NoError(t, err)
			require.Equal(t, tt.expected, result2, "formatting should be idempotent")
		})
	}
}

func (FormatSuite) TestMultilineDirectives(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "multiline suffix directives are preserved",
			input: `workspace: Directory!
  @defaultPath(path: "/")
  @ignorePatterns(patterns: [
    "*",
    "!sdk/elixir"
  ])`,
			expected: `workspace: Directory!
  @defaultPath(path: "/")
  @ignorePatterns(patterns: [
    "*",
    "!sdk/elixir",
  ])
`,
		},
		{
			name:  "single-line suffix directives stay on one line",
			input: `workspace: Directory! @defaultPath(path: "/")`,
			expected: `workspace: Directory! @defaultPath(path: "/")
`,
		},
		{
			name:  "multiple suffix directives on same line stay on one line",
			input: `x: Int! @foo @bar`,
			expected: `x: Int! @foo @bar
`,
		},
		{
			name: "multiline directives inside type body",
			input: `type Foo {
  workspace: Directory!
    @defaultPath(path: "/")
    @ignorePatterns(patterns: [
      "*",
      "!sdk/elixir"
    ])
}`,
			expected: `type Foo {
  workspace: Directory!
    @defaultPath(path: "/")
    @ignorePatterns(patterns: [
      "*",
      "!sdk/elixir",
    ])
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

func (FormatSuite) TestLegacyTryCatchMigration(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "single-expression body unwraps to postfix",
			input: `let x = try {
  risky
} catch {
  e: BasicError => e.message
}`,
			expected: `let x = risky rescue {
  e: BasicError => e.message
}
`,
		},
		{
			name: "bare binding becomes typed catch-all when read",
			input: `let x = try {
  risky
} catch {
  err => err.message
}`,
			expected: `let x = risky rescue {
  err: Error => err.message
}
`,
		},
		{
			name: "unused bare binding collapses to fallback form",
			input: `let x = try {
  risky
} catch {
  err => ""
}`,
			expected: `let x = risky rescue ""
`,
		},
		{
			name: "unused binding with null fallback",
			input: `let x = try {
  risky
} catch {
  err => null
}`,
			expected: `let x = risky rescue null
`,
		},
		{
			name: "unused binding with block expr keeps clause form",
			input: `let x = try {
  risky
} catch {
  err => { a; b }
}`,
			expected: `let x = risky rescue {
  err: Error => { a; b }
}
`,
		},
		{
			name: "multi-form body keeps its block",
			input: `let x = try {
  let y = risky
  raise y
} catch {
  err => ""
}`,
			expected: `let x = {
  let y = risky
  raise y
} rescue ""
`,
		},
		{
			name: "default-expr body keeps its block",
			input: `let x = try {
  risky ?? "missing"
} catch {
  err => err.message
}`,
			expected: `let x = {
  risky ?? "missing"
} rescue {
  err: Error => err.message
}
`,
		},
		{
			name: "raise body keeps its block",
			input: `let x = try {
  raise "boom"
} catch {
  err => ""
}`,
			expected: `let x = {
  raise "boom"
} rescue ""
`,
		},
		{
			name: "zero-clause handler is dropped",
			input: `let x = try {
  risky
} catch { }`,
			expected: `let x = risky
`,
		},
		{
			name: "multiple clauses keep clause form",
			input: `let x = try {
  risky
} catch {
  v: ValidationError => v.field
  err => ""
}`,
			expected: `let x = risky rescue {
  v: ValidationError => v.field
  err: Error => ""
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
