package analysis_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

// TestPitfalls checks each pitfall (see dang.Pitfall) end to end: source in,
// rendered diagnostics out, as a CLI or a tool shows them. A row with no want
// is a negative case: code that looks like a pitfall that none of its
// diagnostics may call one. It must have no diagnostics at all unless it's
// marked invalid.
//
// It runs in both builds: with cgo, unmatched parse errors are diagnosed by
// tree-sitter; without, by the token the parser failed at. A pitfall reads
// the same either way, since it is detected from the parser's failure alone.
func TestPitfalls(t *testing.T) {
	for _, tt := range []struct {
		name    string
		src     string
		version dang.Version
		want    string
		invalid bool
	}{
		// line-comment
		{
			name: "line comment after code",
			src:  "let x = 1 // one\n",
			want: "main.dang:1:11: error: syntax error: `//` is not a comment\n" +
				"  hint: comments start with `#`\n" +
				"  docs: https://danglang.org/syntax#comments\n",
		},
		{
			name: "line comment on its own line",
			src:  "// one\nlet x = 1\n",
			want: "main.dang:1:1: error: syntax error: `//` is not a comment\n" +
				"  hint: comments start with `#`\n" +
				"  docs: https://danglang.org/syntax#comments\n",
		},
		{name: "slashes in a string", src: "let url = \"http://example.com\"\nprint(url)\n"},
		{name: "slashes in an interpolating string", src: "let host = \"example.com\"\nprint(\"https://${host}/x // y\")\n"},
		{name: "slashes in a verbatim string", src: "print('https://${HOST} // y')\nprint('''\n  // z\n''')\n"},
		{name: "slashes in a comment", src: "let x = 1 # see http://example.com\n"},
		{
			name: "line comment after an interpolating string",
			src:  "let host = \"example.com\"\nlet url = \"https://${host}\" // the URL\n",
			want: "main.dang:2:29: error: syntax error: `//` is not a comment\n" +
				"  hint: comments start with `#`\n" +
				"  docs: https://danglang.org/syntax#comments\n",
		},
		{name: "spaced division", src: "let x = 1 / / 2\n", invalid: true},
		// Python's floor division, not a comment.
		{name: "floor division", src: "let n = 10\nlet half = n // 2\n", invalid: true},
		{name: "floor division in an expression", src: "let n = 10\nprint(n // 2 + 1)\n", invalid: true},
		{name: "floor division by a name", src: "let total = 10\nlet count = 2\nlet avg = total // count\n", invalid: true},
		{name: "floor division and a comment", src: "let n = 10\nlet half = n // 2 # halve it\n", invalid: true},
		{
			name: "one-word line comment",
			src:  "let x = 1 // TODO\n",
			want: "main.dang:1:11: error: syntax error: `//` is not a comment\n" +
				"  hint: comments start with `#`\n" +
				"  docs: https://danglang.org/syntax#comments\n",
		},
		{
			// It fails at the second slash, continuing the line before.
			name: "commented-out code on its own line",
			src:  "let x = 1\n// print(x)\n",
			want: "main.dang:2:1: error: syntax error: `//` is not a comment\n" +
				"  hint: comments start with `#`\n" +
				"  docs: https://danglang.org/syntax#comments\n",
		},

		// logical-operators
		{
			name: "&&",
			src:  "let a = true\nlet b = a && false\n",
			want: "main.dang:2:11: error: syntax error: `&&` is not an operator\n" +
				"  hint: the logical operators are the keywords `and` and `or` (and `!` to negate)\n" +
				"  docs: https://danglang.org/operators#logical\n",
		},
		{
			name: "||",
			src:  "let ok = true || false\n",
			want: "main.dang:1:15: error: syntax error: `||` is not an operator\n" +
				"  hint: the logical operators are the keywords `and` and `or` (and `!` to negate)\n" +
				"  docs: https://danglang.org/operators#logical\n",
		},
		{name: "and/or", src: "let a = true\nprint(a and !false or false)\n"},
		{name: "&& in a string", src: "let s = \"a && b\"\nprint(s)\n"},
		{name: "&& in strings that interpolate", src: "let a = \"x\"\nprint(\"${a} && b\")\nprint(`${a} && b`)\nprint('${a} && b')\n"},
		{
			// An interpolation is code.
			name: "&& in an interpolation",
			src:  "let a = true\nprint(\"${a && a}\")\n",
			want: "main.dang:2:12: error: syntax error: `&&` is not an operator\n" +
				"  hint: the logical operators are the keywords `and` and `or` (and `!` to negate)\n" +
				"  docs: https://danglang.org/operators#logical\n",
		},
		{
			// A leading `&&` parses as `&(&false)`.
			name: "&& starting a line",
			src:  "let a = true\nlet ok = a\n  && false\n",
			want: "main.dang:3:3: error: & requires a function, got Boolean!\n" +
				"  hint: the logical operators are the keywords `and` and `or` (and `!` to negate)\n" +
				"  docs: https://danglang.org/operators#logical\n",
		},
		{name: "& reference", src: "f(x: Int!): Int! { x }\nlet g = &f\nprint(g(1))\n"},
		{name: "& of a non-function", src: "let a = 1\nlet g = &a\n", invalid: true},

		// loop-keywords
		{
			name: "for-in loop",
			src:  "for x in [1, 2] {\n  print(x)\n}\n",
			want: "main.dang:1:1: error: syntax error: Dang has no `for` loop\n" +
				"  hint: iterate with `xs.each { x => ... }`, or repeat with `loop { ... }` and `break`\n" +
				"  docs: https://danglang.org/control-flow#loop\n",
		},
		{
			name: "while loop",
			src:  "let i = 0\nwhile (i < 3) {\n  i += 1\n}\n",
			want: "main.dang:2:1: error: syntax error: Dang has no `while` loop\n" +
				"  hint: iterate with `xs.each { x => ... }`, or repeat with `loop { ... }` and `break`\n" +
				"  docs: https://danglang.org/control-flow#loop\n",
		},
		{
			// `for { ... }` parses as a call of a function named for.
			name: "for that parses as a call",
			src:  "for {\n  break\n}\n",
			want: "main.dang:1:1: error: \"for\" not found\n" +
				"  hint: iterate with `xs.each { x => ... }`, or repeat with `loop { ... }` and `break`\n" +
				"  docs: https://danglang.org/control-flow#loop\n",
		},
		{
			name: "while that parses as a call",
			src:  "while(true) {\n  break\n}\n",
			want: "main.dang:1:1: error: \"while\" not found\n" +
				"  hint: iterate with `xs.each { x => ... }`, or repeat with `loop { ... }` and `break`\n" +
				"  docs: https://danglang.org/control-flow#loop\n",
		},
		{name: "each and loop", src: "[1, 2].each { x => print(x) }\nloop { break }\n"},
		{name: "for as a name", src: "let for = 1\nprint(for)\n"},
		{name: "other undefined name", src: "forEach { break }\n", invalid: true},
		{
			// Before 2.1, `for (cond) { ... }` is a loop.
			name:    "for loop in Dang 2.0",
			version: dang.Version2_0,
			src:     "for (x in [1]) {\n}\n",
			invalid: true,
		},

		// single-brace-record
		{
			name: "single-brace record",
			src:  "let p = { name: \"a\", age: 1 }\n",
			want: "main.dang:1:9: error: syntax error: a record literal needs double braces\n" +
				"  hint: record literals use double braces: `{{ name: value }}`; `{ ... }` is a block\n" +
				"  docs: https://danglang.org/syntax#record-literals\n",
		},
		{
			name: "single-brace record argument",
			src:  "print({\n  name: \"a\"\n})\n",
			want: "main.dang:1:7: error: syntax error: a record literal needs double braces\n" +
				"  hint: record literals use double braces: `{{ name: value }}`; `{ ... }` is a block\n" +
				"  docs: https://danglang.org/syntax#record-literals\n",
		},
		{
			// The parser fails inside `true`, past a type variable `t`.
			name: "single-brace record of a keyword",
			src:  "let p = { ok: true }\n",
			want: "main.dang:1:9: error: syntax error: a record literal needs double braces\n" +
				"  hint: record literals use double braces: `{{ name: value }}`; `{ ... }` is a block\n" +
				"  docs: https://danglang.org/syntax#record-literals\n",
		},
		{
			name: "single-brace record of a name",
			src:  "let name = \"a\"\nlet p = { name: name }\n",
			want: "main.dang:2:9: error: syntax error: a record literal needs double braces\n" +
				"  hint: record literals use double braces: `{{ name: value }}`; `{ ... }` is a block\n" +
				"  docs: https://danglang.org/syntax#record-literals\n",
		},
		{name: "double-brace record", src: "let p = {{ name: \"a\", age: 1 }}\nprint(p.name)\n"},
		{name: "field without a type in a type body", src: "type Foo {\n  name: \"a\"\n}\n", invalid: true},
		{
			// The mistake is the type: `int` for `Int!`.
			name:    "declaration with a lowercase type in a block",
			src:     "let v = {\n  count: int = 1\n  count\n}\nprint(v)\n",
			invalid: true,
		},
		{
			name: "single-brace record of an interpolating string",
			src:  "let n = \"Ada\"\nlet p = { name: \"hi ${n}\" }\n",
			want: "main.dang:2:9: error: syntax error: a record literal needs double braces\n" +
				"  hint: record literals use double braces: `{{ name: value }}`; `{ ... }` is a block\n" +
				"  docs: https://danglang.org/syntax#record-literals\n",
		},
		{name: "braces in strings", src: "let name = \"Ada\"\nprint(\"{ name: ${name} }\")\nprint('{ name: x }')\n"},

		// interpolating strings
		{name: "loop words in strings", src: "let x = 1\nprint(\"for ${x} in xs, while ${x}\")\n"},
		{
			// It has a hint of its own: write it as text.
			name:    "undefined name in an interpolation",
			src:     "print(\"${while}\")\n",
			invalid: true,
		},
		{name: "a record in an interpolation", src: "print(\"${ {{ a: 1 }}.a }\")\n"},

		// truthiness
		{
			name: "list condition",
			src:  "let xs = [1]\nif (xs) { print(\"some\") }\n",
			want: "main.dang:2:5: error: condition must be Boolean!, got [Int!]!\n" +
				"  hint: there is no truthiness: test explicitly, e.g. `x != null` or `!xs.isEmpty`\n" +
				"  docs: https://danglang.org/control-flow#if-else\n",
		},
		{
			name: "logical operand",
			src:  "let n = 1\nprint(n and true)\n",
			want: "main.dang:2:7: error: logical 'and' left operand must be Boolean!, got Int!\n" +
				"  hint: there is no truthiness: test explicitly, e.g. `x != null` or `!xs.isEmpty`\n" +
				"  docs: https://danglang.org/control-flow#if-else\n",
		},
		{
			name: "negated string",
			src:  "let s = \"x\"\nprint(!s)\n",
			want: "main.dang:2:7: error: the operand of ! must be Boolean!, got String!\n" +
				"  hint: there is no truthiness: test explicitly, e.g. `x != null` or `!xs.isEmpty`\n" +
				"  docs: https://danglang.org/control-flow#if-else\n",
		},
		{name: "explicit test", src: "let xs = [1]\nif (!xs.isEmpty) { print(\"some\") }\n"},
		{
			// A nullable Boolean has advice of its own.
			name:    "nullable Boolean condition",
			src:     "let b: Boolean = null\nif (b) { print(\"yes\") }\n",
			invalid: true,
		},
		{
			// Decoded data needs a type, not a test.
			name:    "Decoded condition",
			src:     "let ok = YAML.decode(\"true\")\nif (ok) { print(\"yes\") }\n",
			invalid: true,
		},
		{
			// A function needs a call, not a test.
			name:    "function condition",
			src:     "positive(x: Int!): Boolean! { x > 0 }\nif (&positive) { print(\"yes\") }\n",
			invalid: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			src := mapFS(map[string]string{"main.dang": tt.src})
			a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{Version: tt.version})
			require.NoError(t, err)
			diags := a.Diagnostics()
			rendered := analysis.Render(diags, analysis.RenderOptions{})
			if tt.want == "" {
				if !tt.invalid {
					require.Empty(t, diags, "valid code:\n%s", rendered)
					return
				}
				require.NotEmpty(t, diags, "invalid code")
				for _, d := range diags {
					require.Empty(t, d.Pitfall, "not a pitfall:\n%s", rendered)
				}
				return
			}
			require.Equal(t, tt.want, rendered)
		})
	}
}
