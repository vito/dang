package lsp

import (
	"context"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleWorkspaceWorkspaceFolders(ctx context.Context, req *jrpc2.Request) (any, error) {
	h.mu.Lock()
	folderPaths := append([]string(nil), h.folders...)
	h.mu.Unlock()

	folders := []WorkspaceFolder{}
	for _, folder := range folderPaths {
		folders = append(folders, WorkspaceFolder{
			URI:  toURI(folder),
			Name: folder,
		})
	}
	return folders, nil
}
