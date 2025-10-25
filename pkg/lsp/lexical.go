package lsp

import (
	"fmt"
	"log/slog"

	"github.com/vito/dang/pkg/dang"
)

// LexicalBinding represents a symbol binding with its valid scope
type LexicalBinding struct {
	Symbol   string
	Location *dang.SourceLocation // Where the symbol is defined
	Bounds   *dang.SourceLocation // Where the symbol is valid (scope range)
	Kind     CompletionItemKind   // Type of symbol (parameter, variable, etc.)
}

// LexicalAnalyzer analyzes code to find lexically scoped bindings
type LexicalAnalyzer struct {
	Bindings []LexicalBinding
}

// NewLexicalAnalyzer creates a new lexical analyzer
func NewLexicalAnalyzer() *LexicalAnalyzer {
	return &LexicalAnalyzer{
		Bindings: []LexicalBinding{},
	}
}

// Analyze walks the AST and collects lexically scoped bindings
func (la *LexicalAnalyzer) Analyze(uri DocumentURI, nodes []dang.Node) {
	for _, node := range nodes {
		la.analyzeNode(uri, node, nil)
	}
}

// analyzeNode recursively analyzes a node to find bindings
func (la *LexicalAnalyzer) analyzeNode(uri DocumentURI, node dang.Node, parentBounds *dang.SourceLocation) {
	if node == nil {
		return
	}

	slog.Debug("lexical: analyzing node", "type", fmt.Sprintf("%T", node))

	switch n := node.(type) {
	case *dang.Lambda:
		// Lambda parameters are scoped to the lambda body
		slog.Debug("lexical: found Lambda")
		la.analyzeLambda(uri, n)

	case *dang.Block:
		// Let bindings in blocks
		slog.Debug("lexical: found Block", "forms", len(n.Forms))
		la.analyzeBlock(uri, n, parentBounds)

	case *dang.ClassDecl:
		// Class members
		slog.Debug("lexical: found ClassDecl", "name", n.Named)
		la.analyzeClass(uri, n)

	case *dang.SlotDecl:
		// Check if this is a function declaration (method)
		slog.Debug("lexical: found SlotDecl", "name", n.Name.Name)
		if funDecl, ok := n.Value.(*dang.FunDecl); ok {
			slog.Debug("lexical: SlotDecl contains FunDecl")
			// Use the SlotDecl's location as bounds since it spans the entire slot including body
			slotLoc := n.GetSourceLocation()
			la.analyzeFunDeclWithBounds(uri, funDecl, slotLoc)
		} else if n.Value != nil {
			// Recursively analyze non-function slot values
			slog.Debug("lexical: SlotDecl contains other value", "type", fmt.Sprintf("%T", n.Value))
			la.analyzeNode(uri, n.Value, parentBounds)
		}

	case *dang.FunDecl:
		// Function declarations (can appear standalone or in slots)
		slog.Debug("lexical: found standalone FunDecl", "name", n.Named)
		la.analyzeFunDecl(uri, n)

	default:
		slog.Debug("lexical: unhandled node type", "type", fmt.Sprintf("%T", node))
	}
}

// analyzeLambda processes lambda parameters
func (la *LexicalAnalyzer) analyzeLambda(uri DocumentURI, lambda *dang.Lambda) {
	// Get the lambda's source location for bounds
	lambdaLoc := lambda.GetSourceLocation()
	if lambdaLoc == nil {
		return
	}

	// The scope bounds for parameters is the entire lambda body
	bodyLoc := lambda.FunctionBase.Body.GetSourceLocation()
	if bodyLoc == nil {
		return
	}

	// For each parameter, create a binding with scope = lambda body
	for _, arg := range lambda.FunctionBase.Args {
		paramLoc := arg.GetSourceLocation()
		if paramLoc == nil {
			// Use lambda location as fallback
			paramLoc = lambdaLoc
		}

		// Create a scope range for the parameter
		// It's valid from the parameter declaration through the end of the lambda body
		bounds := &dang.SourceLocation{
			Filename: lambdaLoc.Filename,
			Line:     lambdaLoc.Line,
			Column:   lambdaLoc.Column,
			End:      bodyLoc.End,
		}

		// If the body doesn't have an end position, try to infer it from the body location
		if bounds.End == nil && bodyLoc.Length > 0 {
			// Approximate end position based on length (single-line assumption)
			bounds.End = &dang.SourcePosition{
				Line:   bodyLoc.Line,
				Column: bodyLoc.Column + bodyLoc.Length,
			}
		}

		la.Bindings = append(la.Bindings, LexicalBinding{
			Symbol:   arg.Name.Name,
			Location: paramLoc,
			Bounds:   bounds,
			Kind:     VariableCompletion, // Parameters are treated as variables
		})
	}

	// Recursively analyze the lambda body
	la.analyzeNode(uri, lambda.FunctionBase.Body, nil)
}

