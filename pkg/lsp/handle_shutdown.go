package lsp

import (
	"context"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleShutdown(ctx context.Context, req *jrpc2.Request) (any, error) {
	// Close the shared Dagger client
	if h.cachedDag != nil {
		if err := h.cachedDag.Close(); err != nil {
			return nil, err
		}
	}

	// Clean up default GraphQL provider connection if it exists
	if h.defaultProvider != nil {
		_ = h.defaultProvider.Close()
	}

	return nil, nil
}
