package lsp

import (
	"context"
)

func (h *langHandler) handleTextDocumentDidSave(ctx context.Context, params DidSaveTextDocumentParams) (any, error) {
	return nil, h.saveFile(params.TextDocument.URI)
}
