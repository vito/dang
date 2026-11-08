//go:build !cgo

package dang

import "context"

// Complete returns completions for the given source text at the given cursor
// position. Without CGo, tree-sitter is not available, so this is a no-op.
func Complete(ctx context.Context, env TypeScope, text string, line, col int) CompletionResult {
	return CompletionResult{}
}

// CompletionContextAt classifies the cursor for completion. Without CGo,
// tree-sitter is not available, so there is never anything to complete.
func CompletionContextAt(text string, line, col int) (cc *CompletionContext, replaceFrom int) {
	return nil, 0
}
