//go:build js && wasm

// Command dang-playground is a WebAssembly module that evaluates Dang source
// entirely client-side in the browser. It backs the interactive code snippets
// on the documentation site.
//
// It exposes a single global function, dangEval(source, token), which parses,
// type-checks, and evaluates a Dang program with the standard library in
// scope, returning a Promise that resolves to a result object.
//
// When token is a non-empty string, a live `import GitHub` is wired into the
// program: a GraphQL client pointed at https://api.github.com/graphql with the
// token as a bearer credential. The schema is introspected on first use (and
// cached for the life of the module) so `import GitHub` resolves to GitHub's
// real types and root fields — `viewer`, `repository`, and so on. Introspection
// and queries are ordinary cross-origin fetches; GitHub's GraphQL endpoint
// permits CORS, so no proxy is involved.
//
// dangEval returns a Promise (not a plain value) because that GitHub traffic is
// network I/O: a synchronous js.Func cannot block on a fetch without deadlocking
// the single-threaded wasm event loop, so the work runs in a goroutine that
// resolves the Promise when it finishes. Snippets with no token never touch the
// network but still resolve through the same Promise for a single call shape.
//
// Build with:
//
//	GOOS=js GOARCH=wasm go build -o docs/js/dang.wasm ./cmd/dang-playground
package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"syscall/js"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
	"github.com/vito/dang/pkg/ioctx"
)

// githubEndpoint is GitHub's GraphQL API. Queries and introspection both go here
// directly from the browser; the endpoint sends permissive CORS headers.
const githubEndpoint = "https://api.github.com/graphql"

func main() {
	js.Global().Set("dangEval", js.FuncOf(dangEval))
	// dangReady lets the page know the module has finished initializing.
	js.Global().Set("dangReady", js.ValueOf(true))
	if cb := js.Global().Get("onDangReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}
	// Block forever so the exported function stays callable.
	select {}
}

// result builds the JS object returned to the page.
//
//	{ ok: bool, value: string, output: string, error: string, stage: string }
//
// stage is "" on success, or "parse" | "type" | "eval" | "auth" identifying
// which phase failed. "auth" covers GitHub introspection failures (e.g. an
// expired or unauthorized token).
func result(value, output, errMsg, stage string) map[string]any {
	return map[string]any{
		"ok":     errMsg == "",
		"value":  value,
		"output": output,
		"error":  errMsg,
		"stage":  stage,
	}
}

// dangEval is the JS-facing entry point: dangEval(source, token) -> Promise of
// a result object. token is optional; when present and non-empty, an
// `import GitHub` in the source resolves against the live GitHub schema.
func dangEval(_ js.Value, args []js.Value) any {
	source := ""
	if len(args) >= 1 && args[0].Type() == js.TypeString {
		source = args[0].String()
	}
	token := ""
	if len(args) >= 2 && args[1].Type() == js.TypeString {
		token = args[1].String()
	}

	// Run the (potentially network-bound) evaluation in a goroutine and report
	// the outcome by resolving a Promise. See the package doc for why this
	// can't be synchronous.
	executor := js.FuncOf(func(_ js.Value, pargs []js.Value) any {
		resolve := pargs[0]
		go func() {
			resolve.Invoke(evalSource(source, token))
		}()
		return nil
	})
	// The Promise constructor invokes the executor synchronously, so once New
	// returns the executor won't be called again and can be released. The
	// goroutine keeps its own reference to resolve.
	defer executor.Release()
	return js.Global().Get("Promise").New(executor)
}

// evalSource parses, type-checks, and evaluates one snippet, returning the
// result map. It runs on its own goroutine, so it may block on network I/O
// (GitHub introspection/queries) freely.
func evalSource(source, token string) map[string]any {
	if source == "" {
		return result("", "", "dangEval expects a source string", "parse")
	}

	// Parse.
	parsed, err := dang.ParseWithRecovery("playground", []byte(source))
	if err != nil {
		return result("", "", err.Error(), "parse")
	}
	file, ok := parsed.(*dang.FileBlock)
	if !ok {
		return result("", "", "unexpected parse result", "parse")
	}
	forms := file.Forms

	// Build the context carrying any GraphQL imports. With a token, `import
	// GitHub` resolves; without one, the context is bare and only the standard
	// library is in scope. The same context backs both inference and
	// evaluation so the import's schema-module identity is shared between them.
	baseCtx := context.Background()
	if token != "" {
		cfg, err := githubImport(baseCtx, token)
		if err != nil {
			return result("", "", err.Error(), "auth")
		}
		baseCtx = dang.ContextWithImportConfigs(baseCtx, cfg)
	}

	// Fresh standard-library scopes per run, so re-running a snippet is
	// idempotent and never leaks state between evaluations.
	typeScope, valueScope := dang.BuildScopesFromImports("", nil)

	// Type-check.
	fresh := hm.NewSimpleFresher()
	if _, err := dang.InferFormsWithPhases(baseCtx, forms, typeScope, fresh); err != nil {
		return result("", "", err.Error(), "type")
	}

	// Evaluate, capturing anything written to stdout/stderr (e.g. log()).
	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(baseCtx, &out)
	ctx = ioctx.StderrToContext(ctx, &out)

	var last dang.Value
	for _, node := range forms {
		val, err := dang.EvalNode(ctx, valueScope, node)
		if err != nil {
			return result("", strings.TrimRight(out.String(), "\n"), err.Error(), "eval")
		}
		last = val
	}

	value := ""
	if last != nil {
		value = fmt.Sprint(last.String())
	}
	return result(value, strings.TrimRight(out.String(), "\n"), "", "")
}

// GitHub schema cache, keyed by the token it was introspected with. The wasm
// instance lives for the page's lifetime, so introspecting once (a multi-MB
// fetch) and reusing the schema keeps subsequent Runs fast. A different token
// (re-auth) forces a fresh introspection.
var (
	ghMu     sync.Mutex
	ghSchema *introspection.Schema
	ghToken  string
)

// githubImport builds the GitHub ImportConfig, introspecting the schema on
// first use and serving it from cache thereafter.
func githubImport(ctx context.Context, token string) (dang.ImportConfig, error) {
	client := graphql.NewClient(githubEndpoint, &http.Client{
		Transport: bearerTransport{token: token, base: http.DefaultTransport},
	})

	ghMu.Lock()
	defer ghMu.Unlock()
	if ghSchema == nil || ghToken != token {
		// dagger=false: GitHub is a plain GraphQL endpoint, so use the standard
		// introspection query rather than Dagger's variant.
		schema, err := dang.IntrospectSchema(ctx, client, false)
		if err != nil {
			return dang.ImportConfig{}, fmt.Errorf("GitHub introspection failed (check that you're signed in): %w", err)
		}
		ghSchema = schema
		ghToken = token
	}

	return dang.ImportConfig{
		Name:   "GitHub",
		Client: client,
		Schema: ghSchema,
	}, nil
}

// bearerTransport attaches the GitHub bearer token to every request.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}
