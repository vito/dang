package lsp

import (
	"context"
	"log/slog"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleTextDocumentDidOpen(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params DidOpenTextDocumentParams
	if err := req.UnmarshalParams(&params); err != nil {
		return nil, err
	}

	if err := h.openFile(params.TextDocument.URI, params.TextDocument.LanguageID, params.TextDocument.Version); err != nil {
		return nil, err
	}
	go func() {
		if err := h.updateFile(ctx, params.TextDocument.URI, params.TextDocument.Text, &params.TextDocument.Version); err != nil {
			slog.WarnContext(ctx, "failed to update file on open", "uri", params.TextDocument.URI, "error", err)
		}
	}()
	return nil, nil
}
