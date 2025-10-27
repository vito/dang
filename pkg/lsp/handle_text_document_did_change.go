package lsp

import (
	"github.com/newstack-cloud/ls-builder/common"
	lsp "github.com/newstack-cloud/ls-builder/lsp_3_17"
)

func (h *langHandler) handleTextDocumentDidChange(ctx *common.LSPContext, params *lsp.DidChangeTextDocumentParams) error {
	if len(params.ContentChanges) == 0 {
		return nil
	}

	// We use full document sync, so just take the last change which should be the full text
	// The ContentChanges is []any in lsp_3_17, we need to extract the text
	lastChange := params.ContentChanges[len(params.ContentChanges)-1]

	// Try to extract text from the change event
	var text string
	if changeEvent, ok := lastChange.(lsp.TextDocumentContentChangeEventWhole); ok {
		text = changeEvent.Text
	} else if change, ok := lastChange.(lsp.TextDocumentContentChangeEvent); ok {
		text = change.Text
	}

	version := int(params.TextDocument.Version)
	return h.updateFile(ctx, params.TextDocument.URI, text, &version)
}
