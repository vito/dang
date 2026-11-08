//go:build unix

package analysis_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

func TestDiagnosticsDontReadTheDisk(t *testing.T) {
	// A FIFO where the working directory would have the Source's file: any
	// read of it blocks.
	dir := t.TempDir()
	fifo := filepath.Join(dir, "main.dang")
	require.NoError(t, syscall.Mkfifo(fifo, 0o644))
	t.Chdir(dir)

	src := mapFS(map[string]string{
		"main.dang": "let z = undefined_var\n",
	})

	done := make(chan []analysis.Diagnostic, 1)
	go func() {
		a, err := analysis.Analyze(context.Background(), src, ".", analysis.Options{})
		if err != nil {
			t.Error(err)
			done <- nil
			return
		}
		done <- a.Diagnostics()
	}()

	select {
	case diags := <-done:
		require.Len(t, diags, 1)
		require.Equal(t, `"undefined_var" not found`, diags[0].Message)
	case <-time.After(10 * time.Second):
		// Unblock the reader before failing.
		if f, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			_ = f.Close()
		}
		t.Fatal("analysis read main.dang from the working directory")
	}
}
