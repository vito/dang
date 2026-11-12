package analysis_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

func membersNamed(ns ...string) []analysis.Member {
	var ms []analysis.Member
	for _, n := range ns {
		ms = append(ms, analysis.Member{Name: n, Kind: analysis.KindFunction})
	}
	return ms
}

func TestNearest(t *testing.T) {
	listMethods := membersNamed("all", "any", "dropFirst", "dropLast", "filter", "find", "length", "map", "takeFirst", "takeLast", "toLower")
	for _, tc := range []struct {
		name  string
		limit int
		want  []string
	}{
		// A transposition is one edit.
		{"lenght", 3, []string{"length"}},
		// Case alone is closest.
		{"TOLOWER", 3, []string{"toLower"}},
		// A prefix is a strong match, which outranks sharing a word
		// (takeFirst, dropFirst).
		{"findFirst", 3, []string{"find"}},
		{"len", 3, []string{"length"}},
		// Edits first, closest first, then prefixes.
		{"fiter", 3, []string{"filter"}},
		{"mapp", 3, []string{"map"}},
		// Short names allow fewer edits.
		{"fnd", 3, []string{"find"}},
		{"ay", 3, nil},
		// Sharing a camelCase word is the fallback, fewer edits first.
		{"lastItem", 3, []string{"takeLast", "dropLast"}},
		{"lastItem", 1, []string{"takeLast"}},
		// Short words are not shared words.
		{"toUpper", 3, nil},
		{"xyzzy", 3, nil},
		// The name itself isn't near itself.
		{"find", 3, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, names(analysis.Nearest(tc.name, listMethods, tc.limit)))
		})
	}
}

func TestCamelWords(t *testing.T) {
	for in, want := range map[string][]string{
		"sortStrings":  {"sort", "strings"},
		"toJSON":       {"to", "json"},
		"HTMLEscape":   {"html", "escape"},
		"escape_html":  {"escape", "html"},
		"base64Encode": {"base", "64", "encode"},
		"find":         {"find"},
		"":             nil,
	} {
		require.Equal(t, want, analysis.CamelWords(in), in)
	}
}

func TestLevenshtein(t *testing.T) {
	require.Equal(t, 0, analysis.Levenshtein("find", "find"))
	require.Equal(t, 1, analysis.Levenshtein("lenght", "length"))
	require.Equal(t, 3, analysis.Levenshtein("kitten", "sitting"))
	require.Equal(t, 3, analysis.Levenshtein("", "abc"))
	require.Equal(t, 3, analysis.Levenshtein("abc", ""))
}
