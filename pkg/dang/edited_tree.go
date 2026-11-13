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

// treeSitterContext returns a context bounding one parse of source by
// treeSitterTimeout, compiling the module first if nothing has yet: the
// timeout is for the parse, and a cold compile on a busy machine can take
// most of it.
func treeSitterContext(source []byte) (context.Context, context.CancelFunc, error) {
	if err := tswasm.Load(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), treeSitterTimeout(source))
	return ctx, cancel, nil
}

// editedDoc is the source completion and highlighting last parsed. Both ask
// about one source again and again as it is typed, so each parse reuses the
// last one's tree where it can (tree-sitter's incremental reparsing). When
// it's busy, a parse starts afresh rather than wait. Its instance keeps the
// memory the largest source it parsed took, as wasm memory never shrinks.
// Around a syntax error the recovered tree can depend on what was parsed
// before (see tswasm.Document), so completion there can too: rarely (one
// step in about 13,000 when typing into and deleting from the test sources
// a byte at a time), and native tree-sitter reparses the same way.
var (
	editedDoc   = tswasm.NewDocument()
	editedDocMu sync.Mutex

	// incrementalParsing lets benchmarks turn the reuse off.
	incrementalParsing = true
)

// LoadTreeSitter readies tree-sitter for completion, highlighting and parse
// error messages ahead of their first use: compiling its WebAssembly module
// takes tens of milliseconds, far more on a busy machine, and would
// otherwise hold up the first of them. A failure is left for that first use
// to report.
func LoadTreeSitter() {
	_ = tswasm.Load()
}

// inspectEditedTree parses source, which is likely an edit of the last
// source it parsed, and calls f with its tree, read lazily (see
// tswasm.Inspect), so f pays only for what it looks at. It reports whether
// parsing succeeded.
func inspectEditedTree(source []byte, f func(root *tswasm.Node)) bool {
	ctx, cancel, err := treeSitterContext(source)
	if err != nil {
		return false
	}
	defer cancel()
	inspect := func(root *tswasm.Node) error {
		f(root)
		return nil
	}
	if incrementalParsing && editedDocMu.TryLock() {
		err = editedDoc.Inspect(ctx, source, inspect)
		editedDocMu.Unlock()
	} else {
		err = tswasm.Inspect(ctx, source, inspect)
	}
	return err == nil
}

// queryEditedTree runs q over source, which is likely an edit of the last
// source parsed, as inspectEditedTree does. Making q compiled the module, so
// all of the timeout goes to the query.
func queryEditedTree(q *tswasm.Query, source []byte) ([]tswasm.Capture, error) {
	ctx, cancel := context.WithTimeout(context.Background(), treeSitterTimeout(source))
	defer cancel()
	if incrementalParsing && editedDocMu.TryLock() {
		defer editedDocMu.Unlock()
		return editedDoc.Captures(ctx, q, source)
	}
	return q.Captures(ctx, source)
}
