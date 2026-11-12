//go:build !js

package tswasm

// Node is a tree-sitter syntax node.
type Node struct {
	// Kind is the node's type, as tree-sitter names it (ts_node_type):
	// "ERROR" for an error, the expected token for a missing one.
	Kind string

	// Field is the field the node fills in its parent, or "".
	Field string

	Missing  bool // inserted by error recovery (ts_node_is_missing)
	Error    bool // an ERROR node (ts_node_is_error)
	HasError bool // an ERROR or missing node is at or beneath it
	Extra    bool
	Named    bool

	// Pruned is set when the node has children that the parse left out
	// (see Prune).
	Pruned bool

	StartByte, EndByte    uint32
	StartRow, StartColumn uint32 // 0-based; columns count bytes
	EndRow, EndColumn     uint32

	Parent *Node

	children []*Node

	// lazy is the tree the children are read from on demand, if they
	// haven't been read yet (see Inspect).
	lazy       *lazyTree
	index      uint32 // in lazy
	childCount uint32 // to read from lazy
}

// Children returns the node's children: all of them, named or not, in
// order.
func (n *Node) Children() []*Node {
	if n.lazy != nil {
		n.lazy.load(n)
	}
	return n.children
}

// ChildByFieldName returns the first child filling the named field, like
// ts_node_child_by_field_name, or nil.
func (n *Node) ChildByFieldName(name string) *Node {
	for _, c := range n.Children() {
		if c.Field == name {
			return c
		}
	}
	return nil
}

// Text returns the node's source text.
func (n *Node) Text(source []byte) string {
	if int(n.EndByte) > len(source) || n.StartByte > n.EndByte {
		return ""
	}
	return string(source[n.StartByte:n.EndByte])
}

// Prune says which subtrees a parse leaves out, to spare decoding a whole
// tree when only part of it will be read. A pruned node keeps its own
// fields; it only has no Children.
type Prune int

const (
	// PruneNone keeps the whole tree.
	PruneNone Prune = iota

	// PruneErrorFree leaves out the children of nodes with no errors
	// beneath them: all error reporting reads.
	PruneErrorFree
)
