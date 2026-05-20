package lsp_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/lsp"
)

type lspHarness struct {
	client      *jrpc2.Client
	server      *jrpc2.Server
	diagnostics chan lsp.PublishDiagnosticsParams
	notifyErrs  chan error
}

func newLSPHarness(ctx context.Context, t *testctx.T, root string) *lspHarness {
	t.Helper()

	clientCh, serverCh := channel.Direct()

	h := &lspHarness{
		diagnostics: make(chan lsp.PublishDiagnosticsParams, 16),
		notifyErrs:  make(chan error, 16),
	}

	services := &dang.ServiceRegistry{}
	ctx = dang.ContextWithServices(ctx, services)
	t.Cleanup(services.StopAll)

	handler := lsp.NewHandler(ctx)
	h.server = jrpc2.NewServer(handler, &jrpc2.ServerOptions{
		AllowPush: true,
		Logger: func(text string) {
			if testing.Verbose() {
				t.Logf("lsp server: %s", text)
			}
		},
	})
	handler.SetServer(h.server)
	h.server.Start(serverCh)

	h.client = jrpc2.NewClient(clientCh, &jrpc2.ClientOptions{
		Logger: func(text string) {
			if testing.Verbose() {
				t.Logf("lsp client: %s", text)
			}
		},
		OnNotify: func(req *jrpc2.Request) {
			if req.Method() != "textDocument/publishDiagnostics" {
				return
			}

			var params lsp.PublishDiagnosticsParams
			if err := req.UnmarshalParams(&params); err != nil {
				h.notifyErrs <- fmt.Errorf("unmarshal %s: %w", req.Method(), err)
				return
			}
			h.diagnostics <- params
		},
	})

	t.Cleanup(func() {
		if err := h.client.Close(); err != nil {
			t.Logf("closing LSP client: %v", err)
		}
		h.server.Stop()
		if err := h.server.Wait(); err != nil && !channel.IsErrClosing(err) {
			t.Logf("LSP server stopped: %v", err)
		}
	})

	var initResult lsp.InitializeResult
	require.NoError(t, h.client.CallResult(ctx, "initialize", lsp.InitializeParams{
		RootURI: fileURI(t, root),
	}, &initResult))
	require.NoError(t, h.client.Notify(ctx, "initialized", map[string]any{}))

	return h
}

func (h *lspHarness) open(ctx context.Context, t *testctx.T, path string) lsp.DocumentURI {
	t.Helper()

	contents, err := os.ReadFile(path)
	require.NoError(t, err)

	uri := fileURI(t, path)
	require.NoError(t, h.client.Notify(ctx, "textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        uri,
			LanguageID: "dang",
			Version:    1,
			Text:       string(contents),
		},
	}))
	return uri
}

func (h *lspHarness) waitForDiagnostics(ctx context.Context, t *testctx.T, uri lsp.DocumentURI) []lsp.Diagnostic {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	for {
		select {
		case params := <-h.diagnostics:
			if params.URI == uri {
				return params.Diagnostics
			}
		case err := <-h.notifyErrs:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatalf("timed out waiting for diagnostics for %s", uri)
		}
	}
}

func (LSPSuite) TestDiagnosticsReportsFileErrors(ctx context.Context, t *testctx.T) {
	root := filepath.Join("testdata")
	mainPath := filepath.Join(root, "diagnostics.dang")

	h := newLSPHarness(ctx, t, root)
	uri := h.open(ctx, t, mainPath)
	diagnostics := h.waitForDiagnostics(ctx, t, uri)

	require.Len(t, diagnostics, 2)
	requireDiagnosticMessages(t, diagnostics,
		`cannot use Int! as String!`,
		`"undefined_var" not found`,
	)
}

func (LSPSuite) TestDiagnosticsLoadsSiblingFiles(ctx context.Context, t *testctx.T) {
	root := t.TempDir()
	writeDangFile(t, filepath.Join(root, "defs.dang"), `type Template {
  pub name: String!
}

pub modules(): [Template!] {
  []
}
`)
	mainPath := filepath.Join(root, "main.dang")
	writeDangFile(t, mainPath, `pub useTemplates = modules()

pub typedTemplates: [Template!] {
  []
}
`)

	_, err := dang.RunDir(ctx, root, false)
	require.NoError(t, err, "fixture should type-check when loaded as a directory")

	h := newLSPHarness(ctx, t, root)
	uri := h.open(ctx, t, mainPath)
	diagnostics := h.waitForDiagnostics(ctx, t, uri)

	require.Empty(t, diagnostics, "opening one file should type-check it in the context of sibling .dang files")
}

func requireDiagnosticMessages(t *testctx.T, diagnostics []lsp.Diagnostic, expectedMessages ...string) {
	t.Helper()

	remaining := append([]string(nil), expectedMessages...)
	for _, diagnostic := range diagnostics {
		for i, expected := range remaining {
			if strings.Contains(diagnostic.Message, expected) {
				remaining = append(remaining[:i], remaining[i+1:]...)
				break
			}
		}
	}
	require.Empty(t, remaining, "missing expected diagnostics in %#v", diagnostics)
}

func fileURI(t *testctx.T, path string) lsp.DocumentURI {
	t.Helper()

	abs, err := filepath.Abs(path)
	require.NoError(t, err)

	return lsp.DocumentURI((&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(abs),
	}).String())
}

func writeDangFile(t *testctx.T, path, contents string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(strings.TrimLeft(contents, "\n")), 0o644))
}
