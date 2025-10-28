package lsp

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/sourcegraph/jsonrpc2"
)

func (h *langHandler) handleTextDocumentDidChange(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	if req.Params == nil {
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams}
	}

	var params DidChangeTextDocumentParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		return nil, err
	}

	if len(params.ContentChanges) == 0 {
		return nil, nil
	}

	// We use full document sync, so just take the last change which should be the full text
	text := params.ContentChanges[len(params.ContentChanges)-1].Text
	go func() {
		if err := h.updateFile(ctx, params.TextDocument.URI, text, &params.TextDocument.Version); err != nil {
			slog.WarnContext(ctx, "failed to update file on change", "uri", params.TextDocument.URI, "error", err)
		}
	}()

	return nil, nil
}
