package lsp

import (
	"context"
	"log/slog"
	"strings"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleWorkspaceSymbol(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params WorkspaceSymbolParams
	if err := req.UnmarshalParams(&params); err != nil {
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
