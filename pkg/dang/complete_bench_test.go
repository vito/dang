//go:build !js && !dang_nowasm

package dang

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// benchSource returns about size bytes of Dang, whole test files end to
// end, with an unfinished call typed at a top-level line near the middle,
// and the cursor's line and column at the end of it: what completion sees
// as someone types.
func benchSource(tb testing.TB, size int, typed string) (string, int, int) {
	files, err := filepath.Glob(filepath.Join("..", "..", "tests", "test_*.dang"))
	if err != nil {
		tb.Fatal(err)
	}
	slices.Sort(files)
	var src []byte
	// Whole files, as many as fit, going round again for big sizes.
	for added := true; len(src) < size && added; {
		added = false
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				tb.Fatal(err)
			}
			if len(src)+len(data) > size*3/2 {
				continue
			}
			src = append(src, data...)
			src = append(src, '\n')
			added = true
			if len(src) >= size {
				break
			}
		}
	}
	at := bytes.Index(src[len(src)/2:], []byte("\nlet "))
	if at < 0 {
		at = len(src) - 1
	} else {
		at += len(src) / 2
	}
	at++ // past the newline
	text := string(src[:at]) + typed + "\n" + string(src[at:])
	line := strings.Count(text[:at], "\n")
	return text, line, len(typed)
}

var benchSizes = []struct {
	name string
	size int
}{{"1KB", 1 << 10}, {"20KB", 20 << 10}, {"100KB", 100 << 10}, {"400KB", 400 << 10}}

// BenchmarkCompletionContextAt classifies the cursor as someone types one
// more letter of an argument name, in sources of growing size, reparsing
// incrementally or not.
func BenchmarkCompletionContextAt(b *testing.B) {
	for _, incremental := range []bool{false, true} {
		for _, s := range benchSizes {
			name := s.name + "/fresh"
			if incremental {
				name = s.name + "/incremental"
			}
			b.Run(name, func(b *testing.B) {
				defer func(was bool) { incrementalParsing = was }(incrementalParsing)
				incrementalParsing = incremental
				texts := [2]string{}
				var line, col [2]int
				texts[0], line[0], col[0] = benchSource(b, s.size, "let zz = foo.bar(baz: 1, qu")
				texts[1], line[1], col[1] = benchSource(b, s.size, "let zz = foo.bar(baz: 1, qux")
				for i := 0; b.Loop(); i++ {
					cc, _ := CompletionContextAt(texts[i%2], line[i%2], col[i%2])
					if cc == nil || cc.Kind != ContextArgument {
						b.Fatalf("got %+v", cc)
					}
				}
			})
		}
	}
}

// BenchmarkHighlight highlights one REPL line as it is typed, and whole
// sources.
func BenchmarkHighlight(b *testing.B) {
	b.Run("line", func(b *testing.B) {
		line := `let ctr = container.from("alpine").withExec(["echo", "${name}"]).stdout`
		for i := 0; b.Loop(); i++ {
			Highlight(line[:len(line)-i%2])
		}
	})
	for _, s := range benchSizes[:3] {
		b.Run(s.name, func(b *testing.B) {
			defer func(was bool) { incrementalParsing = was }(incrementalParsing)
			incrementalParsing = false
			text, _, _ := benchSource(b, s.size, "")
			for b.Loop() {
				Highlight(text)
			}
		})
	}
}
