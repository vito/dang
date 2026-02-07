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
			name: "single-line chain with multiline block stays together",
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
			name: "nested multiline block args",
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
			expected: `pub x = """
hello
world
"""
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
			name: "multiline block arg no trailing space after arrow",
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
			// NOTE: extra blank line after { is bug #2, will be fixed
			expected: `pub readFile(
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
