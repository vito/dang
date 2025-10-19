package lsp

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/vito/dang/pkg/dang"
)

func (h *langHandler) handleInitialize(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	if req.Params == nil {
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams}
	}

	h.conn = conn

	var params InitializeParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		return nil, err
	}

	rootPath, err := fromURI(params.RootURI)
	if err != nil {
		return nil, err
	}
	h.rootPath = filepath.Clean(rootPath)
	h.addFolder(rootPath)

	// Initialize GraphQL schema
	if err := h.initializeSchema(ctx); err != nil {
		slog.WarnContext(ctx, "failed to initialize GraphQL schema", "error", err)
		// Don't fail initialization if schema loading fails - LSP can still provide basic features
	}

	return InitializeResult{
		Capabilities: ServerCapabilities{
			TextDocumentSync: TDSKFull,
			CompletionProvider: &CompletionProvider{
				TriggerCharacters: []string{"."},
			},
			DefinitionProvider: true,
			HoverProvider:      true,
			Workspace: &ServerCapabilitiesWorkspace{
				WorkspaceFolders: WorkspaceFoldersServerCapabilities{
					Supported:           true,
					ChangeNotifications: true,
				},
			},
		},
	}, nil
}

// initializeSchema loads the GraphQL schema for use in LSP features
func (h *langHandler) initializeSchema(ctx context.Context) error {
	config := dang.LoadGraphQLConfig()
	h.provider = dang.NewGraphQLClientProvider(config)

	client, schema, err := h.provider.GetClientAndSchema(ctx)
	if err != nil {
		return err
	}

	h.client = client
	h.schema = schema

	slog.InfoContext(ctx, "loaded GraphQL schema", "types", len(schema.Types))
	return nil
}
