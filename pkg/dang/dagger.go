package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/pkg/introspection"
	"go.opentelemetry.io/otel/propagation"
)

// FindDaggerModule searches for a dagger.json starting from dir, walking
// up parent directories. Returns the directory containing dagger.json, or
// empty string if not found. Stops at .git boundaries.
func FindDaggerModule(startPath string) string {
	dir, err := filepath.Abs(startPath)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "dagger.json")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return ""
		}
		// .not-dagger is a marker for test fixtures etc. to suppress detection
		if _, err := os.Stat(filepath.Join(dir, ".not-dagger")); err == nil {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// daggerSessionParams is the JSON output from `dagger session`.
type daggerSessionParams struct {
	Port         int    `json:"port"`
	SessionToken string `json:"session_token"`
}

// otelPropagator propagates trace context and baggage via headers.
var otelPropagator = propagation.NewCompositeTextMapPropagator(
	propagation.Baggage{},
	propagation.TraceContext{},
)

// daggerTransport wraps http.RoundTripper to add basic auth (session token)
// and OpenTelemetry trace propagation for Dagger sessions.
type daggerTransport struct {
	base         http.RoundTripper
	sessionToken string
}

func (t *daggerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.SetBasicAuth(t.sessionToken, "")

	// Detect $TRACEPARENT set by `dagger run`
	ctx := req.Context()
	if !hasValidTraceContext(ctx) {
		ctx = otelPropagator.Extract(ctx, newSystemEnvCarrier())
		req = req.WithContext(ctx)
	}

	// Propagate span context via headers (for Dagger-in-Dagger)
	otelPropagator.Inject(req.Context(), propagation.HeaderCarrier(req.Header))

	return t.base.RoundTrip(req)
}

// hasValidTraceContext checks if the context already has a valid trace span.
func hasValidTraceContext(ctx context.Context) bool {
	// Extract from context and check if we get any trace headers
	carrier := propagation.MapCarrier{}
	otelPropagator.Inject(ctx, carrier)
	return carrier.Get("traceparent") != ""
}

// systemEnvCarrier reads OTel propagation fields from environment variables.
type systemEnvCarrier struct{}

func newSystemEnvCarrier() *systemEnvCarrier {
	return &systemEnvCarrier{}
}

func (c *systemEnvCarrier) Get(key string) string {
	return os.Getenv(strings.ToUpper(key))
}

func (c *systemEnvCarrier) Set(key, val string) {
	// no-op: we only read from system env
}

func (c *systemEnvCarrier) Keys() []string {
	return []string{"TRACEPARENT", "TRACESTATE", "BAGGAGE"}
}

// otelPropagationEnv returns environment variables for propagating trace
// context to a child process (e.g. `dagger session`).
func otelPropagationEnv(ctx context.Context) []string {
	// First try to pick up from system env if context doesn't have trace info
	if !hasValidTraceContext(ctx) {
		ctx = otelPropagator.Extract(ctx, newSystemEnvCarrier())
	}

	carrier := &envCarrier{}
	otelPropagator.Inject(ctx, carrier)
	return carrier.Env
}

// envCarrier collects propagation fields as KEY=VALUE strings.
type envCarrier struct {
	Env []string
}

func (c *envCarrier) Get(key string) string {
	envName := strings.ToUpper(key)
	for _, env := range c.Env {
		k, v, ok := strings.Cut(env, "=")
		if ok && k == envName {
			return v
		}
	}
	return ""
}

func (c *envCarrier) Set(key, val string) {
	c.Env = append(c.Env, strings.ToUpper(key)+"="+val)
}

func (c *envCarrier) Keys() []string {
	keys := make([]string, 0, len(c.Env))
	for _, env := range c.Env {
		k, _, ok := strings.Cut(env, "=")
		if ok {
			keys = append(keys, k)
		}
	}
	return keys
}

// newDaggerClient creates a graphql.Client from dagger session parameters.
func newDaggerClient(params *daggerSessionParams) graphql.Client {
	transport := &daggerTransport{
		base: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", params.Port))
			},
		},
		sessionToken: params.SessionToken,
	}
	httpClient := &http.Client{Transport: transport}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/query", params.Port)
	return graphql.NewClient(endpoint, httpClient)
}

// DaggerModuleSchema introspects a Dagger module's schema via raw GraphQL.
// This is the equivalent of dag.ModuleSource(moduleDir).IntrospectionSchemaJSON().Contents().
func DaggerModuleSchema(ctx context.Context, client graphql.Client, moduleDir string) (*introspection.Schema, error) {
	slog.Debug("querying module introspection schema", "moduleDir", moduleDir)
	var resp struct {
		ModuleSource struct {
			IntrospectionSchemaJSON struct {
				Contents string `json:"contents"`
			} `json:"introspectionSchemaJSON"`
		} `json:"moduleSource"`
	}

	err := client.MakeRequest(ctx, &graphql.Request{
		Query: `query DangModuleSchema($ref: String!) {
			moduleSource(refString: $ref) {
				introspectionSchemaJSON {
					contents
				}
			}
		}`,
		Variables: map[string]any{
			"ref": moduleDir,
		},
		OpName: "DangModuleSchema",
	}, &graphql.Response{Data: &resp})
	if err != nil {
		return nil, fmt.Errorf("module introspection query: %w", err)
	}

	var introspResp introspection.Response
	if err := json.Unmarshal([]byte(resp.ModuleSource.IntrospectionSchemaJSON.Contents), &introspResp); err != nil {
		return nil, fmt.Errorf("parsing module introspection JSON: %w", err)
	}
	if introspResp.Schema == nil {
		return nil, fmt.Errorf("module introspection response missing schema")
	}
	return introspResp.Schema, nil
}

// DaggerServeModule serves a Dagger module in the current session and
// returns the live schema. This makes the module's API available for queries.
func DaggerServeModule(ctx context.Context, client graphql.Client, moduleDir string) (*introspection.Schema, error) {
	// Serve the module (this is a query that has side effects on the session)
	var serveResp struct {
		ModuleSource struct {
			AsModule struct {
				Serve any `json:"serve"`
			} `json:"asModule"`
		} `json:"moduleSource"`
	}
	err := client.MakeRequest(ctx, &graphql.Request{
		Query: `query DangServeModule($ref: String!) {
			moduleSource(refString: $ref) {
				asModule {
					serve(includeDependencies: true)
				}
			}
		}`,
		Variables: map[string]any{
			"ref": moduleDir,
		},
		OpName: "DangServeModule",
	}, &graphql.Response{Data: &serveResp})
	if err != nil {
		return nil, fmt.Errorf("serving module: %w", err)
	}

	// Now introspect the live session schema (which includes the served module)
	schema, err := introspectSchema(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("introspecting served module schema: %w", err)
	}
	return schema, nil
}
