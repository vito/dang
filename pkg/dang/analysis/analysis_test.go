package analysis_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

func mapFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for p, text := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(text)}
	}
	return fsys
}

func TestDiagnosticsReportTypeErrors(t *testing.T) {
	src := mapFS(map[string]string{
		"mod/main.dang": "let x: Int! = 1\nlet y = \"hello\" + x\nlet z = undefined_var\n",
	})

	a, err := analysis.Analyze(context.Background(), src, "mod", analysis.Options{})
	require.NoError(t, err)
	require.Equal(t, "mod", a.Dir)
	require.Len(t, a.Files, 1)
	require.Equal(t, "mod/main.dang", a.Files[0].Path)
	require.NotNil(t, a.Files[0].Block)

	diags := a.Diagnostics()
	require.Len(t, diags, 2)
	for _, d := range diags {
		require.Equal(t, "mod/main.dang", d.Path)
		require.Equal(t, analysis.SeverityError, d.Severity)
		require.Equal(t, analysis.CodeType, d.Code)
	}
	// The Source is in memory: messages are the errors' own, with nothing
	// read from (or failing to read from) the host's disk; see
	// TestDiagnosticsDontReadTheDisk.
	require.Equal(t, "operator addition is not defined between types String! and Int!", diags[0].Message)
	require.Equal(t, 2, diags[0].Range.Start.Line)
	require.Equal(t, `"undefined_var" not found`, diags[1].Message)
	// 1-based: undefined_var starts at the 9th character of line 3.
	require.Equal(t, analysis.Pos{Line: 3, Column: 9}, diags[1].Range.Start)
	require.Equal(t, analysis.Pos{Line: 3, Column: 9 + len("undefined_var")}, diags[1].Range.End)
}

func TestDiagnosticsReportParseErrors(t *testing.T) {
	src := mapFS(map[string]string{
		"broken.dang": "let x = (\n",
		"fine.dang":   "pub y = 1\n",
	})

	a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{})
	require.NoError(t, err)

	diags := a.Diagnostics()
	require.NotEmpty(t, diags)
	require.Equal(t, "broken.dang", diags[0].Path)
	require.Equal(t, analysis.CodeSyntax, diags[0].Code)
	require.Equal(t, analysis.SeverityError, diags[0].Severity)

	// The broken file is listed without a block; its sibling still checks.
	require.Nil(t, a.File("broken.dang").Block)
	require.NotNil(t, a.File("fine.dang").Block)
	_, found := a.FileScope("fine.dang").LocalSchemeOf("y")
	require.True(t, found)
}

func TestFocusChecksOnlyTheFocusFile(t *testing.T) {
	src := mapFS(map[string]string{
		"broken.dang": "helper: String! { \"ok\" }\n\npub bad = \"hello\" + 42\n",
		"main.dang":   "greeting: String! { helper }\n",
	})

	focused, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{Focus: "main.dang"})
	require.NoError(t, err)
	require.Empty(t, focused.Diagnostics(), "sibling bodies are not checked under a focus")

	full, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{})
	require.NoError(t, err)
	diags := full.Diagnostics()
	require.Len(t, diags, 1)
	require.Equal(t, "broken.dang", diags[0].Path)
	require.Equal(t, 3, diags[0].Range.Start.Line)
}

func TestFocusSeesUnannotatedSiblingVars(t *testing.T) {
	src := mapFS(map[string]string{
		"defs.dang": "pub answer = \"ok\".toUpper\n",
		"main.dang": "use: String! = answer\n",
	})

	a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{Focus: "main.dang"})
	require.NoError(t, err)
	require.Empty(t, a.Diagnostics())
}

func TestFocusOnUnparsableFile(t *testing.T) {
	src := mapFS(map[string]string{
		"main.dang": "let x = (\n",
		"defs.dang": "pub bad = \"hello\" + 42\n",
	})

	a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{
		Focus: "main.dang",
		ResolveImports: func(context.Context) []dang.ImportConfig {
			t.Error("imports are resolved only once the focus parses")
			return nil
		},
	})
	require.NoError(t, err)
	diags := a.Diagnostics()
	require.Len(t, diags, 1)
	require.Equal(t, analysis.CodeSyntax, diags[0].Code)
	require.Nil(t, a.Scope, "nothing is inferred when the focus doesn't parse")
	require.Nil(t, a.FileScope("main.dang"))
}

