package lsp

import (
	"context"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleWorkspaceDidChangeWorkspaceFolders(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params DidChangeWorkspaceFoldersParams
	if err := req.UnmarshalParams(&params); err != nil {
		return nil, err
	}

	for _, folder := range params.Event.Added {
		path, err := fromURI(folder.URI)
		if err != nil {
			continue
		}
		h.addFolder(path)
	}

	// TODO: Handle removed folders if needed

	return nil, nil
}
