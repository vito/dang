//go:build !js

package tswasm

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEngineSurvivesACorruptCache checks that a compilation cache entry that
// can't be read costs only the time it would have saved.
func TestEngineSurvivesACorruptCache(t *testing.T) {
	dir := t.TempDir()
	if _, err := newEngine(dir); err != nil {
		t.Fatal(err)
	}
	var entries int
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		entries++
		return os.WriteFile(path, []byte("not a module"), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if entries == 0 {
		t.Fatal("nothing was cached")
	}
	e, err := newEngine(dir)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := e.get()
	if err != nil {
		t.Fatal(err)
	}
	defer inst.close()
	if err := inst.parse(context.Background(), []byte("let x = 1\n"), false); err != nil {
		t.Fatal(err)
	}
}

// TestPoolDropsGrownInstances checks that an instance whose memory grew past
// maxIdleMemory to parse a large source is closed rather than pooled: its
// memory would never shrink.
func TestPoolDropsGrownInstances(t *testing.T) {
	big := []byte(strings.Repeat("let x = [1, 2, (3 + 4)]\n", 20_000) + "let y = (1\n")
	if _, err := Parse(context.Background(), big, PruneErrorFree); err != nil {
		t.Fatal(err)
	}
	e, err := loadEngine()
	if err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case inst := <-e.idle:
			if size := inst.mod.Memory().Size(); size > maxIdleMemory {
				t.Errorf("pooled an instance with %d bytes of memory", size)
			}
			inst.mod.Close(context.Background())
		default:
			return
		}
	}
}
