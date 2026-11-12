//go:build cgo

package tsgo_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gts "github.com/odvcencio/gotreesitter"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/vito/dang/v2/pkg/dang/danglang"
	"github.com/vito/dang/v2/pkg/dang/tsgo"
)

// TestParityWithC parses every .dang file in the repository, and every
// tree-sitter corpus input, with both the C runtime (danglang) and
// gotreesitter (tsgo), and requires the same concrete syntax tree for every
// input C parses without errors: every node, named or not, with its kind,
// span, and ERROR/MISSING flags. Recovered trees of inputs with syntax
// errors (the tests/errors fixtures, among others) are only logged where
// they differ: see tests/errors_test.go's gotreesitter goldens for what the
// messages make of them.
func TestParityWithC(t *testing.T) {
	inputs := parityInputs(t)
	if len(inputs) == 0 {
		t.Fatal("no inputs")
	}

	cParser := tree_sitter.NewParser()
	defer cParser.Close()
	if err := cParser.SetLanguage(danglang.Language()); err != nil {
		t.Fatal(err)
	}
	lang := tsgo.Language()
	goParser := gts.NewParser(lang)

	var same, differ, withErrors, differWithErrors int
	for _, in := range inputs {
		cTree := cParser.Parse(in.src, nil)
		var want strings.Builder
		dumpC(&want, cTree.RootNode(), 0)
		cHasError := cTree.RootNode().HasError()
		if cHasError {
			withErrors++
		}
		cTree.Close()

		goTree, err := goParser.Parse(in.src)
		if err != nil {
			t.Errorf("%s: gotreesitter: %v", in.name, err)
			differ++
			continue
		}
		var got strings.Builder
		dumpGo(&got, goTree.RootNode(), lang, 0)
		goTree.Release()

		if want.String() == got.String() {
			same++
			continue
		}
		if cHasError {
			// Error recovery is allowed to diverge (see above).
			differWithErrors++
			t.Logf("%s: recovered trees differ\n%s", in.name, firstDifference(want.String(), got.String()))
			continue
		}
		differ++
		t.Errorf("%s: trees differ\n%s", in.name, firstDifference(want.String(), got.String()))
	}
	t.Logf("%d of %d inputs parse identically; %d error-free inputs differ; %d of %d inputs with syntax errors recover differently",
		same, len(inputs), differ, differWithErrors, withErrors)
}

type parityInput struct {
	name string
	src  []byte
}

func parityInputs(t testing.TB) []parityInput {
	root := filepath.Join("..", "..", "..")
	var inputs []parityInput
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
			inputs = append(inputs, parityInput{rel, src})
		case strings.HasPrefix(filepath.ToSlash(rel), "treesitter/test/corpus/"):
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for name, in := range corpusInputs(string(src)) {
				inputs = append(inputs, parityInput{rel + ": " + name, []byte(in)})
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
	for i := 0; i < len(lines); i++ {
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

func dumpC(w *strings.Builder, n *tree_sitter.Node, depth int) {
	sp, ep := n.StartPosition(), n.EndPosition()
	fmt.Fprintf(w, "%s%q [%d:%d-%d:%d] %d-%d%s\n", strings.Repeat("  ", depth), n.Kind(),
		sp.Row, sp.Column, ep.Row, ep.Column, n.StartByte(), n.EndByte(),
		flags(n.IsError(), n.IsMissing(), n.IsNamed(), n.IsExtra()))
	for i := uint(0); i < n.ChildCount(); i++ {
		dumpC(w, n.Child(i), depth+1)
	}
}

func dumpGo(w *strings.Builder, n *gts.Node, lang *gts.Language, depth int) {
	sp, ep := n.StartPoint(), n.EndPoint()
	fmt.Fprintf(w, "%s%q [%d:%d-%d:%d] %d-%d%s\n", strings.Repeat("  ", depth), n.Type(lang),
		sp.Row, sp.Column, ep.Row, ep.Column, n.StartByte(), n.EndByte(),
		flags(n.IsError(), n.IsMissing(), n.IsNamed(), n.IsExtra()))
	for i := 0; i < n.ChildCount(); i++ {
		dumpGo(w, n.Child(i), lang, depth+1)
	}
}

func flags(isError, missing, named, extra bool) string {
	var s string
	if isError {
		s += " ERROR"
	}
	if missing {
		s += " MISSING"
	}
	if named {
		s += " named"
	}
	if extra {
		s += " extra"
	}
	return s
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
			from := max(0, i-3)
			return fmt.Sprintf("line %d:\n  C:  %s\n  Go: %s\ncontext (C):\n%s", i+1, w, g,
				strings.Join(wl[from:min(len(wl), i+3)], "\n"))
		}
	}
	return "(no difference)"
}
