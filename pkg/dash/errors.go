package dash

import (
	"context"
	"fmt"
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
	result.WriteString(fmt.Sprintf("   %s|%s\n", dim, reset))

	// Show context lines
	startLine := max(1, e.Location.Line-2)
	endLine := min(len(lines), e.Location.Line+2)

	for i := startLine; i <= endLine; i++ {
		lineStr := fmt.Sprintf("%d", i)
		if i == e.Location.Line {
			// Highlight the error line
			result.WriteString(fmt.Sprintf("%s%s%s | %s%s%s\n",
				blue, bold, padLeft(lineStr, 3), reset, lines[i-1], reset))

			// Add underline for the specific error location
			padding := strings.Repeat(" ", len(padLeft(lineStr, 3))+3+e.Location.Column-1)
			underline := strings.Repeat("^", max(1, e.Location.Length))
			result.WriteString(fmt.Sprintf("%s%s%s %s%s%s\n",
				dim, padding, reset, red, underline, reset))
		} else {
			// Context lines
			result.WriteString(fmt.Sprintf("%s%s | %s%s\n",
				dim, padLeft(lineStr, 3), lines[i-1], reset))
		}
	}

	result.WriteString(fmt.Sprintf("   %s|%s\n", dim, reset))

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

// NewEvalContext creates a new evaluation context
func NewEvalContext(filename, source string) *EvalContext {
	return &EvalContext{
		Filename: filename,
		Source:   source,
	}
}

// CreateSourceError creates a SourceError from a regular error, trying to extract location info
func (ctx *EvalContext) CreateSourceError(err error, node Node) error {
	if sourceErr, ok := err.(*SourceError); ok {
		return sourceErr
	}

	// Use the actual source location from the AST node
	var location *SourceLocation
	if node != nil {
		if nodeLoc := node.GetSourceLocation(); nodeLoc != nil {
			location = &SourceLocation{
				Filename: ctx.Filename,
				Line:     nodeLoc.Line,
				Column:   nodeLoc.Column,
				Length:   nodeLoc.Length,
			}
		}
	}

	// Only use guessing as a last resort if we don't have any location info
	if location == nil {
		line, column, length := ctx.guessLocation(err, node)
		location = &SourceLocation{
			Filename: ctx.Filename,
			Line:     line,
			Column:   column,
			Length:   length,
		}
	}

	return NewSourceError(err.Error(), location, ctx.Source)
}

// guessLocation tries to guess the source location based on error patterns
func (ctx *EvalContext) guessLocation(err error, node Node) (line, column, length int) {
	errMsg := err.Error()
	lines := strings.Split(ctx.Source, "\n")

	// Try to find patterns in the source that match the error
	switch {
	case strings.Contains(errMsg, "Select.Eval"):
		// Look for field selection patterns
		if strings.Contains(errMsg, "cannot select field") {
			// Extract the field name from the error message
			var fieldName string
			if start := strings.Index(errMsg, `"`); start >= 0 {
				start++ // Move past the opening quote
				if end := strings.Index(errMsg[start:], `"`); end > 0 {
					fieldName = errMsg[start : start+end]
				}
			}

			// Try to find the specific field selection in the source
			for i, sourceLine := range lines {
				if fieldName != "" {
					// Look for the specific field being accessed
					pattern := "." + fieldName
					if idx := strings.Index(sourceLine, pattern); idx != -1 {
						return i + 1, idx + 1, len(pattern) // Point to the dot and field name
					}
				} else if strings.Contains(sourceLine, ".") {
					// Fallback: find any field access
					if idx := strings.Index(sourceLine, "."); idx != -1 {
						return i + 1, idx + 1, 1
					}
				}
			}
		}
	case strings.Contains(errMsg, "not found in env"):
		// Look for symbol references
		for i, sourceLine := range lines {
			// This is a simple heuristic - look for the first non-empty line
			if strings.TrimSpace(sourceLine) != "" {
				return i + 1, 1, len(strings.TrimSpace(sourceLine))
			}
		}
	}

	// Default: return the first line
	if len(lines) > 0 {
		return 1, 1, len(lines[0])
	}
	return 1, 1, 1
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
