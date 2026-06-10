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
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/lsp"
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
		`operator addition is not defined between types String! and Int!`,
		`"undefined_var" not found`,
	)
}

func (LSPSuite) TestDiagnosticsIgnoresSiblingBodyErrors(ctx context.Context, t *testctx.T) {
	// Active file inference is "focused": siblings contribute only their
	// declarations (so cross-file references resolve), and their bodies are
	// not inferred. Diagnostics for the open buffer therefore reflect only
	// the open buffer's own errors.
	root := t.TempDir()
	writeDangFile(t, filepath.Join(root, "broken.dang"), `helper: String! { "ok" }

pub bad_add = "hello" + 42
`)
	mainPath := filepath.Join(root, "main.dang")
	writeDangFile(t, mainPath, `greeting: String! { helper }
`)

	h := newLSPHarness(ctx, t, root)
	uri := h.open(ctx, t, mainPath)
	diagnostics := h.waitForDiagnostics(ctx, t, uri)

	require.Empty(t, diagnostics, "sibling body errors should not surface on the active buffer")
}

func (LSPSuite) TestDiagnosticsImportedTypesUnifyAcrossFiles(ctx context.Context, t *testctx.T) {
	// Two files that exchange an imported type must agree on type identity.
	// Mirrors the go-sdk failure pattern: one file declares a type with a
	// field of an imported schema type, another file declares a type that
	// constructs the first one and passes that imported value through.
	//
	// Without shared schema modules across the directory, each file's
	// `import Test` builds its own *Type and unification fails with
	// "cannot use Test.ServerInfo as Test.ServerInfo".
	root := t.TempDir()

	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "tests", "gqlserver", "schema.graphqls"))
	require.NoError(t, err)
	writeDangFile(t, filepath.Join(root, "dang.toml"), fmt.Sprintf("[imports.Test]\nschema = %q\n", schemaPath))

	holderPath := filepath.Join(root, "holder.dang")
	writeDangFile(t, holderPath, `import Test

type Holder {
  let info: Test.ServerInfo!
}
`)

	ownerPath := filepath.Join(root, "owner.dang")
	writeDangFile(t, ownerPath, `import Test

type Owner {
  let info: Test.ServerInfo!

  make: Holder! {
    Holder(info: info)
  }
}
`)

	h := newLSPHarness(ctx, t, root)

	// Mirror the editor sequence: open holder.dang first (its analyzeDirectory
	// builds and caches a *Type), then open owner.dang. If the cache isn't
	// shared across the two analysis passes, owner.dang's ImportDecl builds a
	// distinct *Type and Test.ServerInfo fails to unify across the
	// Holder(info: info) call boundary.
	_ = h.open(ctx, t, holderPath)
	_ = h.waitForDiagnostics(ctx, t, fileURI(t, holderPath))

	uri := h.open(ctx, t, ownerPath)
	diagnostics := h.waitForDiagnostics(ctx, t, uri)

	require.Empty(t, diagnostics, "Test.ServerInfo should unify across files")
}

func (LSPSuite) TestDiagnosticsResolvesUnannotatedSiblingVars(ctx context.Context, t *testctx.T) {
	// A sibling's `pub answer = expr` has no type annotation, so the variable
	// signatures phase doesn't publish a type — body inference does. The
	// focused inference path runs the variables phase across all scopes so
	// the active file's reference still resolves.
	root := t.TempDir()
	writeDangFile(t, filepath.Join(root, "defs.dang"), `pub answer = "ok".toUpper
`)
	mainPath := filepath.Join(root, "main.dang")
	writeDangFile(t, mainPath, `use: String! = answer
`)

	h := newLSPHarness(ctx, t, root)
	uri := h.open(ctx, t, mainPath)
	diagnostics := h.waitForDiagnostics(ctx, t, uri)

	require.Empty(t, diagnostics, "unannotated sibling exports should publish their inferred types")
}

func (LSPSuite) TestDiagnosticsLoadsSiblingFiles(ctx context.Context, t *testctx.T) {
	root := t.TempDir()
	writeDangFile(t, filepath.Join(root, "defs.dang"), `type Template {
  name: String!
}

modules(): [Template!] {
  []
}
`)
	mainPath := filepath.Join(root, "main.dang")
	writeDangFile(t, mainPath, `pub useTemplates = modules()

typedTemplates: [Template!] {
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
