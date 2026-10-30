package dang

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/v2/pkg/hm"
)

var parseBlueprints = NewSyntaxCache()

func parseFileBlueprint(filename string) (any, error) {
	return parseFileBlueprintWithCache(filename, CurrentVersion, parseBlueprints)
}

func TestParseBlueprintFreshSyntax(t *testing.T) {
	file := filepath.Join(t.TempDir(), "main.dang")
	source := []byte("import Dagger\ntype Thing { value: String! = \"one\" }\n")
	if err := os.WriteFile(file, source, 0o600); err != nil {
		t.Fatal(err)
	}
	firstValue, err := parseFileBlueprint(file)
	if err != nil {
		t.Fatal(err)
	}
	first := firstValue.(*FileBlock)
	if _, ok := parseBlueprints.get(parseBlueprintKey{digest: sha256.Sum256(source), version: CurrentVersion}); !ok {
		t.Fatal("source was not cached")
	}
	first.Forms[0].(*ImportDecl).Name.Name = "Other"
	first.TypeScope = NewPreludeTypeScope("")
	first.SetInferredType(hm.TypeVariable('x'))
	first.Forms[1].(*ObjectDecl).Value.Forms = nil
	secondValue, err := parseFileBlueprint(file)
	if err != nil {
		t.Fatal(err)
	}
	second := secondValue.(*FileBlock)
	parsed, err := ParseWithRecovery(file, source, GlobalStore("filePath", file))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, parsed) {
		t.Fatal("cached syntax changed after mutating a previous result")
	}
	if second == first || second.Forms[0] == first.Forms[0] {
		t.Fatal("calls share syntax pointers")
	}
}

func TestBlueprintCacheBudget(t *testing.T) {
	cache := newSyntaxCache(4, 10)
	create := parseBlueprint{syntax: reflect.ValueOf(1)}
	one, two, three := parseBlueprintKey{digest: [32]byte{1}}, parseBlueprintKey{digest: [32]byte{2}}, parseBlueprintKey{digest: [32]byte{3}}
	cache.add(one, create, 4)
	cache.add(two, create, 4)
	if _, ok := cache.get(one); !ok {
		t.Fatal("first entry missing")
	}
	cache.add(three, create, 4)
	if _, ok := cache.get(two); ok {
		t.Fatal("least recently used entry survives budget eviction")
	}
	if cache.weight != 8 {
		t.Fatalf("wrong retained weight: %d", cache.weight)
	}
	cache.add(one, create, 4)
	if cache.weight != 8 {
		t.Fatal("duplicate miss counted twice")
	}
	cache.add(parseBlueprintKey{digest: [32]byte{4}}, create, 11)
	if cache.weight != 8 {
		t.Fatal("oversized entry changed the cache")
	}
	// The count limit also subtracts the evicted entry from the weight.
	limited := newSyntaxCache(1, 10)
	limited.add(one, create, 4)
	limited.add(two, create, 3)
	if limited.weight != 3 {
		t.Fatalf("count eviction retained wrong weight: %d", limited.weight)
	}
}

