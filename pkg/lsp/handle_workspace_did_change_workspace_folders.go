package lsp

import (
	"github.com/newstack-cloud/ls-builder/common"
	"github.com/newstack-cloud/ls-builder/lsp_3_17"
)

func (h *langHandler) handleWorkspaceDidChangeWorkspaceFolders(ctx *common.LSPContext, params *lsp.DidChangeWorkspaceFoldersParams) error {
	for _, folder := range params.Event.Added {
		path, err := fromURI(folder.URI)
		if err != nil {
			continue
		}
		h.addFolder(path)
	}

	// TODO: Handle removed folders if needed

	return nil
}
