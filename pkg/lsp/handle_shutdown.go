package lsp

import (
	"context"

	"github.com/sourcegraph/jsonrpc2"
)

func (h *langHandler) handleShutdown(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	// Clean up GraphQL provider connection if it exists
	if h.provider != nil {
		h.provider.Close()
	}
	return nil, nil
}
