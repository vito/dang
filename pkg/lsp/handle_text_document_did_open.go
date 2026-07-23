package lsp

import (
	"context"
	"log/slog"
)

func (h *langHandler) handleTextDocumentDidOpen(ctx context.Context, params DidOpenTextDocumentParams) (any, error) {
	if err := h.openFile(params.TextDocument.URI, params.TextDocument.LanguageID, params.TextDocument.Version); err != nil {
		return nil, err
	}
	if err := h.updateFile(ctx, params.TextDocument.URI, params.TextDocument.Text, &params.TextDocument.Version); err != nil {
		slog.WarnContext(ctx, "failed to update file on open", "uri", params.TextDocument.URI, "error", err)
	}
	return nil, nil
}
