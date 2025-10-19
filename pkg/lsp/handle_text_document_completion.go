package lsp

import (
	"context"
	"encoding/json"

	"github.com/sourcegraph/jsonrpc2"
)

func (h *langHandler) handleTextDocumentCompletion(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	if req.Params == nil {
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams}
	}

	var params CompletionParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		return nil, err
	}

	f, ok := h.files[params.TextDocument.URI]
	if !ok {
		return []CompletionItem{}, nil
	}

	var items []CompletionItem

	// Add all defined symbols from the current file
	for name, info := range f.Symbols.Definitions {
		items = append(items, CompletionItem{
			Label: name,
			Kind:  info.Kind,
		})
	}

	return items, nil
}
