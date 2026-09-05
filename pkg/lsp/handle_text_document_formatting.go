package lsp

import (
	"context"
	"fmt"

	jsonrpc "github.com/gumeniukcom/golang-jsonrpc2/v2"
	"github.com/vito/dang/v2/pkg/dang"
)

func (h *langHandler) handleTextDocumentFormatting(ctx context.Context, params DocumentFormattingParams) (any, error) {
	// Wait for file to be fully processed
	f := h.waitForFile(params.TextDocument.URI)
	if f == nil {
		return nil, jsonrpc.NewRPCError(jsonrpc.InvalidParamsErrorCode, fmt.Errorf("document not found: %v", params.TextDocument.URI))
	}

	// Format the file content
	formatted, err := dang.FormatFile([]byte(f.Text))
	if err != nil {
		// If formatting fails (e.g., parse error), return empty edits
		// The parse error will already be shown as a diagnostic
		return []TextEdit{}, nil
	}

	// If no changes, return empty edits
	if formatted == f.Text {
		return []TextEdit{}, nil
	}

	// Count lines in original text to get the full range
	lines := 0
	lastLineLen := 0
	for i, c := range f.Text {
		if c == '\n' {
			lines++
			lastLineLen = 0
		} else {
			lastLineLen = len(f.Text) - i
		}
	}

	// Return a single edit that replaces the entire document
	return []TextEdit{
		{
			Range: Range{
				Start: Position{Line: 0, Character: 0},
				End:   Position{Line: lines, Character: lastLineLen},
			},
			NewText: formatted,
		},
	}, nil
}
