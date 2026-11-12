//go:build !js

package tswasm

import (
	"bytes"
	"context"
	"sync"
)

// Document parses successive versions of one changing source, each
// reusing what it can of the last one's tree: tree-sitter's incremental
// reparsing. The edit between versions is worked out by comparing them.
// A Document holds an instance of its own until it is closed.
//
// As with tree-sitter anywhere, where a source has syntax errors the tree
// recovered from them can depend on the versions parsed before it, and so
// differ from a fresh parse's; an error-free source parses the same either
// way.
type Document struct {
	mu     sync.Mutex
	inst   *instance
	source []byte // the source the kept tree is of, or nil
}

// NewDocument returns a Document with nothing parsed yet.
func NewDocument() *Document {
	return &Document{}
}

// Parse parses source, a new version of the document, and returns its
// tree, pruned as prune says.
func (d *Document) Parse(ctx context.Context, source []byte, prune Prune) (*Node, error) {
	var root *Node
	err := d.with(ctx, source, func(inst *instance) error {
		var err error
		root, err = inst.nodes(ctx, prune)
		return err
	})
	return root, err
}

// Inspect parses source, a new version of the document, and calls f with
// its tree, as the package's Inspect does.
func (d *Document) Inspect(ctx context.Context, source []byte, f func(root *Node) error) error {
	return d.with(ctx, source, func(inst *instance) error {
		return inst.inspect(ctx, f)
	})
}

// Captures parses source, a new version of the document, and runs q over
// its tree (see Query.Captures).
func (d *Document) Captures(ctx context.Context, q *Query, source []byte) ([]Capture, error) {
	var captures []Capture
	err := d.with(ctx, source, func(inst *instance) error {
		var err error
		captures, err = inst.captures(ctx, q, source)
		return err
	})
	return captures, err
}

// Close releases the Document's instance.
func (d *Document) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inst != nil {
		d.inst.close()
		d.inst = nil
	}
	d.source = nil
}

func (d *Document) with(ctx context.Context, source []byte, f func(*instance) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inst == nil {
		e, err := loadEngine()
		if err != nil {
			return err
		}
		if d.inst, err = e.instantiate(); err != nil {
			return err
		}
	}
	err := d.parse(ctx, source)
	if err == nil {
		err = f(d.inst)
	}
	if err != nil {
		// Start afresh: f's error may be a trap's.
		d.inst.close()
		d.inst = nil
		d.source = nil
	}
	return err
}

func (d *Document) parse(ctx context.Context, source []byte) error {
	reuse := d.source != nil
	if reuse {
		e := diff(d.source, source)
		if _, err := d.inst.edit.Call(ctx,
			uint64(e.start), uint64(e.oldEnd), uint64(e.newEnd),
			uint64(e.startPoint.row), uint64(e.startPoint.col),
			uint64(e.oldEndPoint.row), uint64(e.oldEndPoint.col),
			uint64(e.newEndPoint.row), uint64(e.newEndPoint.col),
		); err != nil {
			return err
		}
	}
	d.source = nil
	if err := d.inst.parse(ctx, source, reuse); err != nil {
		return err
	}
	d.source = bytes.Clone(source)
	return nil
}

type point struct{ row, col uint32 }

// edit is a tree-sitter input edit: bytes [start, oldEnd) of the old source
// became [start, newEnd) of the new one.
type edit struct {
	start, oldEnd, newEnd                uint32
	startPoint, oldEndPoint, newEndPoint point
}

// diff describes the change from old to new as one edit, spanning from the
// first byte that differs to the last.
func diff(old, new []byte) edit {
	prefix := 0
	for prefix < len(old) && prefix < len(new) && old[prefix] == new[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(new)-prefix &&
		old[len(old)-1-suffix] == new[len(new)-1-suffix] {
		suffix++
	}
	return edit{
		start:       uint32(prefix),
		oldEnd:      uint32(len(old) - suffix),
		newEnd:      uint32(len(new) - suffix),
		startPoint:  pointAt(old, prefix),
		oldEndPoint: pointAt(old, len(old)-suffix),
		newEndPoint: pointAt(new, len(new)-suffix),
	}
}

// pointAt is the row and byte column of offset in source.
func pointAt(source []byte, offset int) point {
	before := source[:offset]
	row := bytes.Count(before, []byte{'\n'})
	col := offset - (bytes.LastIndexByte(before, '\n') + 1)
	return point{uint32(row), uint32(col)}
}