func TestFocusMustBeAFileOfDir(t *testing.T) {
	src := mapFS(map[string]string{
		"mod/main.dang": "let y = undefined_var\n",
	})

	a, err := analysis.Analyze(context.Background(), src, "mod", analysis.Options{Focus: "./mod/main.dang"})
	require.NoError(t, err)
	require.Equal(t, "mod/main.dang", a.Focus)
	require.Len(t, a.Diagnostics(), 1)

	_, err = analysis.Analyze(context.Background(), src, "mod", analysis.Options{Focus: "mod/missing.dang"})
	require.ErrorContains(t, err, "focus mod/missing.dang is not one of its .dang files")
}

func TestErrorsOfImportedModulesAreReported(t *testing.T) {
	// A path import is compiled from the host: its errors name its own
	// files, outside the Source. They're why the import fails, so a full
	// check reports them, with no path.
	lib := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(lib, "lib.dang"), []byte("pub x: Int! = \"s\"\n"), 0o644))
	src := mapFS(map[string]string{
		"main.dang": "import Lib\n\npub y = Lib.x\n",
	})
	imports := []dang.ImportConfig{{Name: "Lib", DangModuleDir: lib}}

	a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{ImportConfigs: imports})
	require.NoError(t, err)
	var foreign []analysis.Diagnostic
	for _, d := range a.Diagnostics() {
		if d.Path == "" {
			foreign = append(foreign, d)
		}
	}
	require.NotEmpty(t, foreign, "got %v", a.Diagnostics())
	require.Contains(t, foreign[0].Message, filepath.Join(lib, "lib.dang")+":1:")

	// Focused on a file, only that file's are.
	focused, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{ImportConfigs: imports, Focus: "main.dang"})
	require.NoError(t, err)
	for _, d := range focused.Diagnostics() {
		require.Equal(t, "main.dang", d.Path)
	}
}

func TestResolveImportsAddsImports(t *testing.T) {
	schema, err := dang.SchemaFromSDLFile(filepath.Join("..", "..", "..", "tests", "gqlserver", "schema.graphqls"))
	require.NoError(t, err)
	src := mapFS(map[string]string{
		"main.dang": "import Test\n\npub info: Test.ServerInfo! = Test.serverInfo\n",
	})

	a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{
		ResolveImports: func(context.Context) []dang.ImportConfig {
			return []dang.ImportConfig{{Name: "Test", Schema: schema}}
		},
	})
	require.NoError(t, err)
	require.Empty(t, a.Diagnostics())
}

func TestImportReplacesBuiltinImport(t *testing.T) {
	// A file importing a schema named like a builtin import sees the schema
	// under that name; a sibling file that doesn't sees the builtin.
	schema, err := dang.SchemaFromSDLFile(filepath.Join("..", "..", "..", "tests", "gqlserver", "schema.graphqls"))
	require.NoError(t, err)
	mainSrc := "import YAML\n\npub info: YAML.ServerInfo! = YAML.serverInfo\n"
	otherSrc := "pub encoded = YAML.encode(1)\n"
	src := mapFS(map[string]string{
		"main.dang":  mainSrc,
		"other.dang": otherSrc,
	})

	a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{
		ImportConfigs: []dang.ImportConfig{{Name: "YAML", Schema: schema}},
	})
	require.NoError(t, err)
	require.Empty(t, a.Diagnostics())

	sym, err := a.Inspect("main.dang", at(t, mainSrc, "YAML.serverInfo", 0, 0))
	require.NoError(t, err)
	require.NotNil(t, sym.Member)
	_, found := sym.Member.Scope.NamedType("ServerInfo")
	require.True(t, found, "YAML is the schema in the file importing it")

	sym, err = a.Inspect("other.dang", at(t, otherSrc, "YAML.encode", 0, 0))
	require.NoError(t, err)
	require.NotNil(t, sym.Member)
	require.Same(t, dang.YAMLModule, sym.Member.Scope, "YAML is the builtin import elsewhere")
}

