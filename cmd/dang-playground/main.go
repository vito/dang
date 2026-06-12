//go:build js && wasm

// Command dang-playground is a WebAssembly module that evaluates Dang source
// entirely client-side in the browser. It backs the interactive code snippets
// and REPL on the documentation site.
//
// It exposes these global functions:
//
//   - dangEval(source, token) parses, type-checks, and evaluates a Dang program
//     with the standard library in scope, returning a Promise that resolves to
//     a result object. When token is a non-empty string, a live `import GitHub`
//     is wired into the program: a GraphQL client pointed at
//     https://api.github.com/graphql with the token as a bearer credential. The
//     schema is introspected on first use (and cached for the life of the
//     module) so `import GitHub` resolves to GitHub's real types and root fields
//     — `viewer`, `repository`, and so on. Introspection and queries are
//     ordinary cross-origin fetches; GitHub's GraphQL endpoint permits CORS, so
//     no proxy is involved.
//
//     dangEval returns a Promise (not a plain value) because that GitHub traffic
//     is network I/O: a synchronous js.Func cannot block on a fetch without
//     deadlocking the single-threaded wasm event loop, so the work runs in a
//     goroutine that resolves the Promise when it finishes. Snippets with no
//     token never touch the network but still resolve through the same Promise
//     for a single call shape.
//
//   - dangReplEval(sessionID, source) evaluates one REPL entry against a
//     persistent, long-running session identified by sessionID. The session's
//     scopes are kept alive between calls and mutated in place, so each entry is
//     type-checked and evaluated incrementally against accumulated state —
//     nothing is re-parsed or re-run. This mirrors the native CLI REPL
//     (cmd/dang/repl_tuist.go), which reuses one scope pair for the whole
//     session rather than replaying history. The browser REPL runs the core
//     language only (no GraphQL imports), so it stays synchronous.
//
//   - dangLiterateEval(sessionID, source) is dangReplEval with the literate
//     blocks' display rule: an entry whose last form is a declaration yields
//     no value, only its output. It backs the \dang-literate blocks
//     (docs/go/literate.go), which share one session per page; re-running a
//     page's chain client-side must show exactly what the build baked in.
//
//   - dangLiterateFailEval(sessionID, source) is dangLiterateEval against
//     throwaway forks of the session's scopes, for the expected-failure
//     literate blocks (```dang-failure fences): the entry sees everything the
//     session has accumulated but contributes nothing back, mirroring
//     literateFailEval (docs/go/literate.go). The result is reported as-is —
//     the page decides how to present a block that was expected to fail but
//     succeeded.
//
//   - dangReplReset(sessionID) discards a session's accumulated state, so the
//     next dangReplEval starts from fresh standard-library scopes.
//
// Build with:
//
//	GOOS=js GOARCH=wasm go build -o docs/js/dang.wasm ./cmd/dang-playground
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"syscall/js"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"
	"github.com/vito/dang/v2/pkg/ioctx"
)

// githubEndpoint is GitHub's GraphQL API. Queries and introspection both go here
// directly from the browser; the endpoint sends permissive CORS headers.
const githubEndpoint = "https://api.github.com/graphql"

