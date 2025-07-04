package bind

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// SourceLocation represents a location in source code
type SourceLocation struct {
	Filename string
	Line     int
	Column   int
	Length   int // Length of the syntax node that caused the error
}

// SourceError represents an error with source location information
type SourceError struct {
	Message  string
	Location *SourceLocation
	Source   string // The source code of the file
}

func (e *SourceError) Error() string {
	if e.Location == nil {
		return e.Message
	}

	return e.FormatWithHighlighting()
}

// FormatWithHighlighting returns a nicely formatted error with syntax highlighting
func (e *SourceError) FormatWithHighlighting() string {
	if e.Location == nil || e.Source == "" {
		return e.Message
	}

	lines := strings.Split(e.Source, "\n")
	if e.Location.Line < 1 || e.Location.Line > len(lines) {
		return e.Message
	}

	// Colors for terminal output
	const (
		red    = "\033[31m"
		yellow = "\033[33m"
		blue   = "\033[34m"
		bold   = "\033[1m"
		reset  = "\033[0m"
		dim    = "\033[2m"
	)

	var result strings.Builder

	// Error header
	result.WriteString(fmt.Sprintf("%s%sError:%s %s\n", bold, red, reset, e.Message))
	result.WriteString(fmt.Sprintf("  %s%s--> %s:%d:%d%s\n", dim, blue, e.Location.Filename, e.Location.Line, e.Location.Column, reset))

	// Top separator pipe (aligned with line numbers)
	result.WriteString(fmt.Sprintf(" %s%s |%s\n", dim, padLeft("", 3), reset))

	// Show context lines
	startLine := max(1, e.Location.Line-2)
	endLine := min(len(lines), e.Location.Line+2)

	for i := startLine; i <= endLine; i++ {
		lineStr := fmt.Sprintf("%d", i)
		paddedLineStr := padLeft(lineStr, 3)
		if i == e.Location.Line {
			// Highlight the error line
			result.WriteString(fmt.Sprintf(" %s%s%s%s | %s%s\n",
				dim, blue, bold, paddedLineStr, reset, lines[i-1]))

			// Add underline for the specific error location
			// Calculate padding: 1 space + 3 for line number + " | " (3 chars) + column position - 1
			padding := strings.Repeat(" ", 1+3+3+e.Location.Column-1)
			underline := strings.Repeat("^", max(1, e.Location.Length))
			result.WriteString(fmt.Sprintf("%s%s%s%s%s\n",
				dim, padding, red, underline, reset))
		} else {
			// Context lines
			result.WriteString(fmt.Sprintf(" %s%s | %s%s\n",
				dim, paddedLineStr, lines[i-1], reset))
		}
	}

	// Bottom separator pipe (aligned with line numbers)
	result.WriteString(fmt.Sprintf(" %s%s |%s\n", dim, padLeft("", 3), reset))

	return result.String()
}

// Helper functions
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// NewSourceError creates a new SourceError
func NewSourceError(message string, location *SourceLocation, source string) *SourceError {
	return &SourceError{
		Message:  message,
		Location: location,
		Source:   source,
	}
}

// EvalContext carries evaluation context including source information
type EvalContext struct {
	Filename string
	Source   string
}

// Context key for storing EvalContext in Go context
type evalContextKey struct{}

// WithEvalContext stores the EvalContext in the Go context
func WithEvalContext(ctx context.Context, evalCtx *EvalContext) context.Context {
	return context.WithValue(ctx, evalContextKey{}, evalCtx)
}

// GetEvalContext retrieves the EvalContext from the Go context
func GetEvalContext(ctx context.Context) *EvalContext {
	if evalCtx, ok := ctx.Value(evalContextKey{}).(*EvalContext); ok {
		return evalCtx
	}
	return nil
}

// CreateEvalError creates a source error from within an evaluator
func CreateEvalError(ctx context.Context, err error, node Node) error {
	if evalCtx := GetEvalContext(ctx); evalCtx != nil {
		return evalCtx.CreateSourceError(err, node)
	}
	return err
}

// InferError represents a type inference error with source location information
type InferError struct {
	Message  string
	Location *SourceLocation
	Node     Node // Keep reference to the AST node for additional context
}

func (e *InferError) Error() string {
	return e.Message
}

// NewInferError creates a new InferError with source location from an AST node
func NewInferError(message string, node Node) *InferError {
	var location *SourceLocation
	if node != nil {
		location = node.GetSourceLocation()
	}
	return &InferError{
		Message:  message,
		Location: location,
		Node:     node,
	}
}

// WrapInferError wraps an existing error with source location information
func WrapInferError(err error, node Node) error {
	var inferErr *InferError
	if errors.As(err, &inferErr) {
		// Already an InferError, don't double-wrap
		return inferErr
	}

	// Create new InferError with the original error's message
	return NewInferError(err.Error(), node)
}

// NewEvalContext creates a new evaluation context
func NewEvalContext(filename, source string) *EvalContext {
	return &EvalContext{
		Filename: filename,
		Source:   source,
	}
}

// CreateSourceError creates a SourceError from a regular error, trying to extract location info
func (ctx *EvalContext) CreateSourceError(err error, node Node) error {
	var sourceErr *SourceError
	if errors.As(err, &sourceErr) {
		return sourceErr
	}

	// Use the actual source location from the AST node
	location := node.GetSourceLocation()
	if location == nil {
		// No location info; give up
		return err
	}

	return NewSourceError(err.Error(), location, ctx.Source)
}

// ConvertInferError converts an InferError to a SourceError with source context
func ConvertInferError(origErr error) error {
	var inferErr *InferError
	if errors.As(origErr, &inferErr) {
		// Convert InferError to SourceError with full context
		location := inferErr.Location
		if location == nil {
			// Location missing; nothing we can do
			return errors.Join(
				origErr,
				fmt.Errorf("No location info available for node (%T): %#v", inferErr.Node, inferErr.Node),
			)
		}
		source, err := os.ReadFile(location.Filename)
		if err != nil {
			return errors.Join(
				origErr,
				fmt.Errorf("Failed to read file %s: %w", location.Filename, err),
			)
		}
		return NewSourceError(inferErr.Message, location, string(source))
	}

	// Not an InferError, handle as regular error
	return origErr
}

// AssertionError represents a failed assertion with detailed information
type AssertionError struct {
	Message  string
	Location *SourceLocation
}

func (e *AssertionError) Error() string {
	if e.Location == nil {
		return e.Message
	}
	return fmt.Sprintf("%s\n  Location: %s:%d:%d", e.Message, e.Location.Filename, e.Location.Line, e.Location.Column)
}
