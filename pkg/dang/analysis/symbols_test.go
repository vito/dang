package analysis_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

// at returns the 1-based position of the nth (0-based) occurrence of needle
// in text, offset by off characters into it.
func at(t *testing.T, text, needle string, nth, off int) analysis.Pos {
	t.Helper()
	rest := text
	base := 0
	for i := 0; ; i++ {
		idx := strings.Index(rest, needle)
		require.GreaterOrEqual(t, idx, 0, "no occurrence %d of %q", nth, needle)
		if i == nth {
			offset := base + idx + off
			line := strings.Count(text[:offset], "\n") + 1
			col := offset - strings.LastIndex(text[:offset], "\n")
			return analysis.Pos{Line: line, Column: col}
		}
		base += idx + len(needle)
		rest = rest[idx+len(needle):]
	}
}

const shadowSource = `let x = 1

"""
Doubles a number.
"""
double(x: Int!): Int! {
  x * 2
}

blocky: Int! {
  let y = x
  let x = 10
  y + x
}

pub total = double(x) + blocky
`

func analyzeShadow(t *testing.T, files map[string]string) *analysis.Analysis {
	t.Helper()
	a, err := analysis.Analyze(context.Background(), mapFS(files), ".", analysis.Options{})
	require.NoError(t, err)
	require.Empty(t, a.Diagnostics())
	return a
}

func TestInspectResolvesShadowedLocals(t *testing.T) {
	a := analyzeShadow(t, map[string]string{"main.dang": shadowSource})
	src := shadowSource

	// x in double's body is its parameter, not the top-level x.
	sym, err := a.Inspect("main.dang", at(t, src, "x * 2", 0, 0))
	require.NoError(t, err)
	require.Equal(t, "x", sym.Name)
	require.Equal(t, &analysis.Location{Path: "main.dang", Range: analysis.Range{
		Start: at(t, src, "x: Int!", 0, 0),
		End:   at(t, src, "x: Int!", 0, 1),
	}}, sym.Definition)
	require.Equal(t, "Int!", sym.Type)

	// In a block, a let shadows only after it: y = x is the top-level x...
	sym, err = a.Inspect("main.dang", at(t, src, "let y = x", 0, len("let y = ")))
	require.NoError(t, err)
	require.Equal(t, at(t, src, "let x = 1", 0, len("let ")), sym.Definition.Range.Start)

	// ...and y + x the block's.
	sym, err = a.Inspect("main.dang", at(t, src, "y + x", 0, len("y + ")))
	require.NoError(t, err)
	require.Equal(t, at(t, src, "let x = 10", 0, len("let ")), sym.Definition.Range.Start)

	// The argument in double(x) is the top-level x.
	sym, err = a.Inspect("main.dang", at(t, src, "double(x)", 0, len("double(")))
	require.NoError(t, err)
	require.Equal(t, at(t, src, "let x = 1", 0, len("let ")), sym.Definition.Range.Start)

	// A function's docs come with it.
	sym, err = a.Inspect("main.dang", at(t, src, "double(x)", 0, 0))
	require.NoError(t, err)
	require.Equal(t, analysis.KindFunction, sym.Kind)
	require.Equal(t, "Doubles a number.", sym.Doc)
}

func TestReferencesOfShadowedLocals(t *testing.T) {
	a := analyzeShadow(t, map[string]string{"main.dang": shadowSource})
	src := shadowSource

	topX := a.References("main.dang", at(t, src, "let x = 1", 0, len("let ")))
	require.Equal(t, []analysis.Pos{
		at(t, src, "let x = 1", 0, len("let ")),
		at(t, src, "let y = x", 0, len("let y = ")),
		at(t, src, "double(x)", 0, len("double(")),
	}, starts(topX))

	param := a.References("main.dang", at(t, src, "x * 2", 0, 0))
	require.Equal(t, []analysis.Pos{
		at(t, src, "x: Int!", 0, 0),
		at(t, src, "x * 2", 0, 0),
	}, starts(param))

	blockX := a.References("main.dang", at(t, src, "let x = 10", 0, len("let ")))
	require.Equal(t, []analysis.Pos{
		at(t, src, "let x = 10", 0, len("let ")),
		at(t, src, "y + x", 0, len("y + ")),
	}, starts(blockX))
}