func main() {
	js.Global().Set("dangEval", js.FuncOf(dangEval))
	js.Global().Set("dangReplEval", js.FuncOf(dangReplEval))
	js.Global().Set("dangLiterateEval", js.FuncOf(dangLiterateEval))
	js.Global().Set("dangLiterateFailEval", js.FuncOf(dangLiterateFailEval))
	js.Global().Set("dangReplReset", js.FuncOf(dangReplReset))
	// dangReady lets the page know the module has finished initializing.
	js.Global().Set("dangReady", js.ValueOf(true))
	if cb := js.Global().Get("onDangReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}
	// Block forever so the exported functions stay callable.
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

// ── REPL sessions ───────────────────────────────────────────────────────────

// replSession is one long-running REPL: a scope pair kept alive across entries.
type replSession struct {
	typeScope  dang.TypeScope
	valueScope dang.ValueScope
}

// replSessions holds the live sessions keyed by the handle the frontend assigns
// to each REPL component on the page (a different REPL gets a different handle,
// so their state never bleeds together). The WASM module is single-threaded —
// one goroutine services every JS call — so a plain map needs no locking.
var replSessions = map[int]*replSession{}

// session returns the session for id, lazily creating it from fresh scopes on
// first use (or after a reset). This is the persistent state the REPL
// accumulates into; subsequent entries mutate these scopes in place.
func session(id int) *replSession {
	s := replSessions[id]
	if s == nil {
		typeScope, valueScope := dang.BuildScopesFromImports("", nil)
		s = &replSession{typeScope: typeScope, valueScope: valueScope}
		replSessions[id] = s
	}
	return s
}

// evalForms parses, type-checks, and evaluates source against the given scopes,
// writing any stdout/stderr through ctx. It returns the stringified value of
// the last form and whether that form is a declaration, or a non-nil error and
// the failing stage ("parse" | "type" | "eval"). The scopes are mutated in
// place, so passing the same scopes to successive calls accumulates session
// state.
func evalForms(ctx context.Context, source string, typeScope dang.TypeScope, valueScope dang.ValueScope, fresh hm.Fresher) (string, bool, error, string) {
	parsed, err := dang.ParseWithRecovery("playground", []byte(source))
	if err != nil {
		return "", false, err, "parse"
	}
	file, ok := parsed.(*dang.FileBlock)
	if !ok {
		return "", false, fmt.Errorf("unexpected parse result"), "parse"
	}
	forms := file.Forms

	if _, err := dang.InferFormsWithPhases(ctx, forms, typeScope, fresh); err != nil {
		return "", false, err, "type"
	}

	var lastNode dang.Node
	var last dang.Value
	for _, node := range forms {
		val, err := dang.EvalNode(ctx, valueScope, node)
		if err != nil {
			return "", false, err, "eval"
		}
		lastNode = node
		last = val
	}

	value := ""
	lastIsDecl := false
	if last != nil {
		value = fmt.Sprint(last.String())
		lastIsDecl = len(lastNode.DeclaredSymbols()) > 0
	}
	return value, lastIsDecl, nil, ""
}

// dangReplEval evaluates one REPL entry: dangReplEval(sessionID, source).
//
// The entry is type-checked and evaluated against the session's persistent
// scopes, which are mutated in place so definitions accumulate (`let greeting =
// "world"` then “ `hello, ${greeting}!` “ works) without re-parsing or
// re-running any earlier entry. Only this entry's output and result are
// returned. The browser REPL is core-language only (no GraphQL imports), so
// unlike dangEval it never touches the network and stays synchronous.
//
// Like the native CLI REPL, a single scope pair lives for the whole session.
// The tradeoff is that an entry which fails partway (a type error, or a runtime
// error after some forms have already bound) can leave partial state behind;
// dangReplReset clears it. A fresh Fresher is required per call — its type-
// variable counter is monotonic — but that's unrelated to the session state.
func dangReplEval(_ js.Value, args []js.Value) any {
	if len(args) < 2 || args[0].Type() != js.TypeNumber || args[1].Type() != js.TypeString {
		return result("", "", "dangReplEval expects (sessionID, source)", "parse")
	}
	sess := session(args[0].Int())
	source := args[1].String()

	fresh := hm.NewSimpleFresher()

	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	ctx = ioctx.StderrToContext(ctx, &out)

	value, _, err, stage := evalForms(ctx, source, sess.typeScope, sess.valueScope, fresh)
	if err != nil {
		return result("", strings.TrimRight(out.String(), "\n"), err.Error(), stage)
	}
	return result(value, strings.TrimRight(out.String(), "\n"), "", "")
}

// dangLiterateEval evaluates one literate block: dangLiterateEval(sessionID,
// source). Identical to dangReplEval except for the display rule shared with
// the build-time evaluator (literateEval in docs/go/literate.go — the two
// must stay in lockstep): a block whose last form is a declaration yields no
// value, so a setup block shows only what it prints.
func dangLiterateEval(_ js.Value, args []js.Value) any {
	if len(args) < 2 || args[0].Type() != js.TypeNumber || args[1].Type() != js.TypeString {
		return result("", "", "dangLiterateEval expects (sessionID, source)", "parse")
	}
	sess := session(args[0].Int())
	source := args[1].String()

	fresh := hm.NewSimpleFresher()

	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	ctx = ioctx.StderrToContext(ctx, &out)

	value, lastIsDecl, err, stage := evalForms(ctx, source, sess.typeScope, sess.valueScope, fresh)
	if err != nil {
		return result("", strings.TrimRight(out.String(), "\n"), err.Error(), stage)
	}
	if lastIsDecl {
		value = ""
	}
	return result(value, strings.TrimRight(out.String(), "\n"), "", "")
}

// dangLiterateFailEval evaluates one expected-failure literate block:
// dangLiterateFailEval(sessionID, source). The block runs against throwaway
// forks of the session's scopes — a cloned type scope and a sealed child
// value scope — so whatever it declares or half-evaluates never reaches the
// shared session: failing is the point, and a failing block's partial state
// is unknowable. This mirrors the build-time evaluator (literateFailEval in
// docs/go/literate.go — the two must stay in lockstep). Unlike the build,
// which rejects a succeeding snippet outright, this reports the result
// as-is; the page presents an unexpected success however it sees fit.
func dangLiterateFailEval(_ js.Value, args []js.Value) any {
	if len(args) < 2 || args[0].Type() != js.TypeNumber || args[1].Type() != js.TypeString {
		return result("", "", "dangLiterateFailEval expects (sessionID, source)", "parse")
	}
	sess := session(args[0].Int())
	source := args[1].String()

	typeScope, ok := sess.typeScope.Clone().(dang.TypeScope)
	if !ok {
		return result("", "", "session type scope is not a TypeScope", "type")
	}
	valueScope := sess.valueScope.Derive(true)

	fresh := hm.NewSimpleFresher()

	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	ctx = ioctx.StderrToContext(ctx, &out)

	value, lastIsDecl, err, stage := evalForms(ctx, source, typeScope, valueScope, fresh)
	if err != nil {
		return result("", strings.TrimRight(out.String(), "\n"), failureMessage(err), stage)
	}
	if lastIsDecl {
		value = ""
	}
	return result(value, strings.TrimRight(out.String(), "\n"), "", "")
}

// failureMessage extracts the bare message from an expected failure.
// SourceError.Error() renders for a terminal — ANSI colors plus a quoted
// source span — but the failing block's editor already shows the source, and
// escape codes have no business in the page. The same unwrap lives in
// docs/go/literate.go's failureMessage; the two must stay in lockstep so a
// replay shows exactly the line the build baked.
func failureMessage(err error) string {
	var srcErr *dang.SourceError
	if errors.As(err, &srcErr) {
		return srcErr.Inner.Error()
	}
	return err.Error()
}

// dangReplReset(sessionID) discards a session so the next dangReplEval starts
// from fresh scopes. It's a no-op for an unknown id (session() recreates lazily).
func dangReplReset(_ js.Value, args []js.Value) any {
	if len(args) < 1 || args[0].Type() != js.TypeNumber {
		return false
	}
	delete(replSessions, args[0].Int())
	return true
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
