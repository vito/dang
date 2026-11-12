//go:build !js && !dang_nowasm

package dang

import (
	"context"
	"sync"
	"time"

	"github.com/vito/dang/v2/pkg/dang/tswasm"
)

// treeSitterTimeout bounds one tree-sitter parse or query of source: it's
// there to stop a runaway parse, never to cut short a slow one, so it grows
// with the source. The wasm parser takes about a microsecond a byte
// (measured on a 20 KB file with a syntax error); this allows twenty, with a
// second's floor.
func treeSitterTimeout(source []byte) time.Duration {
	return max(time.Second, time.Duration(len(source))*20*time.Microsecond)
}

// editedDoc is the source completion and highlighting last parsed. Both ask
// about one source again and again as it is typed, so each parse reuses the
// last one's tree where it can (tree-sitter's incremental reparsing). When
// it's busy, a parse starts afresh rather than wait. Its instance keeps the
// memory the largest source it parsed took, as wasm memory never shrinks.
var (
	editedDoc   = tswasm.NewDocument()
	editedDocMu sync.Mutex

	// incrementalParsing lets benchmarks turn the reuse off.
	incrementalParsing = true
)

// inspectEditedTree parses source, which is likely an edit of the last
// source it parsed, and calls f with its tree, read lazily (see
// tswasm.Inspect), so f pays only for what it looks at. It reports whether
// parsing succeeded.
func inspectEditedTree(source []byte, f func(root *tswasm.Node)) bool {
	ctx, cancel := context.WithTimeout(context.Background(), treeSitterTimeout(source))
	defer cancel()
	inspect := func(root *tswasm.Node) error {
		f(root)
		return nil
	}
	var err error
	if incrementalParsing && editedDocMu.TryLock() {
		err = editedDoc.Inspect(ctx, source, inspect)
		editedDocMu.Unlock()
	} else {
		err = tswasm.Inspect(ctx, source, inspect)
	}
	return err == nil
}

// queryEditedTree runs q over source, which is likely an edit of the last
// source parsed, as inspectEditedTree does.
func queryEditedTree(q *tswasm.Query, source []byte) ([]tswasm.Capture, error) {
	ctx, cancel := context.WithTimeout(context.Background(), treeSitterTimeout(source))
	defer cancel()
	if incrementalParsing && editedDocMu.TryLock() {
		defer editedDocMu.Unlock()
		return editedDoc.Captures(ctx, q, source)
	}
	return q.Captures(ctx, source)
}
