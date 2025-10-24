package lsp

import (
	"fmt"
	"log/slog"

	"github.com/vito/dang/pkg/dang"
)

// FindNodeAt returns the most specific AST node at the given position
func FindNodeAt(block *dang.Block, line, col int) dang.Node {
	if block == nil {
		return nil
	}

	var found dang.Node
	block.Walk(func(node dang.Node) bool {
		loc := node.GetSourceLocation()
		if loc == nil {
			return true
		}

		// Check if the cursor position is within this node's location
		if containsPosition(loc, line, col) {
			found = node
			// Continue walking into children to find more specific (nested) nodes
			return true
		}
		// Skip walking into children of nodes that don't contain the position
		return false
	})

	return found
}

// containsPosition checks if a source location contains a line/column position
func containsPosition(loc *dang.SourceLocation, line, col int) bool {
	// LSP uses 0-based line/col, SourceLocation uses 1-based
	dangLine := line + 1
	dangCol := col + 1

	// If we don't have an end position, fall back to simple same-line check
	if loc.End == nil {
		return loc.Line == dangLine && dangCol >= loc.Column
	}

	// Check if the position is within the bounds of this node
	// Position must be after or at the start
	if dangLine < loc.Line {
		return false
	}
	if dangLine == loc.Line && dangCol < loc.Column {
		return false
	}

	// Position must be before or at the end
	if dangLine > loc.End.Line {
		return false
	}
	if dangLine == loc.End.Line && dangCol > loc.End.Column {
		return false
	}

	return true
}

// FindReceiverAt finds the receiver expression for a Select node at the cursor
// For "container.withDir", when cursor is after ".", return the "container" Symbol node
func FindReceiverAt(block *dang.Block, line, col int) dang.Node {
	// Find ALL nodes at this position, looking specifically for Select nodes
	var selectNode *dang.Select
	block.Walk(func(node dang.Node) bool {
		loc := node.GetSourceLocation()
		if loc == nil {
			slog.Warn("no source location for node", "want", []int{line, col}, "have", fmt.Sprintf("%T", node))
			return true
		}

		// Check if this node contains the cursor position
		if containsPosition(loc, line, col) {
			// Check if it's a Select node
			if sel, ok := node.(*dang.Select); ok {
				selectNode = sel
				// Continue walking to find the most specific Select
			}
			return true
		}
		// Skip walking into children of nodes that don't contain the position
		return false
	})

	if selectNode != nil {
		return selectNode.Receiver
	}

	return nil
}