func TestParseBlueprintEvictionAndOversize(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "one.dang"), filepath.Join(dir, "two.dang")
	for i, file := range []string{first, second} {
		if err := os.WriteFile(file, []byte(fmt.Sprintf("value: Int! = %d\n", i+1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cache := newSyntaxCache(1, 1<<20)
	got, err := parseFileBlueprintWithCache(first, CurrentVersion, cache)
	if err != nil {
		t.Fatal(err)
	}
	got.(*FileBlock).Forms = nil
	if _, err := parseFileBlueprintWithCache(second, CurrentVersion, cache); err != nil {
		t.Fatal(err)
	}
	key := parseBlueprintKey{digest: sha256.Sum256([]byte("value: Int! = 1\n")), version: CurrentVersion}
	if _, ok := cache.get(key); ok {
		t.Fatal("first file was not evicted")
	}
	reparsed, err := parseFileBlueprintWithCache(first, CurrentVersion, cache)
	if err != nil {
		t.Fatal(err)
	}
	want, err := ParseFileWithRecovery(first, GlobalStore("filePath", first))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reparsed, want) {
		t.Fatal("eviction changed the parsed result")
	}
	// A file whose instructions exceed the budget remains valid source.
	tiny := newSyntaxCache(1, 1)
	oversized, err := parseFileBlueprintWithCache(first, CurrentVersion, tiny)
	if err != nil || !reflect.DeepEqual(oversized, want) {
		t.Fatalf("oversized source no longer parses: %v", err)
	}
	if tiny.weight != 0 {
		t.Fatal("oversized syntax was cached")
	}
}

func TestParseBlueprintSourceChanges(t *testing.T) {
	file := filepath.Join(t.TempDir(), "main.dang")
	for _, source := range []string{"value: Int! = 1\n", "value: Int! = 2\n", "value: Int! =\n", "value: Int! = 1\n"} {
		if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := parseFileBlueprint(file)
		want, wantErr := ParseWithRecovery(file, []byte(source), GlobalStore("filePath", file))
		if (err == nil) != (wantErr == nil) {
			t.Fatalf("wrong error for %q: got %v, want %v", source, err, wantErr)
		}
		if err != nil {
			if err.Error() != wantErr.Error() {
				t.Fatalf("changed diagnostic: got %v, want %v", err, wantErr)
			}
		} else if !reflect.DeepEqual(got, want) {
			t.Fatalf("wrong syntax for source %q", source)
		}
	}
}

func TestParseBlueprintConcurrentEvaluation(t *testing.T) {
	cache := NewSyntaxCache()
	ctx := WithSyntaxCache(context.Background(), cache)
	dir := t.TempDir()
	file := filepath.Join(dir, "main.dang")
	if err := os.WriteFile(file, []byte("type Box { value: Int! = 0; add: Box! { self.value = value + 1; self } }\nresult: Int! = Box().add.value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				env, err := RunDir(ctx, dir, false)
				if err != nil {
					t.Error(err)
					return
				}
				value, found, err := env.Lookup(context.Background(), "result")
				if err != nil || !found || value.(IntValue).Val != 1 {
					t.Errorf("evaluation state leaked: %v, %v, %v", value, found, err)
				}
			}()
		}
		wg.Wait()
	}
	if cache.entries.Len() == 0 {
		t.Fatal("RunDir did not populate its supplied syntax cache")
	}
}

type blueprintClient string

func (c blueprintClient) MakeRequest(_ context.Context, _ *graphql.Request, response *graphql.Response) error {
	data, err := json.Marshal(map[string]string{"value": string(c)})
	if err != nil {
		return err
	}
	return json.Unmarshal(data, response.Data)
}

type blueprintCountingClient struct {
	graphql.Client
	calls atomic.Int64
}

func (c *blueprintCountingClient) MakeRequest(ctx context.Context, request *graphql.Request, response *graphql.Response) error {
	c.calls.Add(1)
	return c.Client.MakeRequest(ctx, request, response)
}

func TestParseBlueprintUsesCurrentImport(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.dang")
	if err := os.WriteFile(file, []byte("import Session\nresult: String! = Session.value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	schema, err := SchemaFromSDL("type Query { value: String! }", "session.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first-session", "second-session"} {
		ctx := ContextWithImportConfigs(WithSyntaxCache(context.Background(), parseBlueprints), ImportConfig{Name: "Session", Client: blueprintClient(name), Schema: schema})
		env, err := RunDir(ctx, dir, false)
		if err != nil {
			t.Fatal(err)
		}
		got, found, err := env.Lookup(ctx, "result")
		if err != nil || !found || got.(StringValue).Val != name {
			t.Fatalf("old session client reused: %v, %v", got, err)
		}
	}
	// An incompatible schema on the same source must be checked again.
	changed, err := SchemaFromSDL("type Query { value: Int! }", "session.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	client := &blueprintCountingClient{Client: blueprintClient("third-session")}
	ctx := ContextWithImportConfigs(WithSyntaxCache(context.Background(), parseBlueprints), ImportConfig{Name: "Session", Client: client, Schema: changed})
	if _, err := RunDir(ctx, dir, false); err == nil {
		t.Fatal("cached syntax bypassed changed-schema validation")
	}
	if client.calls.Load() != 0 {
		t.Fatal("incompatible schema reached evaluation before type checking failed")
	}
}

func TestParseBlueprintCorpus(t *testing.T) {
	files, err := filepath.Glob("../../tests/*.dang")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("language corpus is empty")
	}
	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseWithRecovery(file, source, GlobalStore("filePath", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := syntaxBlueprint(reflect.ValueOf(parsed)); err != nil {
			t.Fatalf("unsupported syntax in corpus %s: %v", file, err)
		}
		for range 2 {
			got, err := parseFileBlueprint(file)
			if err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			// Functions installed by the parser are immutable; DeepEqual
			// deliberately treats them as unequal, so compare source format.
			if Format(got.(Node)) != Format(parsed.(Node)) {
				t.Fatalf("syntax changed for %s", file)
			}
		}
	}
}

func TestParseSourceFileThroughSyntaxCache(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.dang")
	source := []byte("type Thing { value: String! = \"one\" }\n")
	if err := os.WriteFile(file, source, 0o600); err != nil {
		t.Fatal(err)
	}
	want, err := ParseWithRecovery(file, source, GlobalStore("filePath", file))
	if err != nil {
		t.Fatal(err)
	}
	cache := NewSyntaxCache()
	ctx := WithSyntaxCache(context.Background(), cache)
	first, err := ParseSourceFile(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, want) {
		t.Fatal("ParseSourceFile returned different syntax than parsing the file")
	}
	if _, ok := cache.get(parseBlueprintKey{digest: sha256.Sum256(source), version: CurrentVersion}); !ok {
		t.Fatal("ParseSourceFile did not fill the syntax cache")
	}
	first.Forms = nil
	second, err := ParseSourceFile(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, want) {
		t.Fatal("cached syntax changed after mutating a previous ParseSourceFile result")
	}
	env, err := RunDir(ctx, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !env.Has("Thing") {
		t.Fatal("RunDir of the cached source lost a declaration")
	}
	if _, err := ParseSourceFile(context.Background(), file); err != nil {
		t.Fatalf("ParseSourceFile without a cache: %v", err)
	}
}
