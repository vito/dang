package lsp

import (
	"context"

	"github.com/sourcegraph/jsonrpc2"
)

func (h *langHandler) handleShutdown(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	// Close the shared Dagger client
	if h.dag != nil {
		if err := h.dag.Close(); err != nil {
			return nil, err
		}
	}
	
	// Clean up default GraphQL provider connection if it exists
	if h.defaultProvider != nil {
		h.defaultProvider.Close()
	}
	
	return nil, nil
}
