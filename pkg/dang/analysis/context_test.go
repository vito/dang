package analysis_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

// diagnose analyzes main.dang with diagnostic context and returns its one
// diagnostic.
func diagnose(t *testing.T, source string) analysis.Diagnostic {
	t.Helper()
	return diagnoseWith(t, source, analysis.Options{DiagnosticContext: true})
}

func diagnoseWith(t *testing.T, source string, opts analysis.Options) analysis.Diagnostic {
	t.Helper()
	a, err := analysis.Analyze(context.Background(), mapFS(map[string]string{"main.dang": source}), ".", opts)
	require.NoError(t, err)
	diags := a.Diagnostics()
	require.Len(t, diags, 1)
	return diags[0]
}

func TestContextIsOptIn(t *testing.T) {
	for _, source := range []string{
		"pub x = [1].lenght\n",            // an unknown method
		"pub x = sortStrings([\"b\"])\n", // an unknown name
		"pub x = [1].reduce\n",            // a member missing its block
	} {
		off := diagnoseWith(t, source, analysis.Options{})
		require.Nil(t, off.Context, source)
		payload, err := json.Marshal(off)
		require.NoError(t, err)
		require.NotContains(t, string(payload), "context")

		on := diagnose(t, source)
		require.NotNil(t, on.Context, source)
		require.Equal(t, off.Message, on.Message, "the message is the same either way")
		require.Equal(t, off.Range, on.Range)
	}
}

func TestContextOfAnUnknownMethod(t *testing.T) {
	d := diagnose(t, "let names = [\"b\", \"a\"]\npub x = names.findFirst { n => n == \"a\" }\n")
	// The message is the type checker's own.
	require.Equal(t, `list does not have method "findFirst"`, d.Message)

	c := d.Context
	require.NotNil(t, c)
	require.Equal(t, "findFirst", c.Name)
	require.Equal(t, &analysis.Range{
		Start: analysis.Pos{Line: 2, Column: 15},
		End:   analysis.Pos{Line: 2, Column: 15 + len("findFirst")},
	}, c.NameRange)
	require.Nil(t, c.Member)
	require.Equal(t, "[String!]!", c.Receiver)
	require.Contains(t, names(c.Has), "find")
	require.Contains(t, names(c.Has), "sortBy")
	require.Equal(t, []string{"find"}, names(c.Near))
	require.Equal(t, "List", c.Near[0].Receiver)
}

func TestContextSearchesTheReceiversDocs(t *testing.T) {
	d := diagnose(t, "let title = \"First line\"\npub x = title.substring(0, 5)\n")
	c := d.Context
	require.NotNil(t, c)
	require.Equal(t, "String!", c.Receiver)
	require.Contains(t, names(c.Has), "truncate")
	require.Empty(t, c.Near, "nothing is spelled like substring")
	require.Equal(t, "substring", c.Query)
	require.NotEmpty(t, c.Search)
	require.Equal(t, "String.contains", c.Search[0].Qualified())
}

func TestContextOfAnUnknownName(t *testing.T) {
	d := diagnose(t, "pub x = sortStrings([\"b\", \"a\"])\n")
	require.Equal(t, `"sortStrings" not found`, d.Message)
	c := d.Context
	require.NotNil(t, c)
	require.Equal(t, "sortStrings", c.Name)
	require.Equal(t, &analysis.Range{
		Start: analysis.Pos{Line: 1, Column: 9},
		End:   analysis.Pos{Line: 1, Column: 9 + len("sortStrings")},
	}, c.NameRange)
	require.Empty(t, c.Receiver)
	require.Empty(t, c.Has, "a scope's members aren't listed")
	require.Equal(t, "sort strings", c.Query)
	require.NotEmpty(t, c.Search)
	require.Equal(t, "List.sort", c.Search[0].Qualified())
}

func TestContextSeesLocals(t *testing.T) {
	d := diagnose(t, "pub f(items: [String!]!): Int! {\n  let count = items.length\n  itmes.length + cuont\n}\n")
	c := d.Context
	require.NotNil(t, c)
	require.Equal(t, "itmes", c.Name)
	require.Equal(t, []string{"items"}, names(c.Near))
}

func TestContextLeavesOutNamesNotYetDeclared(t *testing.T) {
	// A later local is what's missing, not the answer.
	d := diagnose(t, "pub f: Int! {\n  let a = total + 1\n  let total = 2\n  total\n}\n")
	require.Equal(t, `"total" not found`, d.Message)
	require.NotNil(t, d.Context)
	require.Nil(t, d.Context.Member)
	require.NotContains(t, names(d.Context.Near), "total")

	d = diagnose(t, "pub f: Int! {\n  let a = totl + 1\n  let total = 2\n  total\n}\n")
	require.NotNil(t, d.Context)
	require.NotContains(t, names(d.Context.Near), "total", "total isn't declared yet")

	// Nor is a declaration's own name, in its value.
	d = diagnose(t, "pub total = total + 1\n")
	require.NotNil(t, d.Context)
	require.Nil(t, d.Context.Member)
}

func TestContextSearchesOnlyMeaningfulWords(t *testing.T) {
	d := diagnose(t, "pub f: Int! {\n  let x = 1\n  y + 1\n}\n")
	require.NotNil(t, d.Context)
	require.Empty(t, d.Context.Query)
	require.Empty(t, d.Context.Search)
}

func TestContextOfAnExistingMember(t *testing.T) {
	// The member exists; the call is missing its block, so the context is
	// the signature it needs.
	d := diagnose(t, "pub x = [1].reduce\n")
	require.Equal(t, "function requires a block argument", d.Message)
	c := d.Context
	require.NotNil(t, c)
	require.NotNil(t, c.Member)
	require.Equal(t, "List.reduce", c.Member.Qualified())
	require.NotNil(t, c.Member.Block)
	require.Empty(t, c.Has)
	require.Empty(t, c.Near)
}

func TestContextOfAUserType(t *testing.T) {
	d := diagnose(t, `type Person {
  """
  The person's name.
  """
  name: String!
}

pub p = Person(name: "x")
pub n = p.nmae
`)
	c := d.Context
	require.NotNil(t, c)
	require.Equal(t, "Person!", c.Receiver)
	require.Equal(t, []string{"name"}, names(c.Has))
	require.Equal(t, []string{"name"}, names(c.Near))
	require.Equal(t, "Person", c.Near[0].Receiver)
}

func TestNoContextForOtherErrors(t *testing.T) {
	d := diagnose(t, "pub x = \"a\" + 1\n")
	require.Nil(t, d.Context)

	payload, err := json.Marshal(d)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "context")
}

func TestContextSerializes(t *testing.T) {
	d := diagnose(t, "pub x = [1].lenght\n")
	payload, err := json.Marshal(d)
	require.NoError(t, err)
	var decoded struct {
		Context struct {
			Name     string `json:"name"`
			Receiver string `json:"receiver"`
			Near     []struct {
				Name string `json:"name"`
			} `json:"near"`
		} `json:"context"`
	}
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Equal(t, "lenght", decoded.Context.Name)
	require.Equal(t, "[Int!]!", decoded.Context.Receiver)
	require.Len(t, decoded.Context.Near, 1)
	require.Equal(t, "length", decoded.Context.Near[0].Name)
}
