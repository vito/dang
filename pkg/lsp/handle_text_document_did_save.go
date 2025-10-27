package lsp

import (
	"github.com/newstack-cloud/ls-builder/common"
	"github.com/newstack-cloud/ls-builder/lsp_3_17"
)

func (h *langHandler) handleTextDocumentDidSave(ctx *common.LSPContext, params *lsp.DidSaveTextDocumentParams) error {
	return h.saveFile(params.TextDocument.URI)
}
