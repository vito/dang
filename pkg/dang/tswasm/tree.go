//go:build !js

package tswasm

import (
	"context"
)

// nodeWords is the size of csrc/shim.c's node struct, in uint32s.
const nodeWords = 10

const (
	flagMissing = 1 << iota
	flagError
	flagHasError
	flagExtra
	flagNamed
	flagPruned
)

// The builtin symbols past the grammar's own (ts_builtin_sym_error and
// ts_builtin_sym_error_repeat), which ts_language_symbol_count leaves out.
const (
	symbolError       = 0xFFFF
	symbolErrorRepeat = 0xFFFE
)

// decode fills n from the shim's node struct w, but for its children.
func (e *engine) decode(n *Node, w []byte, parent *Node) {
	flags := word(w, 1)
	*n = Node{
		Missing:     flags&flagMissing != 0,
		Error:       flags&flagError != 0,
		HasError:    flags&flagHasError != 0,
		Extra:       flags&flagExtra != 0,
		Named:       flags&flagNamed != 0,
		Pruned:      flags&flagPruned != 0,
		StartByte:   word(w, 3),
		EndByte:     word(w, 4),
		StartRow:    word(w, 5),
		StartColumn: word(w, 6),
		EndRow:      word(w, 7),
		EndColumn:   word(w, 8),
		Parent:      parent,
	}
	switch symbol := word(w, 0); {
	case int(symbol) < len(e.symbols):
		n.Kind = e.symbols[symbol]
	case symbol == symbolError:
		n.Kind = "ERROR"
	case symbol == symbolErrorRepeat:
		n.Kind = "_ERROR"
	}
	if field := word(w, 2); int(field) < len(e.fields) {
		n.Field = e.fields[field]
	}
}

// nodes decodes the whole kept tree, pruned as prune says.
func (inst *instance) nodes(ctx context.Context, prune Prune) (*Node, error) {
	res, err := call(ctx, inst.nodesFn, uint64(prune))
	if err != nil {
		return nil, err
	}
	if res == 0 {
		return nil, errFailed
	}
	header, err := inst.words(res, 2)
	if err != nil {
		return nil, err
	}
	count := word(header, 0)
	if count == 0 {
		return nil, errFailed
	}
	data, err := inst.words(word(header, 1), count*nodeWords)
	if err != nil {
		return nil, err
	}
	d := decoder{engine: inst.engine, data: data, all: make([]Node, count)}
	root := d.node(nil)
	if !d.ok {
		return nil, errFailed
	}
	return root, nil
}

// decoder rebuilds the tree from the shim's preorder node array.
type decoder struct {
	engine *engine
	data   []byte
	all    []Node // every node, allocated at once
	next   int
	ok     bool
}

func (d *decoder) node(parent *Node) *Node {
	if d.next >= len(d.all) {
		d.ok = false
		return nil
	}
	d.ok = true
	w := d.data[d.next*nodeWords*4:]
	n := &d.all[d.next]
	d.next++
	d.engine.decode(n, w, parent)
	if children := int(word(w, 9)); children > 0 {
		n.children = make([]*Node, children)
		for i := range n.children {
			n.children[i] = d.node(n)
			if !d.ok {
				return nil
			}
		}
	}
	return n
}

// lazyTree is the kept tree of an instance, read a node's children at a
// time (see Inspect).
type lazyTree struct {
	inst *instance
	ctx  context.Context
	done bool
	err  error
}

// inspect calls f with the kept tree, read lazily.
func (inst *instance) inspect(ctx context.Context, f func(*Node) error) error {
	t := &lazyTree{inst: inst, ctx: ctx}
	defer func() { t.done = true }()
	ptr, err := call(ctx, inst.rootFn)
	if err != nil {
		return err
	}
	if ptr == 0 {
		return errFailed
	}
	w, err := inst.words(ptr, nodeWords)
	if err != nil {
		return err
	}
	root := &Node{}
	t.adopt(root, w, nil, 0)
	if err := f(root); err != nil {
		return err
	}
	return t.err
}

// adopt decodes n from w, to read its children from t.
func (t *lazyTree) adopt(n *Node, w []byte, parent *Node, index uint32) {
	t.inst.engine.decode(n, w, parent)
	if count := word(w, 9); count > 0 {
		n.lazy = t
		n.index = index
		n.childCount = count
	}
}

// load reads n's children.
func (t *lazyTree) load(n *Node) {
	n.lazy = nil
	if t.done || t.err != nil {
		return
	}
	ptr, err := call(t.ctx, t.inst.childrenFn, uint64(n.index))
	if err == nil && ptr == 0 {
		err = errFailed
	}
	var res [2]uint32
	if err == nil {
		res, err = t.inst.result(t.ctx)
	}
	var data []byte
	if err == nil {
		data, err = t.inst.words(ptr, res[0]*nodeWords)
	}
	if err != nil {
		t.err = err
		return
	}
	all := make([]Node, res[0])
	n.children = make([]*Node, res[0])
	for i := range all {
		t.adopt(&all[i], data[i*nodeWords*4:], n, res[1]+uint32(i))
		n.children[i] = &all[i]
	}
}
