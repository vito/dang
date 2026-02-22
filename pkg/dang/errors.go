package dang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vito/dang/pkg/hm"
)

// SourceLocation represents a location in source code
type SourceLocation struct {
	Filename string
	Line     int
	Column   int
	Length   int             // Length of the syntax node that caused the error
	End      *SourcePosition // Optional: end position of the node (for range-based operations)
}

// SourcePosition represents a position in source code
type SourcePosition struct {
	Line   int
	Column int
}

// IsWithin checks if this location is within the given range
// Returns true if this location falls within the bounds of the given range
func (loc *SourceLocation) IsWithin(bounds *SourceLocation) bool {
	if loc == nil || bounds == nil {
		return false
	}

	// Check if we're in the same file (or if bounds has no filename, accept any file)
	if bounds.Filename != "" && loc.Filename != bounds.Filename {
		return false
	}

	// Check if we have end position for bounds
	if bounds.End == nil {
		// Can't determine range without end position
		return false
	}

	// Check if location starts before the bounds
	if loc.Line < bounds.Line {
		return false
	}

	// Check if location starts after the bounds end
	if loc.Line > bounds.End.Line {
		return false
	}

	// If on the same line as bounds start, check column
	if loc.Line == bounds.Line && loc.Column < bounds.Column {
		return false
	}

	// If on the same line as bounds end, check column
	if loc.Line == bounds.End.Line && loc.Column > bounds.End.Column {
		return false
	}

	return true
}

// SourceError represents an error with source location information
type SourceError struct {
	Inner    error
	Location *SourceLocation
	Source   string // The source code of the file
}

// NewSourceError creates a new SourceError
func NewSourceError(inner error, location *SourceLocation, source string) *SourceError {
	return &SourceError{
		Inner:    inner,
		Location: location,
		Source:   source,
	}
}

func (e *SourceError) Unwrap() error {
	return e.Inner
}

func (e *SourceError) Error() string {
	if e.Location == nil {
		return e.Inner.Error()
	}

	return e.FormatWithHighlighting()
}

// FormatWithHighlighting returns a nicely formatted error with syntax highlighting
func (e *SourceError) FormatWithHighlighting() string {
	if e.Location == nil && e.Source == "" {
		return e.Inner.Error()
	}

	if e.Source == "" && e.Location.Filename != "" {
		contents, err := os.ReadFile(e.Location.Filename)
		if err == nil {
			e.Source = string(contents)
		}
	}

	lines := strings.Split(e.Source, "\n")
	if e.Location.Line < 1 || e.Location.Line > len(lines) {
		return e.Inner.Error()
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
	result.WriteString(fmt.Sprintf("%s%sError:%s %s\n", bold, red, reset, e.Inner))
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

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
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
func CreateEvalError(ctx context.Context, err error, node SourceLocatable) error {
	if evalCtx := GetEvalContext(ctx); evalCtx != nil {
		return evalCtx.CreateSourceError(err, node)
	}
	return err
}

// InferError represents a type inference error with source location information
type InferError struct {
	Inner    error
	Location *SourceLocation
	Node     any // Keep reference to the AST node for additional context
}

func (e *InferError) Error() string {
	return e.Inner.Error()
}

func (e *InferError) Unwrap() error {
	return e.Inner
}

// NewInferError creates a new InferError with source location from an AST node
func NewInferError(inner error, node SourceLocatable) *InferError {
	var location *SourceLocation
	if node != nil {
		location = node.GetSourceLocation()
	}
	return &InferError{
		Inner:    inner,
		Location: location,
		Node:     node,
	}
}

// WrapInferError wraps an existing error with source location information
func WrapInferError(err error, node SourceLocatable) error {
	var inferErr *InferError
	if errors.As(err, &inferErr) {
		// Already an InferError, don't double-wrap
		return inferErr
	}

	// Create new InferError with the original error's message
	return NewInferError(err, node)
}

// NewEvalContext creates a new evaluation context
func NewEvalContext(filename, source string) *EvalContext {
	return &EvalContext{
		Filename: filename,
		Source:   source,
	}
}

type SourceLocatable interface {
	GetSourceLocation() *SourceLocation
}

// CreateSourceError creates a SourceError from a regular error, trying to extract location info
func (ctx *EvalContext) CreateSourceError(err error, node SourceLocatable) error {
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

	return NewSourceError(err, location, ctx.Source)
}

// ConvertInferError converts an InferError to a SourceError with source context
func ConvertInferError(origErr error) error {
	var inferErrs *InferenceErrors
	if errors.As(origErr, &inferErrs) {
		cp := *inferErrs
		for i, e := range inferErrs.Errors {
			cp.Errors[i] = ConvertInferError(e)
		}
		return &cp
	}
	var inferErr *InferError
	if errors.As(origErr, &inferErr) {
		// Convert InferError to SourceError with full context
		location := inferErr.Location
		if location == nil {
			// Location missing; nothing we can do
			return errors.Join(
				origErr,
				fmt.Errorf("no location info available for node (%T): %#v", inferErr.Node, inferErr.Node),
			)
		}
		if location.Filename != "" {
			source, err := os.ReadFile(location.Filename)
			if err != nil {
				return errors.Join(
					origErr,
					fmt.Errorf("failed to read file %s: %w", location.Filename, err),
				)
			}
			return NewSourceError(inferErr, location, string(source))
		}
	}

	// Not an InferError, handle as regular error
	return origErr
}

// WithInferErrorHandling wraps an Infer method implementation with automatic error handling
func WithInferErrorHandling(node SourceLocatable, fn func() (hm.Type, error)) (hm.Type, error) {
	typ, err := fn()
	if err != nil {
		// Check if error already has source location context
		var inferErr *InferError
		var sourceErr *SourceError
		if errors.As(err, &inferErr) || errors.As(err, &sourceErr) {
			// Already has source location context, preserve it
			return nil, err
		}
		// No source location context, wrap it
		return nil, WrapInferError(err, node)
	}
	return typ, nil
}

// WithEvalErrorHandling wraps an Eval method implementation with automatic error handling
func WithEvalErrorHandling(ctx context.Context, node SourceLocatable, fn func() (Value, error)) (Value, error) {
	val, err := fn()
	if err != nil {
		// Check if error already has source location context
		var sourceErr *SourceError
		var assertionErr *AssertionError
		var raisedErr *RaisedError
		if errors.As(err, &sourceErr) || errors.As(err, &assertionErr) || errors.As(err, &raisedErr) {
			// Already has context or is a user-level raise; preserve it
			return nil, err
		}
		// No source location context, wrap it
		return nil, CreateEvalError(ctx, err, node)
	}
	return val, nil
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
