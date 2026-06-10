package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/dagger/otel-go/oteltestctx"
	"github.com/dagger/testctx"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/ioctx"
)

func TestMain(m *testing.M) {
	os.Exit(oteltestctx.Main(m))
}

type DangSuite struct {
}

func TestDang(tT *testing.T) {
	testctx.New(tT,
		oteltestctx.WithTracing[*testing.T](),
	).RunTests(DangSuite{})
}

func (DangSuite) TestLanguage(ctx context.Context, t *testctx.T) {
	runLanguageTests(ctx, t)
}

func (DangSuite) TestFormatLanguage(_ context.Context, t *testctx.T) {
	runFormatLanguageTests(t)
}

func runLanguageTests(ctx context.Context, t *testctx.T) {
	// Import configs come from dang.toml (schema + service).
	// Pre-load them so they're available to each test without re-resolving.
	services := &dang.ServiceRegistry{}
	t.Cleanup(func() { services.StopAll() })

	var importConfigs []dang.ImportConfig
	configPath, projectConfig, configErr := dang.FindProjectConfig(".")
	if configErr == nil && projectConfig != nil {
		ctx = dang.ContextWithServices(ctx, services)
		ctx = dang.ContextWithProjectConfig(ctx, configPath, projectConfig)
		configDir := filepath.Dir(configPath)
		resolved, err := dang.ResolveImportConfigs(ctx, projectConfig, configDir)
		if err != nil {
			t.Fatalf("Failed to resolve dang.toml imports: %v", err)
		}
		importConfigs = resolved
	}

	// Find all test_*.dang files or test_* packages
	paths, err := filepath.Glob("test_*")
	if err != nil {
		t.Fatalf("Failed to find test files: %v", err)
	}

	if len(paths) == 0 {
		t.Skip("No test_* files or directories found")
	}

	// Run each test file or package.
	for _, testFileOrDir := range paths {
		t.Run(filepath.Base(testFileOrDir), func(ctx context.Context, t *testctx.T) {
			ctx = ioctx.StdoutToContext(ctx, NewTWriter(t))
			ctx = dang.ContextWithServices(ctx, services)
			if len(importConfigs) > 0 {
				ctx = dang.ContextWithImportConfigs(ctx, importConfigs...)
			}

			// t.Parallel()
			fi, err := os.Stat(testFileOrDir)
			if err != nil {
				t.Errorf("Failed to stat test file or directory %s: %v", testFileOrDir, err)
				return
			}

			var runErr error
			if fi.IsDir() {
				_, runErr = dang.RunDir(ctx, testFileOrDir, false)
			} else {
				runErr = dang.RunFile(ctx, testFileOrDir, false)
			}

			if runErr != nil {
				t.Error(runErr)
			}
		})
	}
}

func runFormatLanguageTests(t *testctx.T) {
	// Find all test_*.dang files or test_* packages
	paths, err := filepath.Glob("test_*")
	if err != nil {
		t.Fatalf("Failed to find test files: %v", err)
	}

	if len(paths) == 0 {
		t.Skip("No test_* files or directories found")
	}

	for _, testFileOrDir := range paths {
		t.Run(filepath.Base(testFileOrDir), func(_ context.Context, t *testctx.T) {
			fi, err := os.Stat(testFileOrDir)
			if err != nil {
				t.Errorf("Failed to stat test file or directory %s: %v", testFileOrDir, err)
				return
			}

			if !fi.IsDir() {
				assertFormatPreservesSyntax(t, testFileOrDir)
				return
			}

			entries, err := os.ReadDir(testFileOrDir)
			if err != nil {
				t.Errorf("Failed to read dir %s: %v", testFileOrDir, err)
				return
			}

			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".dang") {
					continue
				}

				testPath := filepath.Join(testFileOrDir, entry.Name())
				t.Run(entry.Name(), func(_ context.Context, t *testctx.T) {
					assertFormatPreservesSyntax(t, testPath)
				})
			}
		})
	}
}

func assertFormatPreservesSyntax(t *testctx.T, path string) {
	source, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("Failed to read %s: %v", path, err)
		return
	}

	before, err := parseSyntaxTree(path, source)
	if err != nil {
		t.Errorf("Failed to parse %s: %v", path, err)
		return
	}

	formatted, err := dang.FormatFile(source)
	if err != nil {
		t.Errorf("Failed to format %s: %v", path, err)
		return
	}

	after, err := parseSyntaxTree(path+" (formatted)", []byte(formatted))
	if err != nil {
		t.Errorf("Failed to parse formatted %s: %v\nFormatted source:\n%s", path, err, formatted)
		return
	}

	if !reflect.DeepEqual(before, after) {
		t.Errorf(
			"format changed syntax tree for %s\nFormatted source:\n%s\nBefore:\n%s\nAfter:\n%s",
			path,
			formatted,
			formatSyntaxTree(before),
			formatSyntaxTree(after),
		)
	}
}

