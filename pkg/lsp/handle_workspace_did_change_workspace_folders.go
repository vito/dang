package lsp

import (
	"context"
	"encoding/json"

	"github.com/sourcegraph/jsonrpc2"
)

func (h *langHandler) handleWorkspaceDidChangeWorkspaceFolders(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	if req.Params == nil {
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams}
	}

	var params DidChangeWorkspaceFoldersParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
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
