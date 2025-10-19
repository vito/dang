package lsp

import (
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

	switch n := node.(type) {
	case *dang.Lambda:
		// Lambda parameters are scoped to the lambda body
		la.analyzeLambda(uri, n)

	case dang.Block:
		// Let bindings in blocks
		la.analyzeBlock(uri, n, parentBounds)

	case *dang.ClassDecl:
		// Class members
		la.analyzeClass(uri, n)

	case dang.SlotDecl:
		// Recursively analyze slot values
		if n.Value != nil {
			la.analyzeNode(uri, n.Value, parentBounds)
		}
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
			Symbol:   arg.Named,
			Location: paramLoc,
			Bounds:   bounds,
			Kind:     VariableCompletion, // Parameters are treated as variables
		})
	}

	// Recursively analyze the lambda body
	la.analyzeNode(uri, lambda.FunctionBase.Body, nil)
}

// analyzeBlock processes block forms
func (la *LexicalAnalyzer) analyzeBlock(uri DocumentURI, block dang.Block, parentBounds *dang.SourceLocation) {
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
	slog.Debug("lexical: analyzeFunDecl start", "name", funDecl.Named, "args", len(funDecl.Args))

	// Get the function's source location for bounds
	funLoc := funDecl.GetSourceLocation()
	if funLoc == nil {
		slog.Warn("lexical: funDecl has no location")
		return
	}
	slog.Debug("lexical: funLoc", "line", funLoc.Line, "col", funLoc.Column)

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
			paramLoc = funLoc
		}

		// Create a scope range for the parameter
		// It's valid from the parameter declaration through the end of the function body
		bounds := &dang.SourceLocation{
			Filename: funLoc.Filename,
			Line:     funLoc.Line,
			Column:   funLoc.Column,
			End:      bodyLoc.End,
		}

		// If the body doesn't have an end position, try to infer it from the body location
		if bounds.End == nil && bodyLoc.Length > 0 {
			// Approximate end position based on length (single-line assumption)
			bounds.End = &dang.SourcePosition{
				Line:   bodyLoc.Line,
				Column: bodyLoc.Column + bodyLoc.Length,
			}
			slog.Debug("lexical: created inferred End position", "line", bounds.End.Line, "col", bounds.End.Column)
		}

		slog.Debug("lexical: adding parameter binding", 
			"symbol", arg.Named,
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
			Symbol:   arg.Named,
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
