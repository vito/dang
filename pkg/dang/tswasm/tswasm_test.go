//go:build !js

package tswasm_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang/tswasm"
)

func TestParseFindsErrors(t *testing.T) {
	root, err := tswasm.Parse(context.Background(), []byte("let x = (1 + 2\n"), tswasm.PruneNone)
	require.NoError(t, err)
	require.Equal(t, "dang", root.Kind)
	require.True(t, root.HasError)

	missing := find(root, func(n *tswasm.Node) bool { return n.Missing })
	require.NotNil(t, missing, "no MISSING node")
	require.Equal(t, ")", missing.Kind)
	require.Equal(t, uint32(0), missing.StartRow)
	require.Equal(t, uint32(14), missing.StartColumn)
}

func TestParsePrunesErrorFreeSubtrees(t *testing.T) {
	src := []byte("let ok = [1, 2, 3]\nlet bad = (1 + 2\n")
	full, err := tswasm.Parse(context.Background(), src, tswasm.PruneNone)
	require.NoError(t, err)
	pruned, err := tswasm.Parse(context.Background(), src, tswasm.PruneErrorFree)
	require.NoError(t, err)

	require.Less(t, count(pruned), count(full))
	require.NotNil(t, find(pruned, func(n *tswasm.Node) bool { return n.Missing }))
	for _, n := range all(pruned) {
		if !n.HasError {
			require.Empty(t, n.Children(), "%s keeps its children without errors", n.Kind)
		}
	}
}

func TestParseEmpty(t *testing.T) {
	root, err := tswasm.Parse(context.Background(), nil, tswasm.PruneNone)
	require.NoError(t, err)
	require.False(t, root.HasError)
}

func TestParseStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tswasm.Parse(ctx, []byte("let x = 1\n"), tswasm.PruneNone)
	require.ErrorIs(t, err, context.Canceled)

	// A deadline stops a parse already under way.
	big := []byte(strings.Repeat("let x = [1, 2, (3 + 4)]\n", 200_000))
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err = tswasm.Parse(ctx, big, tswasm.PruneNone)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// The parse that was stopped leaves no broken instance behind.
	for range 8 {
		root, err := tswasm.Parse(context.Background(), []byte("let x = 1\n"), tswasm.PruneNone)
		require.NoError(t, err)
		require.False(t, root.HasError)
	}
}

func TestParseConcurrently(t *testing.T) {
	src := []byte("let x = (1 + 2\n")
	want, err := tswasm.Parse(context.Background(), src, tswasm.PruneNone)
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := tswasm.Parse(context.Background(), src, tswasm.PruneNone)
			if err == nil && count(got) != count(want) {
				err = errors.New("trees differ")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

// TestDocumentMatchesFreshParses types into, and deletes from, a source a
// byte at a time, here and there, and requires each incremental reparse to
// give the tree a fresh parse gives.
func TestDocumentMatchesFreshParses(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "test_comprehensive.dang"))
	require.NoError(t, err)
	doc := tswasm.NewDocument()
	defer doc.Close()

	check := func(src []byte) {
		t.Helper()
		got, err := doc.Parse(context.Background(), src, tswasm.PruneNone)
		require.NoError(t, err)
		want, err := tswasm.Parse(context.Background(), src, tswasm.PruneNone)
		require.NoError(t, err)
		require.Equal(t, dump(want), dump(got))
	}

	src := base
	check(src)
	typed := "let y = foo.bar(baz: [1, 2], qux: \"s\")\n"
	for _, at := range []int{0, len(base) / 3, len(base) / 2, len(base)} {
		at = bytes.IndexByte(base[at:], '\n') + at + 1
		if at > len(src) {
			at = len(src)
		}
		// Type it in.
		for i := 1; i <= len(typed); i++ {
			src = slices.Concat(base[:at], []byte(typed[:i]), base[at:])
			check(src)
		}
		// Delete it again, from the front.
		for i := 1; i <= len(typed); i++ {
			src = slices.Concat(base[:at], []byte(typed[i:]), base[at:])
			check(src)
		}
	}
	// Something else entirely.
	check([]byte("let x = (1 + 2\n"))
	check(nil)
	check(base)
}

