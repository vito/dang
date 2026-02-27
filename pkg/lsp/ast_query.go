package lsp

import (
	"github.com/vito/dang/pkg/dang"
)

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