func TestReferencesAcrossFiles(t *testing.T) {
	a := analyzeShadow(t, map[string]string{
		"defs.dang": "pub greeting = \"hi\"\n",
		"main.dang": "pub loud = greeting.toUpper\n\nshadowed(greeting: String!): String! { greeting }\n",
	})

	sym, err := a.Inspect("main.dang", analysis.Pos{Line: 1, Column: len("pub loud = ") + 1})
	require.NoError(t, err)
	require.Equal(t, "defs.dang", sym.Definition.Path)

	refs := a.References("defs.dang", analysis.Pos{Line: 1, Column: len("pub ") + 1})
	require.Len(t, refs, 2, "the parameter of the same name isn't a reference: %v", refs)
	require.Equal(t, "defs.dang", refs[0].Path)
	require.Equal(t, "main.dang", refs[1].Path)
}

func TestInspectMembersAndBuiltins(t *testing.T) {
	src := `type Person {
  """
  The name.
  """
  name: String!
}

pub p = Person(name: "a")
pub n = p.name
pub up = n.toUpper
pub s = toString(1)
`
	a := analyzeShadow(t, map[string]string{"main.dang": src})

	// A field of a type declared in the Source.
	sym, err := a.Inspect("main.dang", at(t, src, "p.name", 0, 2))
	require.NoError(t, err)
	require.Equal(t, "name", sym.Name)
	require.Equal(t, "The name.", sym.Doc)
	require.Equal(t, at(t, src, "name: String!", 0, 0), sym.Definition.Range.Start)

	// A builtin method.
	sym, err = a.Inspect("main.dang", at(t, src, "n.toUpper", 0, 2))
	require.NoError(t, err)
	require.Nil(t, sym.Definition)
	require.NotNil(t, sym.Member)
	require.True(t, sym.Member.Builtin)
	require.NotEmpty(t, sym.Doc)

	// A builtin function.
	sym, err = a.Inspect("main.dang", at(t, src, "toString(1)", 0, 0))
	require.NoError(t, err)
	require.Nil(t, sym.Definition)
	require.Equal(t, `toString(42)`, sym.Member.Example)
	require.Nil(t, a.References("main.dang", at(t, src, "toString(1)", 0, 0)))

	// Nothing.
	_, err = a.Inspect("main.dang", analysis.Pos{Line: 6, Column: 1})
	require.Error(t, err)
}

func TestClauseBindingsShadow(t *testing.T) {
	src := `let err = "outer"

pub msg = (raise "boom") rescue {
  err: Error => err.message
}

pub other = err
`
	a := analyzeShadow(t, map[string]string{"main.dang": src})

	sym, err := a.Inspect("main.dang", at(t, src, "err.message", 0, 0))
	require.NoError(t, err)
	require.Equal(t, at(t, src, "err: Error", 0, 0), sym.Definition.Range.Start)

	require.Equal(t, []analysis.Pos{
		at(t, src, "let err", 0, len("let ")),
		at(t, src, "= err\n", 0, len("= ")),
	}, starts(a.References("main.dang", at(t, src, "let err", 0, len("let ")))))

	require.Equal(t, []analysis.Pos{
		at(t, src, "err: Error", 0, 0),
		at(t, src, "err.message", 0, 0),
	}, starts(a.References("main.dang", at(t, src, "err.message", 0, 0))))
}

func TestReferencesOfMembers(t *testing.T) {
	src := `type Person {
  name: String!

  greet: String! { "hi " + name }
}

pub p = Person(name: "a")
pub n = p.name
`
	a := analyzeShadow(t, map[string]string{"main.dang": src})

	want := []analysis.Pos{
		at(t, src, "name: String!", 0, 0),
		at(t, src, "+ name", 0, len("+ ")),
		at(t, src, "p.name", 0, len("p.")),
	}
	require.Equal(t, want, starts(a.References("main.dang", at(t, src, "name: String!", 0, 0))))
	require.Equal(t, want, starts(a.References("main.dang", at(t, src, "p.name", 0, len("p.")))))
}

func TestScopeAtSeesLocals(t *testing.T) {
	a := analyzeShadow(t, map[string]string{"main.dang": shadowSource})

	scope := a.ScopeAt("main.dang", at(t, shadowSource, "y + x", 0, 0))
	require.NotNil(t, scope)
	_, ok := scope.SchemeOf("y")
	require.True(t, ok, "a block's locals are in scope inside it")

	list, err := analysis.Members(context.Background(), scope, "y")
	require.NoError(t, err)
	require.Equal(t, "Int!", list.Type)

	top := a.ScopeAt("main.dang", analysis.Pos{Line: 1, Column: 1})
	_, ok = top.SchemeOf("y")
	require.False(t, ok)
}

func starts(locs []analysis.Location) []analysis.Pos {
	var ps []analysis.Pos
	for _, l := range locs {
		ps = append(ps, l.Range.Start)
	}
	return ps
}
