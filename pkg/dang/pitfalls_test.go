package dang

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// docsLit is the source of the docs site the pitfalls link to.
var docsLit = filepath.Join("..", "..", "docs", "lit")

// TestPitfallsAreDocumented checks that each pitfall is well-formed and that
// its docs link resolves: to a heading tagged {#tag} on the page tagged
// {#page} in docs/lit, as the site serves https://danglang.org/<page>#<tag>.
// Its detection is tested in pkg/dang/analysis (TestPitfalls).
func TestPitfallsAreDocumented(t *testing.T) {
	pages := map[string]string{} // page tag -> its markdown
	err := filepath.WalkDir(docsLit, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if m := regexp.MustCompile(`(?m)^# .*\{#([^}]+)\}\s*$`).FindSubmatch(text); m != nil {
			pages[string(m[1])] = string(text)
		}
		return nil
	})
	require.NoError(t, err)

	ids := map[string]bool{}
	for _, p := range Pitfalls {
		t.Run(p.ID, func(t *testing.T) {
			require.Regexp(t, `^[a-z]+(-[a-z]+)*$`, p.ID)
			require.False(t, ids[p.ID], "duplicate ID")
			ids[p.ID] = true

			require.NotEmpty(t, p.Hint)
			require.NotContains(t, p.Hint, "\n", "a hint is one line")

			rest, ok := strings.CutPrefix(p.Docs, "https://danglang.org/")
			require.True(t, ok, "docs link %q is not on danglang.org", p.Docs)
			page, tag, ok := strings.Cut(rest, "#")
			require.True(t, ok, "docs link %q names no section", p.Docs)
			text, ok := pages[page]
			require.True(t, ok, "no page in docs/lit is tagged {#%s}", page)
			require.Regexp(t, `(?m)^#{2,6} .*\{#`+regexp.QuoteMeta(tag)+`\}\s*$`, text,
				"page {#%s} has no heading tagged {#%s}", page, tag)
		})
	}
}
