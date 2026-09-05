package lsp

import (
	"context"
	"encoding/json"
)

func (h *langHandler) handleWorkspaceWorkspaceFolders(ctx context.Context, params json.RawMessage) (any, error) {
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
