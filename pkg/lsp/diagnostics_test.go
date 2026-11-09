package lsp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagger/testctx"
	jsonrpc "github.com/gumeniukcom/golang-jsonrpc2/v2"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/lsp"
)

type lspHarness struct {
	rpc         *jsonrpc.JSONRPC
	pusher      *testPusher
	diagnostics chan lsp.PublishDiagnosticsParams
	notifyErrs  chan error
}

// testPusher captures server-initiated notifications the handlers push via
// jsonrpc.PusherFromContext, standing in for a bidirectional transport.
type testPusher struct {
	h *lspHarness
}

func (p *testPusher) Notify(ctx context.Context, method string, params any) error {
	if method != "textDocument/publishDiagnostics" {
		return nil
	}

	raw, err := json.Marshal(params)
	if err != nil {
		p.h.notifyErrs <- fmt.Errorf("marshal %s: %w", method, err)
		return nil
	}
	var diagnosticsParams lsp.PublishDiagnosticsParams
	if err := json.Unmarshal(raw, &diagnosticsParams); err != nil {
		p.h.notifyErrs <- fmt.Errorf("unmarshal %s: %w", method, err)
		return nil
	}
	p.h.diagnostics <- diagnosticsParams
	return nil
}

func newLSPHarness(ctx context.Context, t *testctx.T, root string) *lspHarness {
	t.Helper()

	h := &lspHarness{
		diagnostics: make(chan lsp.PublishDiagnosticsParams, 16),
		notifyErrs:  make(chan error, 16),
	}
	h.pusher = &testPusher{h: h}

	services := &dang.ServiceRegistry{}
	ctx = dang.ContextWithServices(ctx, services)
	t.Cleanup(services.StopAll)

	handler := lsp.NewHandler(ctx)
	h.rpc = jsonrpc.New()
	require.NoError(t, handler.Register(h.rpc))

	var initResult lsp.InitializeResult
	h.call(ctx, t, "initialize", lsp.InitializeParams{
		RootURI: fileURI(t, root),
	}, &initResult)
	h.notify(ctx, t, "initialized", map[string]any{})

	return h
}

// dispatch sends a single JSON-RPC message through the dispatcher with the
// harness pusher installed, mirroring what a bidirectional transport does.
func (h *lspHarness) dispatch(ctx context.Context, t *testctx.T, method string, params any, withID bool) json.RawMessage {
	t.Helper()

	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	if withID {
		req["id"] = 1
	}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	return h.rpc.HandleRPCJSONRawMessage(jsonrpc.ContextWithPusher(ctx, h.pusher), raw)
}

func (h *lspHarness) call(ctx context.Context, t *testctx.T, method string, params any, result any) {
	t.Helper()

	raw := h.dispatch(ctx, t, method, params, true)
	require.NotEmpty(t, raw, "expected a response for %s", method)

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	if len(resp.Error) > 0 && string(resp.Error) != "null" {
		t.Fatalf("rpc error calling %s: %s", method, resp.Error)
	}
	if result != nil {
		require.NoError(t, json.Unmarshal(resp.Result, result))
	}
}

func (h *lspHarness) notify(ctx context.Context, t *testctx.T, method string, params any) {
	t.Helper()

	h.dispatch(ctx, t, method, params, false)
}

func (h *lspHarness) open(ctx context.Context, t *testctx.T, path string) lsp.DocumentURI {
	t.Helper()

	contents, err := os.ReadFile(path)
	require.NoError(t, err)

	uri := fileURI(t, path)
	h.notify(ctx, t, "textDocument/didOpen", lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        uri,
			LanguageID: "dang",
			Version:    1,
			Text:       string(contents),
		},
	})
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
