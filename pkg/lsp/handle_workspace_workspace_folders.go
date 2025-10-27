package lsp

import (
	"github.com/newstack-cloud/ls-builder/common"
	lsp "github.com/newstack-cloud/ls-builder/lsp_3_17"
)

func (h *langHandler) handleWorkspaceWorkspaceFolders(ctx *common.LSPContext) (any, error) {
	folders := []lsp.WorkspaceFolder{}
	for _, folder := range h.folders {
		folders = append(folders, lsp.WorkspaceFolder{
			URI:  toURI(folder),
			Name: folder,
		})
	}
	return folders, nil
}
