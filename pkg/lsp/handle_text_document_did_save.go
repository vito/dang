package lsp

import (
	"context"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleTextDocumentDidSave(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params DidSaveTextDocumentParams
	if err := req.UnmarshalParams(&params); err != nil {
		return nil, err
	}

	return nil, h.saveFile(params.TextDocument.URI)
}
