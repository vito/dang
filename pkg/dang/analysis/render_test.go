package analysis_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

func TestRender(t *testing.T) {
	src := mapFS(map[string]string{"main.dang": `type Person {
  """
  The person's name.
  """
  name: String!

  """
  Greets someone.
  """
  greet(greeting: String! = "hi"): String! {
    greeting + ", " + name
  }
}

pub p = Person(name: "x")
pub n = p.nmae
pub g = p.great
pub x = "a" + 1
`})
	a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{DiagnosticContext: true})
	require.NoError(t, err)

	require.Equal(t, `main.dang:16:9: error: field "nmae" not found in Person
  closest: name: String!  The person's name.
  Person! has 2 members: greet name
main.dang:17:9: error: field "great" not found in Person
  closest: greet(greeting: String): String!  Greets someone.
  Person! has 2 members: greet name
main.dang:18:9: error: operator addition is not defined between types String! and Int!
`, analysis.Render(a.Diagnostics(), analysis.RenderOptions{}))

	// Without the context, each diagnostic is its one line.
	a, err = analysis.Analyze(context.Background(), src, ".", analysis.Options{})
	require.NoError(t, err)
	require.Equal(t, `main.dang:16:9: error: field "nmae" not found in Person
main.dang:17:9: error: field "great" not found in Person
main.dang:18:9: error: operator addition is not defined between types String! and Int!
`, analysis.Render(a.Diagnostics(), analysis.RenderOptions{}))
}

func TestRenderCapsAndWrapsNames(t *testing.T) {
	d := diagnose(t, `type Point {
  x: Int!
  y: Int!
  z: Int!
  label: String!
}

pub p = Point(x: 1, y: 2, z: 3, label: "origin")
pub w = p.w
`)
	out := d.Format(analysis.RenderOptions{MaxNames: 3, Width: 30})
	require.Equal(t, `main.dang:9:9: error: field "w" not found in Point
  Point! has 4 members: label
    x y (1 more)`, out)
}

func TestRenderSignatureAndSearch(t *testing.T) {
	d := diagnose(t, "pub x = [1].reduce\n")
	require.Equal(t, `main.dang:1:9: error: function requires a block argument
  signature: List.reduce(initial: b) { acc: b, item: a => b }: b  reduces the list to a single value using an accumulator function`,
		d.Format(analysis.RenderOptions{}))

	d = diagnose(t, "pub x = sortStrings([\"b\", \"a\"])\n")
	require.Contains(t, d.Format(analysis.RenderOptions{}),
		"\n  docs matching \"sort strings\": List.sort[a: Ordered](descending: Boolean = false): [a]!  returns a new list")
}
