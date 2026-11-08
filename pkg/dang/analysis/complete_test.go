package analysis_test

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

func TestCompleteListsTheSameMembers(t *testing.T) {
	ctx := context.Background()
	scope := testScope(t, membersSource)

	for _, input := range []string{
		`"a".spl`,
		`"a".`,
		`[1, 2].re`,
		`["a": 1].`,
		`Person(name: "a").`,
		`Test.serverInfo.`,
		`ad`,
		`add(`,
		`add(x: 1, `,
	} {
		t.Run(input, func(t *testing.T) {
			pos := analysis.Pos{Line: 1, Column: len(input) + 1}
			got := analysis.Complete(ctx, scope, input, pos)
			want := dang.Complete(ctx, scope, input, 0, len(input))
			if len(want.Items) == 0 {
				t.Skip("no completions without cgo")
			}
			require.Equal(t, want.ReplaceFrom, got.ReplaceFrom)
			require.Equal(t, labels(want.Items), labels(got.Items))
		})
	}
}

func TestCompleteDocumentsBuiltins(t *testing.T) {
	ctx := context.Background()
	scope := testScope(t, membersSource)

	got := analysis.Complete(ctx, scope, "Random.", analysis.Pos{Line: 1, Column: len("Random.") + 1})
	if len(got.Items) == 0 {
		t.Skip("no completions without cgo")
	}
	for _, item := range got.Items {
		if item.Label == "int" {
			require.NotEmpty(t, item.Documentation, "static builtins are documented from their definitions")
			return
		}
	}
	t.Fatalf("no Random.int in %v", labels(got.Items))
}

func TestCompleteNamespaceMembers(t *testing.T) {
	ctx := context.Background()
	scope := testScope(t, membersSource)

	got := analysis.Complete(ctx, scope, "Test.", analysis.Pos{Line: 1, Column: len("Test.") + 1})
	if len(got.Items) == 0 {
		t.Skip("no completions without cgo")
	}
	require.Contains(t, labels(got.Items), "serverInfo")
	require.NotContains(t, labels(got.Items), "print", "the prelude an import sees isn't its members")
}

func labels(items []dang.Completion) []string {
	var ls []string
	for _, item := range items {
		ls = append(ls, item.Label)
	}
	sort.Strings(ls)
	return ls
}