// TestInspectReadsTheWholeTree requires a tree read lazily to read the same
// as one decoded at once, for every test source.
func TestInspectReadsTheWholeTree(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "tests", "*", "*.dang"))
	require.NoError(t, err)
	more, err := filepath.Glob(filepath.Join("..", "..", "..", "tests", "*.dang"))
	require.NoError(t, err)
	files = append(files, more...)
	require.NotEmpty(t, files)
	doc := tswasm.NewDocument()
	defer doc.Close()
	for _, f := range files {
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		want, err := tswasm.Parse(context.Background(), src, tswasm.PruneNone)
		require.NoError(t, err)
		var got string
		require.NoError(t, tswasm.Inspect(context.Background(), src, func(root *tswasm.Node) error {
			got = dump(root)
			return nil
		}), f)
		require.Equal(t, dump(want), got, f)
		require.NoError(t, doc.Inspect(context.Background(), src, func(root *tswasm.Node) error {
			got = dump(root)
			return nil
		}), f)
		require.Equal(t, dump(want), got, f)
	}

	// Past Inspect, unread children read as none.
	var root *tswasm.Node
	require.NoError(t, tswasm.Inspect(context.Background(), []byte("let x = 1\n"), func(r *tswasm.Node) error {
		root = r
		return nil
	}))
	require.Empty(t, root.Children())
}

func TestQueryPredicates(t *testing.T) {
	q, err := tswasm.NewQuery(`
((symbol) @builtin (#match? @builtin "^(print|assert)$"))
((symbol) @not-x (#not-eq? @not-x "x"))
((symbol) @one-of (#any-of? @one-of "a" "b"))
((symbol) @directive (#set! priority 1))
`)
	require.NoError(t, err)
	require.Equal(t, []string{"builtin", "not-x", "one-of", "directive"}, q.CaptureNames())

	src := []byte("print(x)\nlet y = a(1) + b(2) + c(3) + assert(true)\n")
	captures, err := q.Captures(context.Background(), src)
	require.NoError(t, err)
	got := map[string][]string{}
	for _, c := range captures {
		name := q.CaptureNames()[c.Index]
		got[name] = append(got[name], string(src[c.StartByte:c.EndByte]))
	}
	require.Equal(t, []string{"print", "assert"}, got["builtin"])
	require.NotContains(t, got["not-x"], "x")
	require.Equal(t, []string{"a", "b"}, got["one-of"])
	require.Contains(t, got["directive"], "y")
}

func TestQueryErrors(t *testing.T) {
	_, err := tswasm.NewQuery(`(no_such_node) @x`)
	var qe *tswasm.QueryError
	require.ErrorAs(t, err, &qe)
	require.Equal(t, "node type", qe.Kind)
}

func dump(n *tswasm.Node) string {
	var b strings.Builder
	var walk func(n *tswasm.Node, depth int)
	walk = func(n *tswasm.Node, depth int) {
		fmt.Fprintf(&b, "%s%s:%q [%d:%d-%d:%d] %d-%d %t %t %t %t %t\n", strings.Repeat("  ", depth),
			n.Field, n.Kind, n.StartRow, n.StartColumn, n.EndRow, n.EndColumn,
			n.StartByte, n.EndByte, n.Error, n.Missing, n.HasError, n.Named, n.Extra)
		for _, c := range n.Children() {
			if c.Parent != n {
				b.WriteString("(not its parent's child)\n")
			}
			walk(c, depth+1)
		}
	}
	walk(n, 0)
	return b.String()
}

// BenchmarkParse is the steady-state cost of a recovery parse: the module is
// compiled and an instance is pooled after the first iteration.
func BenchmarkParse(b *testing.B) {
	for _, file := range []string{
		"tests/errors/syntax_unclosed_paren.dang",
		"tests/test_comprehensive.dang",
	} {
		src, err := os.ReadFile(filepath.Join("..", "..", "..", file))
		require.NoError(b, err)
		b.Run(filepath.Base(file), func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := tswasm.Parse(context.Background(), src, tswasm.PruneErrorFree); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func all(n *tswasm.Node) []*tswasm.Node {
	nodes := []*tswasm.Node{n}
	for _, c := range n.Children() {
		nodes = append(nodes, all(c)...)
	}
	return nodes
}

func count(n *tswasm.Node) int { return len(all(n)) }

func find(n *tswasm.Node, pred func(*tswasm.Node) bool) *tswasm.Node {
	for _, m := range all(n) {
		if pred(m) {
			return m
		}
	}
	return nil
}
