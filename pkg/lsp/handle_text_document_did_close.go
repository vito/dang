package lsp

import (
	"context"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleTextDocumentDidClose(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params DidCloseTextDocumentParams
	if err := req.UnmarshalParams(&params); err != nil {
		return nil, err
	}

	return nil, h.closeFile(params.TextDocument.URI)
}
