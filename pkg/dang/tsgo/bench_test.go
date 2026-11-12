//go:build cgo

package tsgo_test

import (
	"testing"

	gts "github.com/odvcencio/gotreesitter"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/danglang"
	"github.com/vito/dang/v2/pkg/dang/tsgo"
)

// BenchmarkParseCorpus parses every input of TestParityWithC once per
// iteration: with the C runtime, with gotreesitter, and with the PEG parser
// that Dang actually runs (for scale; it fails on the malformed inputs).
func BenchmarkParseCorpus(b *testing.B) {
	inputs := parityInputs(b)
	var bytes int64
	for _, in := range inputs {
		bytes += int64(len(in.src))
	}

	b.Run("C", func(b *testing.B) {
		p := tree_sitter.NewParser()
		defer p.Close()
		if err := p.SetLanguage(danglang.Language()); err != nil {
			b.Fatal(err)
		}
		b.SetBytes(bytes)
		b.ReportAllocs()
		for b.Loop() {
			for _, in := range inputs {
				p.Parse(in.src, nil).Close()
			}
		}
	})

	b.Run("gotreesitter", func(b *testing.B) {
		lang := tsgo.Language()
		p := gts.NewParser(lang)
		b.SetBytes(bytes)
		b.ReportAllocs()
		for b.Loop() {
			for _, in := range inputs {
				tree, err := p.Parse(in.src)
				if err != nil {
					b.Fatal(err)
				}
				tree.Release()
			}
		}
	})

	b.Run("PEG", func(b *testing.B) {
		b.SetBytes(bytes)
		b.ReportAllocs()
		for b.Loop() {
			for _, in := range inputs {
				_, _ = dang.Parse(in.name, in.src)
			}
		}
	})
}

// BenchmarkLoadLanguage measures decoding the embedded gotreesitter tables,
// paid once per process on the first parse error.
func BenchmarkLoadLanguage(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := tsgo.Load(); err != nil {
			b.Fatal(err)
		}
	}
}
