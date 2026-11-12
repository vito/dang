package dang

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vito/dang/v2/pkg/ioctx"
)

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, source := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600))
	}
	return dir
}

// A module with a private helper, a public type using it, a top-level
// initializer and a top-level statement, both of which print.
var scopeModule = map[string]string{
	"helpers.dang": `# The first paragraph of text.
let paragraph(text: String!): String! {
  let parts = text.split("\n\n")
  parts[0] ?? ""
}
`,
	"main.dang": `type Doc {
  pub body: String!
  pub summary: String! { paragraph(body) }
}

let noisy: Int! = {
  print("initialized")
  42
}

print("statement ran")
`,
}

func TestLoadDirDeclaresWithoutRunning(t *testing.T) {
	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)

	m, err := LoadDir(ctx, writeModule(t, scopeModule), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()
	require.Empty(t, out.String(), "declaring the module ran its top level")
	require.Contains(t, Describe(ctx, m.ValueEnv, nil, DescribeOptions{}), "noisy: <lazy>")

	// A snippet calls the module's private helper.
	val, typ, err := m.Eval(ctx, "", `paragraph("a\n\nb")`)
	require.NoError(t, err)
	require.Equal(t, "String!", typ.String())
	require.Equal(t, StringValue{Val: "a"}, val)

	// And uses its public type, whose method calls the helper.
	val, _, err = m.Eval(ctx, "", `Doc(body: "first\n\nsecond").summary`)
	require.NoError(t, err)
	require.Equal(t, StringValue{Val: "first"}, val)
	require.Empty(t, out.String())

	// Reading a top-level binding initializes it, once.
	val, _, err = m.Eval(ctx, "", "noisy + 1")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 43}, val)
	val, _, err = m.Eval(ctx, "", "noisy")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 42}, val)
	require.Equal(t, "initialized\n", out.String())
}

func TestLoadDirEager(t *testing.T) {
	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)

	m, err := LoadDir(ctx, writeModule(t, scopeModule), LoadOptions{Eager: true})
	require.NoError(t, err)
	defer m.Close()
	require.Equal(t, "initialized\nstatement ran\n", out.String())

	val, _, err := m.Eval(ctx, "", "noisy")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 42}, val)
	require.Equal(t, "initialized\nstatement ran\n", out.String())
}

func TestSnippetTypeErrorPosition(t *testing.T) {
	ctx := context.Background()
	m, err := LoadDir(ctx, writeModule(t, scopeModule), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()

	source := "let n = 1\nparagraph(n)\n"
	_, err = m.Check(ctx, "probe.dang", source)
	require.Error(t, err)
	var srcErr *SourceError
	require.ErrorAs(t, err, &srcErr)
	require.Equal(t, "probe.dang", srcErr.Location.Filename)
	require.Equal(t, 2, srcErr.Location.Line)
	require.Equal(t, source, srcErr.Source)
	require.Contains(t, err.Error(), "probe.dang:2:")
	require.NotContains(t, err.Error(), "failed to read file")

	// Unnamed snippets are <eval>.
	_, err = m.Check(ctx, "", "paragraph(1)")
	require.ErrorAs(t, err, &srcErr)
	require.Equal(t, defaultSnippetFilename, srcErr.Location.Filename)
	require.Equal(t, 1, srcErr.Location.Line)

	// Nothing ran: checking is pure.
	val, _, err := m.Eval(ctx, "", "noisy")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 42}, val)
}

