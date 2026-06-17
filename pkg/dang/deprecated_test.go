package dang

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vito/dang/v2/pkg/ioctx"
)

// TestDeprecatedBuiltinWarnsAtCallSite checks that calling a builtin marked
// .Deprecated(...) (here the restored toJSON) prints a warning to stderr that
// names the deprecation reason and points at the call site.
func TestDeprecatedBuiltinWarnsAtCallSite(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.dang")
	src := "let s = toJSON([1, 2, 3])\n"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr, stdout bytes.Buffer
	ctx := ioctx.StderrToContext(context.Background(), &stderr)
	ctx = ioctx.StdoutToContext(ctx, &stdout)

	if err := RunFile(ctx, file, false); err != nil {
		t.Fatalf("RunFile: %v", err)
	}

	out := stderr.String()
	if !strings.Contains(out, "toJSON is deprecated") {
		t.Errorf("expected deprecation warning naming toJSON, got: %q", out)
	}
	if !strings.Contains(out, "use JSON.encode instead") {
		t.Errorf("expected replacement hint in warning, got: %q", out)
	}
	// The warning must report where toJSON was called from.
	if !strings.Contains(out, "main.dang:1:") {
		t.Errorf("expected call-site location (main.dang:1:...) in warning, got: %q", out)
	}
}

// TestWarnAtSourceDedupesPerCallSite checks that repeated warnings from the same
// call site (e.g. a deprecated call inside a loop) collapse to one, while a
// distinct call site warns again.
func TestWarnAtSourceDedupesPerCallSite(t *testing.T) {
	var stderr bytes.Buffer
	ctx := ioctx.StderrToContext(context.Background(), &stderr)
	ctx = WithEvalContext(ctx, NewEvalContext("main.dang", "a\nb\n"))

	loc := &SourceLocation{Filename: "main.dang", Line: 1, Column: 1, Length: 1}
	WarnAtSource(ctx, loc, "foo is deprecated: bar")
	WarnAtSource(ctx, loc, "foo is deprecated: bar")
	if got := strings.Count(stderr.String(), "Warning:"); got != 1 {
		t.Errorf("expected 1 warning after dedupe, got %d: %q", got, stderr.String())
	}

	loc2 := &SourceLocation{Filename: "main.dang", Line: 2, Column: 1, Length: 1}
	WarnAtSource(ctx, loc2, "foo is deprecated: bar")
	if got := strings.Count(stderr.String(), "Warning:"); got != 2 {
		t.Errorf("expected 2 warnings across two call sites, got %d: %q", got, stderr.String())
	}
}
