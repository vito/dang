package lsp

import (
	"os"
	"path/filepath"

	"github.com/newstack-cloud/ls-builder/common"
	lsp "github.com/newstack-cloud/ls-builder/lsp_3_17"
)

func (h *langHandler) handleInitialize(ctx *common.LSPContext, params *lsp.InitializeParams) (any, error) {
	if params.RootURI != nil {
		rootPath, err := fromURI(*params.RootURI)
		if err != nil {
			return nil, err
		}
		h.rootPath = filepath.Clean(rootPath)
		h.addFolder(rootPath)
	}

	tdSync := lsp.TextDocumentSyncKindFull
	return lsp.InitializeResult{
		Capabilities: lsp.ServerCapabilities{
			TextDocumentSync: &tdSync,
			CompletionProvider: &lsp.CompletionOptions{
				TriggerCharacters: []string{"."},
			},
			DefinitionProvider:      true,
			HoverProvider:           true,
			RenameProvider:          true,
			WorkspaceSymbolProvider: true,
			Workspace: &lsp.ServerWorkspaceCapabilities{
				WorkspaceFolders: &lsp.WorkspaceFoldersServerCapabilities{
					Supported:           func() *bool { b := true; return &b }(),
					ChangeNotifications: &lsp.BoolOrString{BoolVal: func() *bool { b := true; return &b }()},
				},
			},
		},
	}, nil
}

func (h *langHandler) handleInitialized(ctx *common.LSPContext, params *lsp.InitializedParams) error {
	return nil
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