func TestSnippetRuntimeErrorPosition(t *testing.T) {
	ctx := context.Background()
	m, err := LoadDir(ctx, writeModule(t, scopeModule), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()

	source := "let n = 1\nraise \"boom\"\n"
	_, _, err = m.Eval(ctx, "probe.dang", source)
	require.Error(t, err)
	require.Contains(t, err.Error(), "probe.dang:2:")
	require.Contains(t, err.Error(), `raise "boom"`)
}

func TestSessionAccumulatesAndLeavesModuleAlone(t *testing.T) {
	ctx := context.Background()
	m, err := LoadDir(ctx, writeModule(t, scopeModule), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()

	s, err := m.NewSession(ctx)
	require.NoError(t, err)
	run := func(source string) Value {
		t.Helper()
		snippet, err := s.Check(ctx, "", source)
		require.NoError(t, err)
		val, err := snippet.Eval(ctx)
		require.NoError(t, err)
		return val
	}

	require.Equal(t, NullValue{}, run("let doubled = noisy * 2"))
	require.Equal(t, IntValue{Val: 84}, run("doubled"))
	// A snippet can shadow a module helper, and rebind a module variable,
	// for the session only.
	run(`let paragraph(text: String!): String! { "shadowed" }`)
	require.Equal(t, StringValue{Val: "shadowed"}, run(`paragraph("a")`))
	run("noisy = 1")
	require.Equal(t, IntValue{Val: 1}, run("noisy"))

	val, _, err := m.Eval(ctx, "", `[toString(noisy), paragraph("a\n\nb")]`)
	require.NoError(t, err)
	require.Equal(t, `["42", "a"]`, Repr(val))

	snippet, err := s.Check(ctx, "", "1")
	require.NoError(t, err)
	_, err = snippet.Eval(ctx)
	require.NoError(t, err)
	_, err = snippet.Eval(ctx)
	require.ErrorContains(t, err, "already evaluated")
}

// A declaration may snapshot a binding statements write later, whether the
// declarations run before every statement (a module's top level) or in
// source order (a snippet), with the same values either way.
func TestSnapshotBeforeWritesInScriptsAndSessions(t *testing.T) {
	ctx := context.Background()
	src := "let x = 1\nlet before = x\nx = 2\n"

	// As a module's top level, run eagerly: declarations, then statements.
	script, err := LoadDir(ctx, writeModule(t, map[string]string{"main.dang": src}), LoadOptions{Eager: true})
	require.NoError(t, err)
	defer script.Close()
	val, _, err := script.Eval(ctx, "", "[before, x]")
	require.NoError(t, err)
	require.Equal(t, "[1, 2]", Repr(val))

	// As a snippet, in source order.
	m, err := LoadDir(ctx, writeModule(t, map[string]string{"ws.dang": "pub ws: Int! = 1\n"}), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()
	val, _, err = m.Eval(ctx, "", src+"[before, x]")
	require.NoError(t, err)
	require.Equal(t, "[1, 2]", Repr(val))

	// Snapshotting a module binding a snippet reassigns.
	val, _, err = m.Eval(ctx, "", "let before = ws\nws = ws + 1\nws = ws + 1\n[before, ws]")
	require.NoError(t, err)
	require.Equal(t, "[1, 3]", Repr(val))

	// A read after a write is still rejected, in either.
	_, err = m.Check(ctx, "", "let x = 1\nx = 2\nlet after = x\n")
	require.ErrorContains(t, err, "`after`'s initializer reads `x`, which a top-level statement writes (line 2)")
	_, err = LoadDir(ctx, writeModule(t, map[string]string{"main.dang": "let x = 1\nx = 2\nlet after = x\n"}), LoadOptions{})
	require.ErrorContains(t, err, "`after`'s initializer reads `x`, which a top-level statement writes (line 2)")
}

func TestSessionFailedSnippetsLeaveNothing(t *testing.T) {
	// Not auto-imported: snippets import it.
	ctx, _ := fakeGraphQLContext(t, false)
	m, err := LoadDir(ctx, writeModule(t, scopeModule), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()
	s, err := m.NewSession(ctx)
	require.NoError(t, err)

	// One that fails to check, imports and all.
	_, err = s.Check(ctx, "", "import Test\nlet leaked = 1\nlet bad: Int! = \"nope\"")
	require.Error(t, err)
	_, err = s.Check(ctx, "", "leaked")
	require.ErrorContains(t, err, "leaked")
	_, err = s.Check(ctx, "", "Test.engineVersion")
	require.ErrorContains(t, err, "Test")

	// An import that joins the session stays.
	snippet, err := s.Check(ctx, "", "import Test")
	require.NoError(t, err)
	_, err = snippet.Eval(ctx)
	require.NoError(t, err)
	val := func(source string) Value {
		t.Helper()
		snippet, err := s.Check(ctx, "", source)
		require.NoError(t, err)
		v, err := snippet.Eval(ctx)
		require.NoError(t, err)
		return v
	}
	require.Equal(t, StringValue{Val: "v1"}, val("Test.engineVersion"))

	// One that fails to evaluate, after assigning bindings of the module
	// and of an earlier snippet.
	val("let a = 1")
	snippet, err = s.Check(ctx, "", "let leaked = 1\na = 2\nnoisy = 7\nraise \"boom\"")
	require.NoError(t, err)
	_, err = snippet.Eval(ctx)
	require.ErrorContains(t, err, "boom")
	_, err = s.Check(ctx, "", "leaked")
	require.ErrorContains(t, err, "leaked")
	require.Equal(t, IntValue{Val: 1}, val("a"))
	require.Equal(t, IntValue{Val: 42}, val("noisy"))

	// One checked before another joined the session.
	first, err := s.Check(ctx, "", "let a = 1")
	require.NoError(t, err)
	second, err := s.Check(ctx, "", "let b = 2")
	require.NoError(t, err)
	_, err = first.Eval(ctx)
	require.NoError(t, err)
	_, err = second.Eval(ctx)
	require.ErrorContains(t, err, "check it again")
}

func TestLoadDirRetriesCancelledInitializer(t *testing.T) {
	ctx, client := fakeGraphQLContext(t, true)
	m, err := LoadDir(ctx, writeModule(t, map[string]string{
		"main.dang": "let version: String! = engineVersion\n",
	}), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()
	require.Zero(t, client.count())

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = m.Eval(cancelled, "", "version")
	require.ErrorIs(t, err, context.Canceled)

	val, _, err := m.Eval(ctx, "", "version")
	require.NoError(t, err)
	require.Equal(t, StringValue{Val: "v1"}, val)
	require.Equal(t, 2, client.count())
}

func TestSessionErrorInEarlierSnippet(t *testing.T) {
	ctx := context.Background()
	m, err := LoadDir(ctx, writeModule(t, scopeModule), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()
	s, err := m.NewSession(ctx)
	require.NoError(t, err)

	helper := "let helper(): Int! {\n  raise \"from helper\"\n}\n"
	snippet, err := s.Check(ctx, "", helper)
	require.NoError(t, err)
	_, err = snippet.Eval(ctx)
	require.NoError(t, err)

	snippet, err = s.Check(ctx, "", "let a = 1\nlet b = 2\nhelper()\n")
	require.NoError(t, err)
	_, err = snippet.Eval(ctx)
	require.Error(t, err)
	// The raise is in the first snippet, and shown from it.
	require.Contains(t, err.Error(), "<eval>:2:")
	require.Contains(t, err.Error(), `raise "from helper"`)
}

// blockingClient answers engineVersion, except that its first request
// blocks until its context is done.
type blockingClient struct {
	started chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (c *blockingClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	if c.calls.Add(1) == 1 {
		c.once.Do(func() { close(c.started) })
		<-ctx.Done()
		return ctx.Err()
	}
	*resp.Data.(*json.RawMessage) = json.RawMessage(`{"engineVersion":"v1"}`)
	return nil
}

func TestLoadDirWaiterOutlivesCancelledInitializer(t *testing.T) {
	schema, err := SchemaFromSDL(fakeSchema, "fake.graphqls")
	require.NoError(t, err)
	client := &blockingClient{started: make(chan struct{})}
	ctx := ContextWithImportConfigs(context.Background(), ImportConfig{Name: "Test", Schema: schema, Client: client, AutoImport: true})
	m, err := LoadDir(ctx, writeModule(t, map[string]string{
		"main.dang": "let version: String! = engineVersion\n",
	}), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()

	cancelled, cancel := context.WithCancel(ctx)
	firstErr := make(chan error, 1)
	go func() {
		_, _, err := m.Eval(cancelled, "", "version")
		firstErr <- err
	}()
	<-client.started

	// A second read, with a live context, waits on the first's run.
	second := make(chan Value, 1)
	go func() {
		val, _, err := m.Eval(ctx, "", "version")
		assert.NoError(t, err)
		second <- val
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	require.ErrorIs(t, <-firstErr, context.Canceled)
	require.Equal(t, StringValue{Val: "v1"}, <-second)
}

func TestLoadDirFromFS(t *testing.T) {
	fsys := fstest.MapFS{}
	for name, source := range scopeModule {
		fsys["mods/doc/"+name] = &fstest.MapFile{Data: []byte(source)}
	}
	fsys["mods/doc/dang.toml"] = &fstest.MapFile{Data: []byte("dang = \"2.1\"\n")}

	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	m, err := LoadDir(ctx, "mods/doc", LoadOptions{FS: fsys})
	require.NoError(t, err)
	defer m.Close()
	require.Equal(t, "mods/doc", m.Dir)
	require.Len(t, m.Files, 2)
	require.Equal(t, Version{Major: 2, Minor: 1}, m.Version)
	require.Empty(t, out.String())

	val, _, err := m.Eval(ctx, "", `Doc(body: "x\n\ny").summary`)
	require.NoError(t, err)
	require.Equal(t, StringValue{Val: "x"}, val)

	// A type error in the module points at the FS file's own text.
	bad := `let broken(n: Int!): String! { n }` + "\n"
	fsys["mods/bad/main.dang"] = &fstest.MapFile{Data: []byte(bad)}
	_, err = LoadDir(ctx, "mods/bad", LoadOptions{FS: fsys})
	var srcErr *SourceError
	require.ErrorAs(t, err, &srcErr)
	require.Equal(t, "mods/bad/main.dang", srcErr.Location.Filename)
	require.Equal(t, bad, srcErr.Source)

	// Imports a dang.toml declares can't be resolved from an FS.
	fsys["mods/imports/dang.toml"] = &fstest.MapFile{Data: []byte("[imports.Foo]\nschema = \"foo.graphqls\"\n")}
	fsys["mods/imports/main.dang"] = &fstest.MapFile{Data: []byte("let x = 1\n")}
	_, err = LoadDir(ctx, "mods/imports", LoadOptions{FS: fsys})
	require.ErrorContains(t, err, "ContextWithImportConfigs")
}

func TestLoadDirEmptyAndMissing(t *testing.T) {
	ctx := context.Background()
	m, err := LoadDir(ctx, t.TempDir(), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()
	val, typ, err := m.Eval(ctx, "", "1 + 1")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 2}, val)
	require.Equal(t, "Int!", typ.String())

	_, err = LoadDir(ctx, filepath.Join(t.TempDir(), "missing"), LoadOptions{})
	require.Error(t, err)
}
