package dang

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The formatter rewrites `\/` to `/`, so these escapes are checked here rather
// than in a tests/*.dang file, which the fmt check would normalize.
func TestStringSlashEscape(t *testing.T) {
	for src, want := range map[string]string{
		`"a\/b"`:  `a/b`,
		`"\/\/"`:  `//`,
		`"a\\/b"`: `a\/b`,
		`"\\\/"`:  `\/`,
	} {
		parsed, err := Parse("escape", []byte(src))
		require.NoError(t, err, src)
		str, ok := parsed.(*FileBlock).Forms[0].(*String)
		require.True(t, ok, src)
		require.Equal(t, want, str.Value, src)
	}
}
