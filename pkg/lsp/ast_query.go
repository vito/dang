package lsp

import (
	"fmt"
	"log/slog"

	"github.com/vito/dang/pkg/dang"
)

// FindNodeAt returns the most specific AST node at the given position
func FindNodeAt(root dang.Node, line, col int) dang.Node {
	if root == nil {
		return nil
	}

	var found dang.Node
	root.Walk(func(node dang.Node) bool {
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

// positionWithinNode checks if an LSP Position is within a node's source location
func positionWithinNode(node dang.Node, pos Position) bool {
	if node == nil {
		return false
	}

	loc := node.GetSourceLocation()
	if loc == nil {
		return false
	}

	// Convert LSP Position (0-based) to 1-based for comparison with SourceLocation
	startLine := loc.Line - 1
	startCol := loc.Column - 1
	endLine := startLine
	endCol := startCol + loc.Length

	if loc.End != nil {
		endLine = loc.End.Line - 1
		endCol = loc.End.Column - 1
	}

	// Check if position is within this node's range
	return (pos.Line > startLine || (pos.Line == startLine && pos.Character >= startCol)) &&
		(pos.Line < endLine || (pos.Line == endLine && pos.Character <= endCol))
}

// FindReceiverAt finds the receiver expression for a Select node at the cursor
// For "container.withDir", when cursor is after ".", return the "container" Symbol node
func FindReceiverAt(body dang.Node, line, col int) dang.Node {
	// Find ALL nodes at this position, looking specifically for Select nodes
	var selectNode *dang.Select
	body.Walk(func(node dang.Node) bool {
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

// findEnclosingEnvironments walks the AST and collects all environments that enclose the given position.
// Returns environments from outermost to innermost.
func findEnclosingEnvironments(root dang.Node, pos Position) []dang.Env {
	var environments []dang.Env

	root.Walk(func(n dang.Node) bool {
		if n == nil {
			return false
		}

		// Check if position is within this node's range
		if !positionWithinNode(n, pos) {
			return true
		}

		// Check if this node has a stored environment
		switch typed := n.(type) {
		case *dang.ClassDecl:
			if typed.Inferred != nil {
				environments = append(environments, typed.Inferred)
			}
		case *dang.FunDecl:
			if typed.InferredScope != nil {
				environments = append(environments, typed.InferredScope)
			}
		case *dang.ModuleBlock:
			if typed.Env != nil {
				environments = append(environments, typed.Env)
			}
		case *dang.Block:
			if typed.Env != nil {
				environments = append(environments, typed.Env)
			}
		case *dang.Object:
			if typed.Mod != nil {
				environments = append(environments, typed.Mod)
			}
		case *dang.ObjectSelection:
			if typed.Inferred != nil {
				environments = append(environments, typed.Inferred)
			}
		}

		return true
	})

	return environments
}
