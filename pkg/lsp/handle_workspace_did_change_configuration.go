package lsp

import (
	"context"
	"encoding/json"
)

func (h *langHandler) handleWorkspaceDidChangeConfiguration(ctx context.Context, params json.RawMessage) (any, error) {
	return nil, nil
}
