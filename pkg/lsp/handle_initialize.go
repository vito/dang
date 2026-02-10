package lsp

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
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
			DefinitionProvider:         true,
			HoverProvider:              true,
			RenameProvider:             true,
			WorkspaceSymbolProvider:    true,
			DocumentFormattingProvider: true,
			Workspace: &ServerCapabilitiesWorkspace{
				WorkspaceFolders: WorkspaceFoldersServerCapabilities{
					Supported:           true,
					ChangeNotifications: true,
				},
			},
		},
	}, nil
}

// loadEnvrc loads environment variables from a .envrc found alongside a
// dang.toml. It runs `direnv export json` to safely evaluate the .envrc,
// which only succeeds if the user has run `direnv allow`. This lets ${VAR}
// expansion in dang.toml work even when the editor doesn't inherit the
// direnv environment. Results are cached per directory.
func (h *langHandler) loadEnvrc(ctx context.Context, configDir string) {
	if h.loadedEnvDirs[configDir] {
		return
	}
	h.loadedEnvDirs[configDir] = true

	envrcPath := filepath.Join(configDir, ".envrc")
	if _, err := os.Stat(envrcPath); err != nil {
		return
	}

	// Check that direnv is installed.
	direnvPath, err := exec.LookPath("direnv")
	if err != nil {
		slog.InfoContext(ctx, ".envrc found but direnv is not installed, skipping", "dir", configDir)
		return
	}

	slog.InfoContext(ctx, "loading .envrc via direnv", "dir", configDir)

	cmd := exec.CommandContext(ctx, direnvPath, "export", "json")
	cmd.Dir = configDir
	output, err := cmd.Output()
	if err != nil {
		// direnv exits non-zero when .envrc is not allowed (user hasn't
		// run `direnv allow`), or if the .envrc itself errors. Either way
		// we silently skip — the user knows they need to allow it.
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		slog.InfoContext(ctx, "direnv export json failed (not allowed?), skipping .envrc", "dir", configDir, "error", err, "stderr", stderr)
		return
	}

	if len(output) == 0 {
		// direnv outputs nothing when the environment is already up to date.
		return
	}

	var envVars map[string]*string
	if err := json.Unmarshal(output, &envVars); err != nil {
		slog.WarnContext(ctx, "failed to parse direnv output", "error", err)
		return
	}

	loaded := 0
	for k, v := range envVars {
		if v == nil {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, *v)
			loaded++
		}
	}

	slog.InfoContext(ctx, "loaded environment from .envrc", "dir", configDir, "vars", loaded)
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
		notDaggerPath := filepath.Join(dir, ".not-dagger")
		if _, err := os.Stat(notDaggerPath); err == nil {
			// Custom marker for e.g. test specimens to explicitly say they're not a
			// Dagger module
			//
			// There's probably a better idea in the future
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
