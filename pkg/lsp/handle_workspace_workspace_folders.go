package lsp

import (
	"context"

	"github.com/sourcegraph/jsonrpc2"
)

func (h *langHandler) handleWorkspaceWorkspaceFolders(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	folders := []WorkspaceFolder{}
	for _, folder := range h.folders {
		folders = append(folders, WorkspaceFolder{
			URI:  toURI(folder),
			Name: folder,
		})
	}
	return folders, nil
}
