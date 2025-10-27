package lsp

import (
	"log/slog"
	"strings"

	"github.com/newstack-cloud/ls-builder/common"
	"github.com/newstack-cloud/ls-builder/lsp_3_17"
)

func (h *langHandler) handleWorkspaceSymbol(ctx *common.LSPContext, params *lsp.WorkspaceSymbolParams) (any, error) {
	slog.InfoContext(ctx.Context, "workspace symbol request", "query", params.Query)

	var symbols []lsp.SymbolInformation

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
				symbols = append(symbols, lsp.SymbolInformation{
					Name:     name,
					Kind:     lsp.SymbolKind(info.Kind),
					Location: *info.Location,
				})
			}
		}

		slog.InfoContext(ctx.Context, "searched file", "uri", uri, "definitions", len(file.Symbols.Definitions), "matches", len(symbols))
	}

	slog.InfoContext(ctx.Context, "workspace symbol results", "query", params.Query, "total", len(symbols))

	return symbols, nil
}
