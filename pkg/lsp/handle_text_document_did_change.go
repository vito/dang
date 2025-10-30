package lsp

import (
	"context"
	"log/slog"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleTextDocumentDidChange(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params DidChangeTextDocumentParams
	if err := req.UnmarshalParams(&params); err != nil {
		return nil, err
	}

	if len(params.ContentChanges) == 0 {
		return nil, nil
	}

	// We use full document sync, so just take the last change which should be the full text
	text := params.ContentChanges[len(params.ContentChanges)-1].Text
	if err := h.updateFile(ctx, params.TextDocument.URI, text, &params.TextDocument.Version); err != nil {
		slog.WarnContext(ctx, "failed to update file on change", "uri", params.TextDocument.URI, "error", err)
	}

	return nil, nil
}
