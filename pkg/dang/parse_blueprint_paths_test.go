package dang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseBlueprintAcrossPaths(t *testing.T) {
	cache := newSyntaxCache(16, 1<<20)
	firstPath := filepath.Join(t.TempDir(), "main.dang")
	// A literal that happens to contain an old path must remain unchanged.
	source := []byte(fmt.Sprintf("value: String! = %q\n", firstPath))
	for i := range 4 {
		filename := firstPath
		if i != 0 {
			filename = filepath.Join(t.TempDir(), "other.dang")
		}
		if err := os.WriteFile(filename, source, 0600); err != nil {
			t.Fatal(err)
		}
		got, err := parseFileBlueprintWithCache(filename, CurrentVersion, cache)
		if err != nil {
			t.Fatal(err)
		}
		want, err := ParseWithRecovery(filename, source, GlobalStore("filePath", filename))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("syntax or source locations changed at %s", filename)
		}
		got.(*FileBlock).Loc.Filename = "mutated by caller"
	}
	if got := cache.entries.Len(); got != 1 {
		t.Fatalf("identical source retained %d entries across paths, want 1", got)
	}
}

func TestParseBlueprintRelativeImportsAcrossPaths(t *testing.T) {
	for i := range 2 {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "helpers"), 0700); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			"main.dang":         "import Helpers\nresult: Int! = Helpers.value\n",
			"dang.toml":         "[imports.Helpers]\npath = \"./helpers\"\n",
			"helpers/main.dang": fmt.Sprintf("pub value: Int! = %d\n", i+1),
		}
		for filename, source := range files {
			if err := os.WriteFile(filepath.Join(dir, filename), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
		}
		env, err := RunDir(WithSyntaxCache(context.Background(), parseBlueprints), dir, false)
		if err != nil {
			t.Fatal(err)
		}
		value, found, err := env.Lookup(context.Background(), "result")
		if err != nil || !found || value.(IntValue).Val != i+1 {
			t.Fatalf("relative import at %s: got %v, found %t, error %v", dir, value, found, err)
		}
	}
}

func TestParseBlueprintDiagnosticAcrossPaths(t *testing.T) {
	cache := NewSyntaxCache()
	ctx := WithSyntaxCache(t.Context(), cache)
	for range 2 {
		dir := t.TempDir()
		filename := filepath.Join(dir, "main.dang")
		if err := os.WriteFile(filename, []byte("answer: Int! = \"wrong\"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := RunDir(ctx, dir, false)
		var sourceError *SourceError
		if !errors.As(err, &sourceError) || sourceError.Location == nil || sourceError.Location.Filename != filename {
			t.Fatalf("diagnostic should name %s, got %v", filename, err)
		}
		if err := os.Remove(filename); err != nil {
			t.Fatal(err)
		}
	}
	if got := cache.entries.Len(); got != 1 {
		t.Fatalf("identical source retained %d entries, want 1", got)
	}
}
