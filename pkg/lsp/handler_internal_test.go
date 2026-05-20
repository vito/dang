package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedUpdateClearsAnalysis(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.dang")
	initialText := "pub value = 1\n"
	if err := os.WriteFile(mainPath, []byte(initialText), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	h := NewHandler(ctx)
	uri := toURI(mainPath)
	if err := h.openFile(uri, "dang", 1); err != nil {
		t.Fatalf("open file: %v", err)
	}
	version := 1
	if err := h.updateFile(ctx, uri, initialText, &version); err != nil {
		t.Fatalf("initial update: %v", err)
	}

	snapshot := h.waitForFile(uri)
	if snapshot == nil || snapshot.AST == nil {
		t.Fatalf("initial update did not produce an AST")
	}
	if snapshot.Symbols == nil || len(snapshot.Symbols.Definitions) == 0 {
		t.Fatalf("initial update did not produce symbols")
	}
	if snapshot.TypeEnv == nil {
		t.Fatalf("initial update did not produce a type env")
	}

	brokenPath := filepath.Join(dir, "broken.dang")
	if err := os.Symlink(filepath.Join(dir, "missing.dang"), brokenPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	updatedText := "pub changed = 2\n"
	version = 2
	if err := h.updateFile(ctx, uri, updatedText, &version); err == nil {
		t.Fatalf("update unexpectedly succeeded with unreadable sibling")
	}

	snapshot = h.waitForFile(uri)
	if snapshot == nil {
		t.Fatalf("file disappeared after failed update")
	}
	if snapshot.Text != updatedText {
		t.Fatalf("failed update did not keep latest text: got %q", snapshot.Text)
	}
	if snapshot.Version != version {
		t.Fatalf("failed update did not keep latest version: got %d, want %d", snapshot.Version, version)
	}
	if snapshot.AST != nil {
		t.Fatalf("failed update kept stale AST")
	}
	if snapshot.TypeEnv != nil {
		t.Fatalf("failed update kept stale type env")
	}
	if len(snapshot.Diagnostics) != 0 {
		t.Fatalf("failed update kept diagnostics: %#v", snapshot.Diagnostics)
	}
	if snapshot.Symbols == nil {
		t.Fatalf("failed update did not install an empty symbol table")
	}
	if len(snapshot.Symbols.Definitions) != 0 || len(snapshot.Symbols.References) != 0 {
		t.Fatalf("failed update kept stale symbols: %#v", snapshot.Symbols)
	}
}
