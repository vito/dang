package lsp

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/sourcegraph/jsonrpc2"
)

func (h *langHandler) handleInitialize(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	if req.Params == nil {
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams}
	}

	h.conn = conn

	// Initialize a single Dagger client to be shared across all workspaces/modules
	dag, err := dagger.Connect(ctx)
	if err != nil {
		slog.WarnContext(ctx, "failed to connect to Dagger, will retry on demand", "error", err)
		// Don't fail initialization, just log the warning
	} else {
		h.dag = dag
	}

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

	return InitializeResult{
		Capabilities: ServerCapabilities{
			TextDocumentSync: TDSKFull,
			CompletionProvider: &CompletionProvider{
				TriggerCharacters: []string{"."},
			},
			DefinitionProvider: true,
			HoverProvider:      true,
			RenameProvider:     true,
			Workspace: &ServerCapabilitiesWorkspace{
				WorkspaceFolders: WorkspaceFoldersServerCapabilities{
					Supported:           true,
					ChangeNotifications: true,
				},
			},
		},
	}, nil
}

// findDaggerModule searches for a dagger.json file in the given directory or its parents
// Returns the directory containing dagger.json, or empty string if not found
// Stops searching at .git directory
func findDaggerModule(startPath string) string {
	dir, err := filepath.Abs(startPath)
	if err != nil {
		return ""
	}

	for {
		// Check if dagger.json exists in this directory
		daggerJSON := filepath.Join(dir, "dagger.json")
		if _, err := os.Stat(daggerJSON); err == nil {
			return dir
		}

		// Check if we've hit a .git directory - stop searching
		gitDir := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			// We found .git, stop searching
			return ""
		}

		// Stop searching if we're in a testdata directory
		if filepath.Base(dir) == "testdata" {
			return ""
		}

		// Move to parent directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// We've reached the root
			return ""
		}
		dir = parent
	}
}
