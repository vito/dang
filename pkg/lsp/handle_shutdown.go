package lsp

import (
	"context"
	"encoding/json"
)

func (h *langHandler) handleShutdown(ctx context.Context, params json.RawMessage) (any, error) {
	// Service processes are cleaned up via the ServiceRegistry.
	return nil, nil
}
