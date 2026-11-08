package lsp

import (
	"context"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleTextDocumentReferences(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params ReferenceParams
	if err := req.UnmarshalParams(&params); err != nil {
		return nil, err
	}

	f := h.waitForFile(params.TextDocument.URI)
	locs := references(f, params.TextDocument.URI, params.Position)
	if len(locs) == 0 {
		return []Location{}, nil
	}
	if !params.Context.IncludeDeclaration {
		// The declaration comes first.
		locs = locs[1:]
	}
	return locs, nil
}
