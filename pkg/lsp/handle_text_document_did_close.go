package lsp

import (
	"context"
)

func (h *langHandler) handleTextDocumentDidClose(ctx context.Context, params DidCloseTextDocumentParams) (any, error) {
	return nil, h.closeFile(params.TextDocument.URI)
}
