package lsp

import (
	"context"
	"os"
	"path/filepath"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleInitialize(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params InitializeParams
	if err := req.UnmarshalParams(&params); err != nil {
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
			DefinitionProvider:      true,
			HoverProvider:           true,
			RenameProvider:          true,
			WorkspaceSymbolProvider: true,
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
