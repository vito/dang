package lsp

import (
	"github.com/newstack-cloud/ls-builder/common"
	"github.com/newstack-cloud/ls-builder/lsp_3_17"
)

func (h *langHandler) handleTextDocumentDidOpen(ctx *common.LSPContext, params *lsp.DidOpenTextDocumentParams) error {
	if err := h.openFile(params.TextDocument.URI, params.TextDocument.LanguageID, int(params.TextDocument.Version)); err != nil {
		return err
	}
	version := int(params.TextDocument.Version)
	if err := h.updateFile(ctx, params.TextDocument.URI, params.TextDocument.Text, &version); err != nil {
		return err
	}
	return nil
}
