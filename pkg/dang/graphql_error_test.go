package dang

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestGraphQLErrorFieldPath(t *testing.T) {
	t.Run("extracts names and skips indices", func(t *testing.T) {
		err := gqlerror.List{{
			Message: "boom",
			Path:    ast.Path{ast.PathName("viewer"), ast.PathName("repositories"), ast.PathIndex(2), ast.PathName("name")},
		}}
		require.Equal(t, []string{"viewer", "repositories", "name"}, graphqlErrorFieldPath(err))
	})

	t.Run("wrapped error", func(t *testing.T) {
		err := fmt.Errorf("executing GraphQL query: %w", gqlerror.List{{
			Path: ast.Path{ast.PathName("viewer"), ast.PathName("repositories")},
		}})
		require.Equal(t, []string{"viewer", "repositories"}, graphqlErrorFieldPath(err))
	})

	t.Run("no path", func(t *testing.T) {
		require.Nil(t, graphqlErrorFieldPath(gqlerror.List{{Message: "boom"}}))
	})

	t.Run("not a gqlerror", func(t *testing.T) {
		require.Nil(t, graphqlErrorFieldPath(fmt.Errorf("plain error")))
	})
}

func TestLocateErrorField(t *testing.T) {
	// viewer.{ login, repositories(first: 3).{ nodes.{ name, stargazerCount } } }
	name := &FieldSelection{Name: "name", Loc: &SourceLocation{Line: 7}}
	nodes := &FieldSelection{
		Name:      "nodes",
		Loc:       &SourceLocation{Line: 6},
		Selection: &ObjectSelection{Fields: []*FieldSelection{name}},
	}
	repositories := &FieldSelection{
		Name:      "repositories",
		Loc:       &SourceLocation{Line: 5},
		Selection: &ObjectSelection{Fields: []*FieldSelection{nodes}},
	}
	sel := &ObjectSelection{Fields: []*FieldSelection{
		{Name: "login", Loc: &SourceLocation{Line: 4}},
		repositories,
	}}

	t.Run("skips receiver prefix and matches field", func(t *testing.T) {
		got := sel.locateErrorField([]string{"viewer", "repositories"})
		require.Same(t, repositories, got)
	})

	t.Run("descends into nested selections", func(t *testing.T) {
		got := sel.locateErrorField([]string{"viewer", "repositories", "nodes", "name"})
		require.Same(t, name, got)
	})

	t.Run("stops at deepest known field", func(t *testing.T) {
		// 'stargazerCount' isn't selected, so the deepest match is 'nodes'.
		got := sel.locateErrorField([]string{"viewer", "repositories", "nodes", "stargazerCount"})
		require.Same(t, nodes, got)
	})

	t.Run("no match", func(t *testing.T) {
		require.Nil(t, sel.locateErrorField([]string{"viewer", "followers"}))
	})

	t.Run("empty path", func(t *testing.T) {
		require.Nil(t, sel.locateErrorField(nil))
	})
}