func TestImportTypesOverBuiltinImportsBefore2_4(t *testing.T) {
	// Before 2.4, a bare type named like a builtin import is the type an
	// import brings in unqualified, as type checking and evaluation have it;
	// from 2.4 on, it is the builtin import's namespace.
	schema, err := dang.SchemaFromSDL("type Query {\n  echo(value: JSON!): JSON!\n}\n\nscalar JSON\n", "schema.graphqls")
	require.NoError(t, err)
	mainSrc := "pub call(fnArgs: JSON!): JSON! {\n  (JSON.encode({{ fnArgs: fnArgs }}) :: JSON!)\n}\n"
	src := mapFS(map[string]string{"main.dang": mainSrc})
	imports := []dang.ImportConfig{{Name: "Dagger", Schema: schema, AutoImport: true}}

	a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{ImportConfigs: imports, Version: dang.Version2_1})
	require.NoError(t, err)
	require.Empty(t, a.Diagnostics())
	sym, err := a.Inspect("main.dang", at(t, mainSrc, "JSON.encode", 0, 0))
	require.NoError(t, err)
	require.NotNil(t, sym.Member)
	require.Same(t, dang.JSONModule, sym.Member.Scope, "the value is the builtin namespace")

	a, err = analysis.Analyze(context.Background(), src, ".", analysis.Options{ImportConfigs: imports, Version: dang.Version2_4})
	require.NoError(t, err)
	require.NotEmpty(t, a.Diagnostics())
	require.Contains(t, a.Diagnostics()[0].Message, "hint expects JSON!")
}

func TestDiagnosticsFlagDeprecatedBuiltins(t *testing.T) {
	src := mapFS(map[string]string{
		"main.dang": "let s = toJSON([1, 2, 3])\n",
	})

	a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{})
	require.NoError(t, err)
	require.Equal(t, []analysis.Diagnostic{{
		Range: analysis.Range{
			Start: analysis.Pos{Line: 1, Column: 9},
			End:   analysis.Pos{Line: 1, Column: 9 + len("toJSON")},
		},
		Path:        "main.dang",
		Severity:    analysis.SeverityWarning,
		Code:        analysis.CodeDeprecated,
		Message:     "toJSON is deprecated: use JSON.encode instead",
		Replacement: "JSON.encode",
	}}, a.Diagnostics())
	require.Equal(t,
		"main.dang:1:9: warning: toJSON is deprecated: use JSON.encode instead (quick fix: JSON.encode)",
		a.Diagnostics()[0].String())
}

func TestDiagnosticsReportInferenceWarnings(t *testing.T) {
	src := mapFS(map[string]string{
		"main.dang": "let g = \"hello\" rescue \"?\"\n",
	})

	a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{})
	require.NoError(t, err)
	diags := a.Diagnostics()
	require.Len(t, diags, 1)
	require.Equal(t, analysis.SeverityWarning, diags[0].Severity)
	require.Equal(t, analysis.CodeWarning, diags[0].Code)
	require.Contains(t, diags[0].Message, "this rescue can never fire")
	require.Equal(t, analysis.Pos{Line: 1, Column: 9}, diags[0].Range.Start)
}

func TestRootNamesFiles(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "work", "mod")
	src := mapFS(map[string]string{
		"main.dang": "let y = undefined_var\n",
	})

	a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{Root: root})
	require.NoError(t, err)
	f := a.File("main.dang")
	require.Equal(t, filepath.Join(root, "main.dang"), f.Name)
	require.Equal(t, f.Name, f.Block.Loc.Filename, "positions name the file under Root")

	diags := a.Diagnostics()
	require.Len(t, diags, 1)
	require.Equal(t, "main.dang", diags[0].Path, "diagnostics report Source paths")
}

func TestMissingDirectoryHasNoFiles(t *testing.T) {
	a, err := analysis.Analyze(context.Background(), fstest.MapFS{}, "nope", analysis.Options{})
	require.NoError(t, err)
	require.Empty(t, a.Files)
	require.Empty(t, a.Diagnostics())
}

// failingFS fails to stat one file, like a broken symlink on disk.
type failingFS struct {
	fstest.MapFS
	broken string
}

func (f failingFS) Stat(name string) (fs.FileInfo, error) {
	if name == f.broken {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return f.MapFS.Stat(name)
}

func TestReadErrorsFailTheAnalysis(t *testing.T) {
	src := failingFS{
		MapFS: mapFS(map[string]string{
			"main.dang":   "pub value = 1\n",
			"broken.dang": "",
		}),
		broken: "broken.dang",
	}

	_, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{})
	var pathErr *fs.PathError
	require.True(t, errors.As(err, &pathErr), "got %v", err)
}