// analyzeBlock processes block forms
func (la *LexicalAnalyzer) analyzeBlock(uri DocumentURI, block *dang.Block, parentBounds *dang.SourceLocation) {
	// For now, just recursively analyze each form
	// TODO: Handle let bindings with proper scoping
	for _, form := range block.Forms {
		la.analyzeNode(uri, form, parentBounds)
	}
}

// analyzeClass processes class declarations
func (la *LexicalAnalyzer) analyzeClass(uri DocumentURI, class *dang.ClassDecl) {
	// Class members are scoped to the class body
	// Recursively analyze each form in the class body
	for _, form := range class.Value.Forms {
		la.analyzeNode(uri, form, nil)
	}
}

// analyzeFunDecl processes function declarations (including methods)
func (la *LexicalAnalyzer) analyzeFunDecl(uri DocumentURI, funDecl *dang.FunDecl) {
	la.analyzeFunDeclWithBounds(uri, funDecl, nil)
}

// analyzeFunDeclWithBounds processes function declarations with optional explicit bounds
func (la *LexicalAnalyzer) analyzeFunDeclWithBounds(uri DocumentURI, funDecl *dang.FunDecl, explicitBounds *dang.SourceLocation) {
	slog.Debug("lexical: analyzeFunDecl start", "name", funDecl.Named, "args", len(funDecl.Args))

	// Use explicit bounds if provided, otherwise use function's own location
	var boundsLoc *dang.SourceLocation
	if explicitBounds != nil {
		boundsLoc = explicitBounds
		slog.Debug("lexical: using explicit bounds from parent", "line", boundsLoc.Line, "col", boundsLoc.Column)
	} else {
		boundsLoc = funDecl.GetSourceLocation()
		if boundsLoc == nil {
			slog.Warn("lexical: funDecl has no location")
			return
		}
		slog.Debug("lexical: funLoc", "line", boundsLoc.Line, "col", boundsLoc.Column)
	}

	// The scope bounds for parameters is the entire function body
	// Access the Body field directly from FunctionBase
	bodyNode := funDecl.FunctionBase.Body
	if bodyNode == nil {
		slog.Warn("lexical: funDecl has no body")
		return
	}

	bodyLoc := bodyNode.GetSourceLocation()
	if bodyLoc == nil {
		slog.Warn("lexical: body has no location")
		return
	}
	slog.Debug("lexical: bodyLoc", "line", bodyLoc.Line, "col", bodyLoc.Column, "length", bodyLoc.Length, "hasEnd", bodyLoc.End != nil)

	// For each parameter, create a binding with scope = function body
	for _, arg := range funDecl.Args {
		paramLoc := arg.GetSourceLocation()
		if paramLoc == nil {
			// Use function location as fallback
			paramLoc = boundsLoc
		}

		// Create a scope range for the parameter
		// It's valid from the parameter declaration through the end of the function
		bounds := &dang.SourceLocation{
			Filename: boundsLoc.Filename,
			Line:     boundsLoc.Line,
			Column:   boundsLoc.Column,
			End:      boundsLoc.End,
		}

		// If we don't have an end position, we can't determine scope
		if bounds.End == nil {
			slog.Warn("lexical: no End position available, skipping parameter binding", "symbol", arg.Name.Name)
			continue
		}

		slog.Debug("lexical: adding parameter binding",
			"symbol", arg.Name.Name,
			"boundsStart", slog.GroupValue(
				slog.Int("line", bounds.Line),
				slog.Int("col", bounds.Column),
			),
			"boundsEnd", func() slog.Attr {
				if bounds.End != nil {
					return slog.Group("end",
						slog.Int("line", bounds.End.Line),
						slog.Int("col", bounds.End.Column),
					)
				}
				return slog.String("end", "nil")
			}(),
		)

		la.Bindings = append(la.Bindings, LexicalBinding{
			Symbol:   arg.Name.Name,
			Location: paramLoc,
			Bounds:   bounds,
			Kind:     VariableCompletion, // Parameters are treated as variables
		})
	}

	slog.Debug("lexical: total bindings after analyzeFunDecl", "count", len(la.Bindings))

	// Recursively analyze the function body
	la.analyzeNode(uri, bodyNode, nil)
}

// FindBindingsAt returns bindings that are valid at the given position
func (la *LexicalAnalyzer) FindBindingsAt(pos *dang.SourceLocation) []LexicalBinding {
	var result []LexicalBinding

	for _, binding := range la.Bindings {
		// Check if the position is within the binding's valid scope
		if pos.IsWithin(binding.Bounds) {
			result = append(result, binding)
		}
	}

	return result
}
