package gqlserver

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/ioctx"
)

// schemaSDL is this package's SDL, embedded so the schema is available without
// the source tree on disk — the docs playground runs as a wasm module in the
// browser, where runtime.Caller paths (used by loadIntrospectionSchema for the
// native test server) don't resolve to real files.
//
//go:embed schema.graphqls
var schemaSDL string

// ImportConfig builds a Dang import backed by this package's gqlgen schema and
// canned resolvers, executed entirely in-process: queries are dispatched
// straight into the gqlgen handler in memory, with no listener or socket. That
// makes the same import work both natively (the docs build, evaluating carousel
// slides) and under GOOS=js/wasm (the docs playground, running them live in the
// browser) — a real GraphQL schema with zero network, so `import <name>` and
// multi-field selection can be demonstrated offline.
//
// The type-checking schema comes from the embedded SDL (so applied directives
// survive); runtime data comes from the handler.
func ImportConfig(name string) (dang.ImportConfig, error) {
	schema, err := dang.SchemaFromSDL(schemaSDL, "schema.graphqls")
	if err != nil {
		return dang.ImportConfig{}, err
	}

	srv := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{}}))
	srv.AddTransport(transport.POST{})
	srv.SetQueryCache(lru.New[*ast.QueryDocument](100))

	return dang.ImportConfig{
		Name:   name,
		Schema: schema,
		Client: inProcessClient{handler: srv},
	}, nil
}

// inProcessClient implements genqlient's graphql.Client by serving each request
// through the gqlgen handler in memory via httptest — no network round trip.
type inProcessClient struct {
	handler http.Handler
}

func (c inProcessClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	// Echo the GraphQL query (not the HTTP mechanics) on the eval's stdout, so
	// docs readers can see what a selection compiled to: one request, resolved
	// server-side. See the carousel's GraphQL slides.
	_, _ = fmt.Fprintf(ioctx.StdoutFromContext(ctx), "→ %s\n", req.Query)

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq := httptest.NewRequest(http.MethodPost, "/query", bytes.NewReader(body)).WithContext(ctx)
	httpReq.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, httpReq)

	if err := json.Unmarshal(rec.Body.Bytes(), resp); err != nil {
		return err
	}
	// Surface GraphQL errors the way genqlient's HTTP client does, so a failed
	// field resolves to a Dang error instead of silently empty data.
	if len(resp.Errors) > 0 {
		return resp.Errors
	}
	return nil
}
