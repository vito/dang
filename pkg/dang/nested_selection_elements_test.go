package dang

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNestedSelectionPreservesElementNullability(t *testing.T) {
	for _, test := range []struct {
		name    string
		element string
		text    string
		valid   bool
	}{
		{"non-null elements and fields", "Row!", "String!", true},
		{"nullable elements", "Row", "String!", false},
		{"nullable fields", "Row!", "String", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := fmt.Sprintf(`
type Row { text: %s }
type Page { matches: [%s]! }
let pages = [Page(matches: [Row(text: "first"), Row(text: "last")]), Page(matches: [])]
let selected = pages.{{matches.{{text}}}}
let empty: [String!]! = []
let values: [String!]! = selected.reduce(empty) { all, page =>
  all + page.matches.map { row => row.text }
}
`, test.text, test.element)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.dang"), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			env, err := RunDir(t.Context(), dir, false)
			if !test.valid {
				if err == nil || !strings.Contains(err.Error(), "cannot use [String]! as [String!]!") {
					t.Fatalf("expected nullable values to be rejected, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			value, found, err := env.Lookup(t.Context(), "values")
			if err != nil || !found {
				t.Fatalf("lookup values: found=%t, error=%v", found, err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != `["first","last"]` {
				t.Fatalf("nested selection changed values or order: %s", encoded)
			}
		})
	}
}
