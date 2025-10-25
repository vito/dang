package lsp

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/sourcegraph/jsonrpc2"
)

func (h *langHandler) handleWorkspaceSymbol(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	if req.Params == nil {
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams}
	}

	var params WorkspaceSymbolParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		return nil, err
	}

	slog.InfoContext(ctx, "workspace symbol request", "query", params.Query)

	var symbols []SymbolInformation

	// Search all open files for symbols matching the query
	query := strings.ToLower(params.Query)
	for uri, file := range h.files {
		if file.Symbols == nil {
			continue
		}

		// Search through all defined symbols in this file
		for name, info := range file.Symbols.Definitions {
			// Fuzzy match: check if query appears anywhere in the symbol name (case-insensitive)
			if query == "" || strings.Contains(strings.ToLower(name), query) {
				symbols = append(symbols, SymbolInformation{
					Name:     name,
					Kind:     int64(info.Kind),
					Location: *info.Location,
				})
			}
		}

		slog.InfoContext(ctx, "searched file", "uri", uri, "definitions", len(file.Symbols.Definitions), "matches", len(symbols))
	}

	slog.InfoContext(ctx, "workspace symbol results", "query", params.Query, "total", len(symbols))

	return symbols, nil
}
