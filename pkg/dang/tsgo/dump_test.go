//go:build cgo

package tsgo_test

import (
	"os"
	"strings"
	"testing"

	gts "github.com/odvcencio/gotreesitter"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/vito/dang/v2/pkg/dang/danglang"
	"github.com/vito/dang/v2/pkg/dang/tsgo"
)

// TestDump prints both runtimes' trees for the file named by $TSGO_DUMP,
// for debugging parity failures.
func TestDump(t *testing.T) {
	path := os.Getenv("TSGO_DUMP")
	if path == "" {
		t.Skip("TSGO_DUMP not set")
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cParser := tree_sitter.NewParser()
	defer cParser.Close()
	if err := cParser.SetLanguage(danglang.Language()); err != nil {
		t.Fatal(err)
	}
	cTree := cParser.Parse(src, nil)
	var c strings.Builder
	dumpC(&c, cTree.RootNode(), 0)
	lang := tsgo.Language()
	goTree, err := gts.NewParser(lang).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	var g strings.Builder
	dumpGo(&g, goTree.RootNode(), lang, 0)
	if dir := os.Getenv("TSGO_DUMP_OUT"); dir != "" {
		_ = os.WriteFile(dir+"/c.txt", []byte(c.String()), 0o644)
		_ = os.WriteFile(dir+"/go.txt", []byte(g.String()), 0o644)
		return
	}
	t.Logf("source:\n%s\nC:\n%s\nGo:\n%s", src, c.String(), g.String())
}
