package lsp

import (
	"context"
)

func (h *langHandler) handleWorkspaceDidChangeWorkspaceFolders(ctx context.Context, params DidChangeWorkspaceFoldersParams) (any, error) {
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
