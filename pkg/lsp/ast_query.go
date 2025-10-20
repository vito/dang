package lsp

import (
	"github.com/vito/dang/pkg/dang"
)

// FindNodeAt returns the most specific AST node at the given position
func FindNodeAt(block *dang.Block, line, col int) dang.Node {
	if block == nil {
		return nil
	}

	var found dang.Node
	walkNodes(block.Forms, func(node dang.Node) bool {
		loc := node.GetSourceLocation()
		if loc == nil {
			return true // continue
		}

		// Check if the cursor position is within this node's location
		if containsPosition(loc, line, col) {
			found = node
			// Continue walking to find more specific (nested) nodes
		}

		return true // always continue walking
	})

	return found
}

// containsPosition checks if a source location contains a line/column position
func containsPosition(loc *dang.SourceLocation, line, col int) bool {
	// LSP uses 0-based line/col, SourceLocation uses 1-based
	dangLine := line + 1
	dangCol := col + 1

	// For now, just check if it's on the same line
	// TODO: Handle multi-line nodes properly
	return loc.Line == dangLine && dangCol >= loc.Column
}

// walkNodes recursively walks all nodes in the AST
func walkNodes(nodes []dang.Node, fn func(dang.Node) bool) {
	for _, node := range nodes {
		if !fn(node) {
			return
		}

		// Recursively walk nested nodes
		switch n := node.(type) {
		case *dang.Block:
			walkNodes(n.Forms, fn)
		case *dang.SlotDecl:
			// Walk into the value of the slot
			if n.Value != nil {
				walkNodes([]dang.Node{n.Value}, fn)
			}
		case *dang.Select:
			if n.Receiver != nil {
				walkNodes([]dang.Node{n.Receiver}, fn)
			}
		case *dang.FunCall:
			walkNodes([]dang.Node{n.Fun}, fn)
			for _, arg := range n.Args {
				walkNodes([]dang.Node{arg.Value}, fn)
			}
		case *dang.Lambda:
			walkNodes([]dang.Node{n.FunctionBase.Body}, fn)
		case *dang.FunDecl:
			walkNodes([]dang.Node{n.FunctionBase.Body}, fn)
		case *dang.Let:
			if n.Value != nil {
				walkNodes([]dang.Node{n.Value}, fn)
			}
		case *dang.Conditional:
			walkNodes([]dang.Node{n.Condition}, fn)
			walkNodes([]dang.Node{n.Then}, fn)
			if n.Else != nil {
				if elseBlock, ok := n.Else.(*dang.Block); ok {
					walkNodes([]dang.Node{elseBlock}, fn)
				}
			}
		case *dang.Index:
			walkNodes([]dang.Node{n.Receiver}, fn)
			walkNodes([]dang.Node{n.Index}, fn)
		case *dang.ObjectSelection:
			walkNodes([]dang.Node{n.Receiver}, fn)
		case *dang.List:
			walkNodes(n.Elements, fn)
		case *dang.Reassignment:
			walkNodes([]dang.Node{n.Target}, fn)
			walkNodes([]dang.Node{n.Value}, fn)
		// Add more cases as needed for other node types
		}
	}
}

// FindReceiverAt finds the receiver expression for a Select node at the cursor
// For "container.withDir", when cursor is after ".", return the "container" Symbol node
func FindReceiverAt(block *dang.Block, line, col int) dang.Node {
	// Find ALL nodes at this position, looking specifically for Select nodes
	var selectNode *dang.Select
	walkNodes(block.Forms, func(node dang.Node) bool {
		loc := node.GetSourceLocation()
		if loc == nil {
			return true
		}

		// Check if this node contains the cursor position
		if containsPosition(loc, line, col) {
			// Check if it's a Select node
			if sel, ok := node.(*dang.Select); ok {
				selectNode = sel
				// Don't return false - continue walking to find the most specific Select
			}
		}

		return true // always continue
	})

	if selectNode != nil {
		return selectNode.Receiver
	}

	return nil
}
