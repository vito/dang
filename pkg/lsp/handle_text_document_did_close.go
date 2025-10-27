package lsp

import (
	"github.com/newstack-cloud/ls-builder/common"
	"github.com/newstack-cloud/ls-builder/lsp_3_17"
)

func (h *langHandler) handleTextDocumentDidClose(ctx *common.LSPContext, params *lsp.DidCloseTextDocumentParams) error {
	return h.closeFile(params.TextDocument.URI)
}
