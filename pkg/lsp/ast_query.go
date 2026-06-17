package lsp

import (
	"github.com/vito/dang/v2/pkg/dang"
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

// positionAfter reports whether p comes strictly after q in a document.
func positionAfter(p, q Position) bool {
	if p.Line != q.Line {
		return p.Line > q.Line
	}
	return p.Character > q.Character
}

// rangesOverlap reports whether two ranges intersect. Touching endpoints count
// as overlapping, so a zero-width cursor at either edge of a range matches.
func rangesOverlap(a, b Range) bool {
	return !positionAfter(a.Start, b.End) && !positionAfter(b.Start, a.End)
}

// findEnclosingEnvironments walks the AST and collects all environments that enclose the given position.
// Returns environments from outermost to innermost.
func findEnclosingEnvironments(root dang.Node, pos Position) []dang.TypeScope {
	var environments []dang.TypeScope

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
		case *dang.ObjectDecl:
			if typed.Inferred != nil {
				environments = append(environments, typed.Inferred)
			}
		case *dang.FunDecl:
			if typed.InferredScope != nil {
				environments = append(environments, typed.InferredScope)
			}
		case *dang.FileBlock:
			if typed.TypeScope != nil {
				environments = append(environments, typed.TypeScope)
			}
		case *dang.Block:
			if typed.TypeScope != nil {
				environments = append(environments, typed.TypeScope)
			}
		case *dang.ObjectLiteral:
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
