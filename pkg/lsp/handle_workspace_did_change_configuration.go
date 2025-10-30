package lsp

import (
	"context"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleWorkspaceDidChangeConfiguration(ctx context.Context, req *jrpc2.Request) (any, error) {
	return nil, nil
}
