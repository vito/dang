package lsp

import (
	"context"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleWorkspaceWorkspaceFolders(ctx context.Context, req *jrpc2.Request) (any, error) {
	folders := []WorkspaceFolder{}
	for _, folder := range h.folders {
		folders = append(folders, WorkspaceFolder{
			URI:  toURI(folder),
			Name: folder,
		})
	}
	return folders, nil
}
