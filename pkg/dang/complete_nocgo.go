//go:build !cgo

package dang

import "context"

// Complete returns completions for the given source text at the given cursor
// position. Without CGo, tree-sitter is not available, so this is a no-op.
func Complete(ctx context.Context, env Env, text string, line, col int) CompletionResult {
	return CompletionResult{}
}