func parseSyntaxTree(path string, source []byte) (any, error) {
	parsed, err := dang.Parse(path, source, dang.GlobalStore("filePath", path))
	if err != nil {
		return nil, err
	}

	return syntaxTreeSnapshot(parsed), nil
}

var (
	sourceLocationType     = reflect.TypeOf(dang.SourceLocation{})
	inferredTypeHolderType = reflect.TypeOf(dang.InferredTypeHolder{})
)

type syntaxTreeVisit struct {
	typ reflect.Type
	ptr uintptr
}

func syntaxTreeSnapshot(v any) any {
	return syntaxTreeSnapshotValue(reflect.ValueOf(v), map[syntaxTreeVisit]bool{})
}

func syntaxTreeSnapshotValue(v reflect.Value, seen map[syntaxTreeVisit]bool) any {
	if !v.IsValid() {
		return nil
	}

	if isSourceLocationType(v.Type()) {
		return nil
	}

	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return syntaxTreeSnapshotValue(v.Elem(), seen)
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		visit := syntaxTreeVisit{typ: v.Type(), ptr: v.Pointer()}
		if seen[visit] {
			return map[string]any{"$ref": v.Type().String()}
		}
		seen[visit] = true
		defer delete(seen, visit)
		return syntaxTreeSnapshotValue(v.Elem(), seen)
	case reflect.Struct:
		if v.Type() == inferredTypeHolderType {
			return nil
		}

		fields := map[string]any{"$type": v.Type().String()}
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if field.PkgPath != "" || skipSyntaxTreeField(field) {
				continue
			}
			fields[field.Name] = syntaxTreeSnapshotValue(v.Field(i), seen)
		}
		return fields
	case reflect.Slice, reflect.Array:
		elements := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			elements[i] = syntaxTreeSnapshotValue(v.Index(i), seen)
		}
		return map[string]any{
			"$type":    v.Type().String(),
			"Elements": elements,
		}
	case reflect.Map:
		entries := make([]map[string]any, 0, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			entries = append(entries, map[string]any{
				"Key":   syntaxTreeSnapshotValue(iter.Key(), seen),
				"Value": syntaxTreeSnapshotValue(iter.Value(), seen),
			})
		}
		sort.Slice(entries, func(i, j int) bool {
			return fmt.Sprint(entries[i]["Key"]) < fmt.Sprint(entries[j]["Key"])
		})
		return map[string]any{
			"$type":   v.Type().String(),
			"Entries": entries,
		}
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint()
	case reflect.Float32, reflect.Float64:
		return v.Float()
	case reflect.String:
		return v.String()
	case reflect.Func:
		return nil
	default:
		if v.CanInterface() {
			return fmt.Sprintf("%#v", v.Interface())
		}
		return fmt.Sprintf("<%s>", v.Type())
	}
}

func isSourceLocationType(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t == sourceLocationType
}

func skipSyntaxTreeField(field reflect.StructField) bool {
	if isSourceLocationType(field.Type) || field.Type.Kind() == reflect.Func {
		return true
	}

	if field.Type == inferredTypeHolderType {
		return true
	}

	switch field.Name {
	case "ContextInferredType", "Env", "Inferred", "InferredScope", "InferredTypeHolder":
		return true
	default:
		return false
	}
}

func formatSyntaxTree(tree any) string {
	jsonBytes, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		return fmt.Sprintf("%#v", tree)
	}
	return string(jsonBytes)
}

// tWriter is a writer that writes to testing.T
type tWriter struct {
	t   testing.TB
	buf bytes.Buffer
	mu  sync.Mutex
}

// NewTWriter creates a new TWriter
func NewTWriter(t testing.TB) io.Writer {
	tw := &tWriter{t: t}
	t.Cleanup(tw.flush)
	return tw
}

// Write writes data to the testing.T
func (tw *tWriter) Write(p []byte) (n int, err error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	tw.t.Helper()

	if n, err = tw.buf.Write(p); err != nil {
		return n, err
	}

	for {
		line, err := tw.buf.ReadBytes('\n')
		if err == io.EOF {
			// If we've reached the end of the buffer, write it back, because it doesn't have a newline
			tw.buf.Write(line)
			break
		}
		if err != nil {
			return n, err
		}

		tw.t.Log(strings.TrimSuffix(string(line), "\n"))
	}
	return n, nil
}

func (tw *tWriter) flush() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.buf.Len() > 0 {
		tw.t.Log(tw.buf.String())
	}
}
