package dang

import (
	"os"
	"path/filepath"
	"testing"
)

// Set DANG_BENCH_SOURCE to measure a module source instead of the corpus fixture.
func BenchmarkSyntaxCache(b *testing.B) {
	filename := os.Getenv("DANG_BENCH_SOURCE")
	if filename == "" {
		filename = filepath.Join("..", "..", "tests", "test_comprehensive.dang")
	}
	cache := NewSyntaxCache()
	if _, err := parseFileBlueprintWithCache(filename, CurrentVersion, cache); err != nil {
		b.Fatal(err)
	}
	if cache.entries.Len() != 1 {
		b.Fatal("benchmark source cannot be cached")
	}

	for _, scenario := range []struct {
		name  string
		parse func(string) (any, error)
	}{
		{"Parse", func(filename string) (any, error) {
			return ParseFileWithRecovery(filename, GlobalStore("filePath", filename))
		}},
		{"Prepare", func(filename string) (any, error) {
			return parseFileBlueprintWithCache(filename, CurrentVersion, NewSyntaxCache())
		}},
		{"Reuse", func(filename string) (any, error) {
			return parseFileBlueprintWithCache(filename, CurrentVersion, cache)
		}},
	} {
		b.Run(scenario.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := scenario.parse(filename); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
