//go:build cgo

package danglang_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/vito/dang/docs/go/danglang"
	"github.com/vito/dang/v2/pkg/dang/tswasm"
)

// The docs site highlights with this binding, and Dang runs the same grammar
// and C runtime compiled to WebAssembly (pkg/dang/tswasm). These tests hold
// them to the same trees and highlights.

// TestParityWithCgo parses every .dang file in the repository, and every
// tree-sitter corpus input, with the cgo runtime (danglang) and the wasm one,
// and requires identical trees: every node, named or not, with its kind,
// field, span and flags. Malformed inputs (tests/errors, among others) are
// included, so error recovery must match too.
func TestParityWithCgo(t *testing.T) {
	inputs := corpus(t)
	if len(inputs) == 0 {
		t.Fatal("no inputs")
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(danglang.Language()); err != nil {
		t.Fatal(err)
	}

	var withErrors int
	for _, in := range inputs {
		tree := parser.Parse(in.src, nil)
		var want strings.Builder
		dumpC(&want, tree.RootNode(), "", 0)
		if tree.RootNode().HasError() {
			withErrors++
		}
		tree.Close()

		root, err := tswasm.Parse(context.Background(), in.src, tswasm.PruneNone)
		if err != nil {
			t.Errorf("%s: %v", in.name, err)
			continue
		}
		var got strings.Builder
		dumpWasm(&got, root, 0)

		if want.String() != got.String() {
			t.Errorf("%s: trees differ\n%s", in.name, firstDifference(want.String(), got.String()))
		}
	}
	t.Logf("compared %d inputs, %d of them with syntax errors", len(inputs), withErrors)
}

// TestQueryParityWithCgo runs the highlight query over every input with
// both runtimes, and requires the same captures, predicates applied.
func TestQueryParityWithCgo(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "pkg", "dang", "highlights.scm"))
	if err != nil {
		t.Fatal(err)
	}
	cq, qerr := tree_sitter.NewQuery(danglang.Language(), string(src))
	if qerr != nil {
		t.Fatal(qerr)
	}
	defer cq.Close()
	wq, err := tswasm.NewQuery(string(src))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(cq.CaptureNames()) != fmt.Sprint(wq.CaptureNames()) {
		t.Fatalf("capture names differ:\n  cgo:  %v\n  wasm: %v", cq.CaptureNames(), wq.CaptureNames())
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(danglang.Language()); err != nil {
		t.Fatal(err)
	}
	var total int
	for _, in := range corpus(t) {
		tree := parser.Parse(in.src, nil)
		cursor := tree_sitter.NewQueryCursor()
		var want strings.Builder
		captures := cursor.Captures(cq, tree.RootNode(), in.src)
		for {
			m, idx := captures.Next()
			if m == nil {
				break
			}
			c := m.Captures[idx]
			fmt.Fprintf(&want, "%d %d-%d\n", c.Index, c.Node.StartByte(), c.Node.EndByte())
			total++
		}
		cursor.Close()
		tree.Close()

		got, err := wq.Captures(context.Background(), in.src)
		if err != nil {
			t.Errorf("%s: %v", in.name, err)
			continue
		}
		var gotS strings.Builder
		for _, c := range got {
			fmt.Fprintf(&gotS, "%d %d-%d\n", c.Index, c.StartByte, c.EndByte)
		}
		if want.String() != gotS.String() {
			t.Errorf("%s: captures differ\n%s", in.name, firstDifference(want.String(), gotS.String()))
		}
	}
	t.Logf("compared %d captures", total)
}

type input struct {
	name string
	src  []byte
}

// corpus returns every .dang file in the repository and every input of the
// tree-sitter test corpus.
func corpus(t testing.TB) []input {
	root := filepath.Join("..", "..", "..")
	var inputs []input
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "mnt", "grammars":
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		switch {
		case strings.HasSuffix(path, ".dang"):
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			inputs = append(inputs, input{rel, src})
		case strings.HasPrefix(filepath.ToSlash(rel), "treesitter/test/corpus/"):
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for name, in := range corpusInputs(string(src)) {
				inputs = append(inputs, input{rel + ": " + name, []byte(in)})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return inputs
}

// corpusInputs splits a tree-sitter test corpus file into its inputs, keyed
// by test name.
func corpusInputs(corpus string) map[string]string {
	inputs := map[string]string{}
	lines := strings.SplitAfter(corpus, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "===") {
			continue
		}
		name := strings.TrimSpace(lines[i+1])
		i += 3 // header, name, header
		var in strings.Builder
		for ; i < len(lines) && !strings.HasPrefix(lines[i], "---"); i++ {
			in.WriteString(lines[i])
		}
		inputs[name] = strings.TrimSuffix(in.String(), "\n\n")
	}
	return inputs
}

func dumpC(w *strings.Builder, n *tree_sitter.Node, field string, depth int) {
	sp, ep := n.StartPosition(), n.EndPosition()
	line(w, depth, n.Kind(), field, uint32(sp.Row), uint32(sp.Column), uint32(ep.Row), uint32(ep.Column),
		uint32(n.StartByte()), uint32(n.EndByte()), n.IsError(), n.IsMissing(), n.HasError(), n.IsNamed(), n.IsExtra())
	for i := uint(0); i < n.ChildCount(); i++ {
		dumpC(w, n.Child(i), n.FieldNameForChild(uint32(i)), depth+1)
	}
}

func dumpWasm(w *strings.Builder, n *tswasm.Node, depth int) {
	line(w, depth, n.Kind, n.Field, n.StartRow, n.StartColumn, n.EndRow, n.EndColumn,
		n.StartByte, n.EndByte, n.Error, n.Missing, n.HasError, n.Named, n.Extra)
	for _, c := range n.Children() {
		dumpWasm(w, c, depth+1)
	}
}

func line(w *strings.Builder, depth int, kind, field string, sr, sc, er, ec, sb, eb uint32, isError, missing, hasError, named, extra bool) {
	fmt.Fprintf(w, "%s%s%q [%d:%d-%d:%d] %d-%d error=%t missing=%t has_error=%t named=%t extra=%t\n",
		strings.Repeat("  ", depth), field+":", kind, sr, sc, er, ec, sb, eb, isError, missing, hasError, named, extra)
}

func firstDifference(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(wl), len(gl)); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("line %d:\n  cgo:  %s\n  wasm: %s", i+1, w, g)
		}
	}
	return "(no difference)"
}