func TestOverlayLaysFilesOverBase(t *testing.T) {
	base := mapFS(map[string]string{
		"main.dang": "let y = undefined_var\n",
		"defs.dang": "pub x = 1\n",
	})
	src := analysis.Overlay(base, map[string]string{
		"main.dang":    "let y = x + other\n",
		"unsaved.dang": "pub other = 2\n",
	})

	a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{Focus: "main.dang"})
	require.NoError(t, err)
	require.Empty(t, a.Diagnostics(), "the overlaid buffer and the unsaved file are analyzed")
	require.Equal(t, []string{"defs.dang", "main.dang", "unsaved.dang"}, paths(a))
	require.Equal(t, "let y = x + other\n", a.File("main.dang").Text)
}

func TestParseCacheReusesUnchangedFiles(t *testing.T) {
	ctx := context.Background()
	mtime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	src := fstest.MapFS{
		"disk.dang":   &fstest.MapFile{Data: []byte("pub a = 1\n"), ModTime: mtime},
		"memory.dang": &fstest.MapFile{Data: []byte("pub b = 2\n")},
	}
	cache := &analysis.ParseCache{}

	first, err := analysis.Analyze(ctx, src, ".", analysis.Options{Parses: cache})
	require.NoError(t, err)
	second, err := analysis.Analyze(ctx, src, ".", analysis.Options{Parses: cache})
	require.NoError(t, err)
	require.Same(t, first.File("disk.dang").Block, second.File("disk.dang").Block)
	require.Same(t, first.File("memory.dang").Block, second.File("memory.dang").Block)

	// A file without a modification time is compared by its text.
	src["memory.dang"] = &fstest.MapFile{Data: []byte("pub b = 3\n")}
	// A file with one is compared by time and size.
	src["disk.dang"] = &fstest.MapFile{Data: []byte("pub a = 10\n"), ModTime: mtime.Add(time.Second)}
	third, err := analysis.Analyze(ctx, src, ".", analysis.Options{Parses: cache})
	require.NoError(t, err)
	require.NotSame(t, first.File("disk.dang").Block, third.File("disk.dang").Block)
	require.NotSame(t, first.File("memory.dang").Block, third.File("memory.dang").Block)

	// A different Dang version re-parses.
	fourth, err := analysis.Analyze(ctx, src, ".", analysis.Options{Parses: cache, Version: dang.Version1_0})
	require.NoError(t, err)
	require.NotSame(t, third.File("disk.dang").Block, fourth.File("disk.dang").Block)
}

func TestSchemaModuleCacheUnifiesImportedTypes(t *testing.T) {
	// Two files exchanging an imported type must agree on its identity, even
	// when they are analyzed one at a time, as an editor does.
	ctx := context.Background()
	schema, err := dang.SchemaFromSDLFile(filepath.Join("..", "..", "..", "tests", "gqlserver", "schema.graphqls"))
	require.NoError(t, err)
	imports := []dang.ImportConfig{{Name: "Test", Schema: schema}}

	src := mapFS(map[string]string{
		"holder.dang": "import Test\n\ntype Holder {\n  let info: Test.ServerInfo!\n}\n",
		"owner.dang":  "import Test\n\ntype Owner {\n  let info: Test.ServerInfo!\n\n  make: Holder! {\n    Holder(info: info)\n  }\n}\n",
	})
	cache := &sync.Map{}
	parses := &analysis.ParseCache{}

	holder, err := analysis.Analyze(ctx, src, ".", analysis.Options{
		Focus: "holder.dang", ImportConfigs: imports, SchemaModuleCache: cache, Parses: parses,
	})
	require.NoError(t, err)
	require.Empty(t, holder.Diagnostics())

	// Re-parse holder.dang, as an open-buffer transition does.
	src["holder.dang"] = &fstest.MapFile{Data: append(src["holder.dang"].Data, '\n')}

	owner, err := analysis.Analyze(ctx, src, ".", analysis.Options{
		Focus: "owner.dang", ImportConfigs: imports, SchemaModuleCache: cache, Parses: parses,
	})
	require.NoError(t, err)
	require.Empty(t, owner.Diagnostics(), "Test.ServerInfo should unify across files")
}

func paths(a *analysis.Analysis) []string {
	var ps []string
	for _, f := range a.Files {
		ps = append(ps, f.Path)
	}
	return ps
}
