package dang

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/vito/dang/v2/pkg/ioctx"
)

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
