//go:build !js

package tswasm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang/tswasm"
)

func TestParseFindsErrors(t *testing.T) {
	root, err := tswasm.Parse(context.Background(), []byte("let x = (1 + 2\n"), false)
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
	full, err := tswasm.Parse(context.Background(), src, false)
	require.NoError(t, err)
	pruned, err := tswasm.Parse(context.Background(), src, true)
	require.NoError(t, err)

	require.Less(t, count(pruned), count(full))
	require.NotNil(t, find(pruned, func(n *tswasm.Node) bool { return n.Missing }))
	for _, n := range all(pruned) {
		if !n.HasError {
			require.Empty(t, n.Children, "%s keeps its children without errors", n.Kind)
		}
	}
}

func TestParseEmpty(t *testing.T) {
	root, err := tswasm.Parse(context.Background(), nil, false)
	require.NoError(t, err)
	require.False(t, root.HasError)
}

func TestParseStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tswasm.Parse(ctx, []byte("let x = 1\n"), false)
	require.ErrorIs(t, err, context.Canceled)

	// A deadline stops a parse already under way.
	big := []byte(strings.Repeat("let x = [1, 2, (3 + 4)]\n", 200_000))
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err = tswasm.Parse(ctx, big, false)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// The parse that was stopped leaves no broken instance behind.
	for range 8 {
		root, err := tswasm.Parse(context.Background(), []byte("let x = 1\n"), false)
		require.NoError(t, err)
		require.False(t, root.HasError)
	}
}

func TestParseConcurrently(t *testing.T) {
	src := []byte("let x = (1 + 2\n")
	want, err := tswasm.Parse(context.Background(), src, false)
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := tswasm.Parse(context.Background(), src, false)
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
				if _, err := tswasm.Parse(context.Background(), src, true); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func all(n *tswasm.Node) []*tswasm.Node {
	nodes := []*tswasm.Node{n}
	for _, c := range n.Children {
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
