package lsp

import (
	"github.com/newstack-cloud/ls-builder/common"
)

func (h *langHandler) handleShutdown(ctx *common.LSPContext) error {
	// Close the shared Dagger client
	if h.dag != nil {
		if err := h.dag.Close(); err != nil {
			return err
		}
	}
	
	// Clean up default GraphQL provider connection if it exists
	if h.defaultProvider != nil {
		h.defaultProvider.Close()
	}
	
	return nil
}
