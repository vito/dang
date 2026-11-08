package dang

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A record literal's fields are inferred in dependency order, but its type
// lists them in the order they're written.
func TestRecordLiteralKeepsWrittenFieldOrder(t *testing.T) {
	env := runDangSnippet(t, `pub r = {{b: a + 1, c: b, a: 1}}`)
	val, found, err := env.Lookup(context.Background(), "r")
	require.NoError(t, err)
	require.True(t, found)
	obj, ok := val.(*Object)
	require.True(t, ok, "got %T", val)
	mod, ok := obj.Mod.(*Type)
	require.True(t, ok, "got %T", obj.Mod)

	var names []string
	for name := range mod.Bindings(PrivateVisibility) {
		names = append(names, name)
	}
	require.Equal(t, []string{"b", "c", "a"}, names)
}
