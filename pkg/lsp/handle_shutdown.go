package lsp

import (
	"context"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleShutdown(ctx context.Context, req *jrpc2.Request) (any, error) {
	// Service processes are cleaned up via the ServiceRegistry.
	return nil, nil
}
