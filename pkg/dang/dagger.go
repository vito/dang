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
	"slices"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/v2/pkg/introspection"
	"go.opentelemetry.io/otel/propagation"
)

// FindDaggerModule searches for a Dagger module starting from dir, walking
// up parent directories. Returns the directory containing a Dagger module config, or
// empty string if not found. Stops at .git boundaries.
func FindDaggerModule(startPath string) string {
	dir, err := filepath.Abs(startPath)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "dagger-module.toml")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, "dagger.toml")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, "dagger.json")); err == nil { // Backward compatibility with pre 1.0
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

// DaggerSource is the `dagger` key of a dang.toml import, which makes the
// import connect to a Dagger Engine session. It is the Dagger API version the
// import's source is written against, written like the `dang` key:
//
//	[imports.Dagger]
//	dagger = "v0.19.0"
//
// The engine serves its core API as of that release, the way it does for a
// Dagger module's engineVersion: it maps a client's version to an API view
// (engine.APIViewVersion), so newer engines keep serving the API the source
// was written against. The session is started with `dagger session --version
// vX.Y.Z`, and a Dagger module's schema is introspected with
// ModuleSource.withEngineVersion("vX.Y.Z"). In a Dagger module, the version
// should equal the module's engineVersion: Dagger runs the module's functions
// at that version, whatever dang.toml says, so a different one type-checks
// the module against an API it doesn't run with.
//
// Prerelease and build suffixes are dropped (see parseVersion), as
// engine.APIViewVersion drops them, so an engineVersion such as
// "v1.0.0-beta.19" can be copied verbatim.
//
// `dagger = true`, the older spelling, connects without declaring a version:
// `dagger session` then sends its own CLI version as the client version, so
// the API served follows the dagger CLI that starts the session, and a Dagger
// module's schema is introspected at its engineVersion.
type DaggerSource struct {
	// Enabled reports whether the import connects to Dagger: `dagger = true`
	// or a version.
	Enabled bool

	// Version is the declared Dagger version, or zero for `dagger = true`.
	Version Version
}

// UnmarshalTOML decodes the `dagger` key of a dang.toml import: true, false,
// or a Dagger version.
func (d *DaggerSource) UnmarshalTOML(data any) error {
	parsed, err := parseDaggerSource(data)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// UnmarshalJSON decodes the `dagger` field of an ImportSource printed by a
// service, like UnmarshalTOML.
func (d *DaggerSource) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	parsed, err := parseDaggerSource(v)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

func parseDaggerSource(data any) (DaggerSource, error) {
	switch v := data.(type) {
	case bool:
		return DaggerSource{Enabled: v}, nil
	case string:
		version, err := parseVersion("Dagger", v)
		if err != nil {
			return DaggerSource{}, err
		}
		if version.IsZero() {
			// The zero Version means `dagger = true`; there is no Dagger 0.0.0.
			return DaggerSource{}, fmt.Errorf("invalid Dagger version %q", v)
		}
		return DaggerSource{Enabled: true, Version: version}, nil
	default:
		return DaggerSource{}, fmt.Errorf("the Dagger version must be a string, e.g. dagger = %q", "v0.19.0")
	}
}

// daggerVersionString formats a Dagger version the way Dagger writes one,
// vMAJOR.MINOR.PATCH. Dagger guesses at forms without the "v" or the patch
// (engine.NormalizeVersion), so a version is always passed to it this way.
func daggerVersionString(v Version) string {
	return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// daggerSessionCommand is the default service command of a Dagger import.
// A declared version is passed with --version, which sets the session's
// client version and so the API view the engine serves it.
func daggerSessionCommand(version Version) []string {
	return daggerServiceCommand([]string{"dagger", "session"}, version)
}

// daggerServiceCommand appends a declared version to a Dagger import's
// service command, which runs a `dagger session` (e.g. of a dev CLI).
func daggerServiceCommand(service []string, version Version) []string {
	cmd := slices.Clone(service)
	if !version.IsZero() {
		cmd = append(cmd, "--version", daggerVersionString(version))
	}
	return cmd
}

// checkDaggerVersion verifies that a freshly started session serves the
// declared version's API. An engine older than the version can't serve it: it
// would silently serve its own, older API and echo the requested view in
// __schemaVersion, so the engine's version is compared instead. (An engine
// refuses a view older than its minimum client version itself, failing
// `dagger session` on startup.)
func checkDaggerVersion(ctx context.Context, client graphql.Client, version Version) error {
	want := daggerVersionString(version)
	var resp struct {
		Version       string `json:"version"`
		SchemaVersion string `json:"__schemaVersion"`
	}
	err := client.MakeRequest(ctx, &graphql.Request{
		Query:  `query DangDaggerVersion { version __schemaVersion }`,
		OpName: "DangDaggerVersion",
	}, &graphql.Response{Data: &resp})
	if err != nil {
		return fmt.Errorf("checking Dagger version %s: %w", want, err)
	}
	// Prerelease and build suffixes are dropped, as the engine drops them when
	// comparing versions: a v1.0.0-beta engine serves v1.0.0. An unparseable
	// engine version (e.g. a dev build's) is not compared.
	if engine, err := parseVersion("Dagger engine", resp.Version); err == nil && !engine.IsZero() && engine.Before(version) {
		return fmt.Errorf("this project's Dagger version is %s, but the Dagger engine is %s; upgrade Dagger", want, resp.Version)
	}
	if resp.SchemaVersion != want {
		return fmt.Errorf("the Dagger session serves the API of %q, not of the declared %s", resp.SchemaVersion, want)
	}
	return nil
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
	return daggerModuleSchema(ctx, client, moduleDir, Version{})
}

// daggerModuleSchema is DaggerModuleSchema at a declared Dagger version. The
// module-facing schema shows the core API at the module's engineVersion, not
// at the session's, so a declared version replaces the engineVersion for the
// introspection:
// dag.ModuleSource(moduleDir).WithEngineVersion(version).IntrospectionSchemaJSON().
// The zero version keeps the module's engineVersion.
func daggerModuleSchema(ctx context.Context, client graphql.Client, moduleDir string, version Version) (*introspection.Schema, error) {
	slog.Debug("querying module introspection schema", "moduleDir", moduleDir, "version", daggerVersionString(version))
	type schemaJSON struct {
		IntrospectionSchemaJSON struct {
			Contents string `json:"contents"`
		} `json:"introspectionSchemaJSON"`
	}
	var resp struct {
		ModuleSource struct {
			schemaJSON
			WithEngineVersion *schemaJSON `json:"withEngineVersion"`
		} `json:"moduleSource"`
	}

	req := &graphql.Request{
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
	}
	if !version.IsZero() {
		req.Query = `query DangModuleSchema($ref: String!, $version: String!) {
			moduleSource(refString: $ref) {
				withEngineVersion(version: $version) {
					introspectionSchemaJSON {
						contents
					}
				}
			}
		}`
		req.Variables = map[string]any{
			"ref":     moduleDir,
			"version": daggerVersionString(version),
		}
	}
	err := client.MakeRequest(ctx, req, &graphql.Response{Data: &resp})
	if err != nil {
		return nil, fmt.Errorf("module introspection query: %w", err)
	}

	contents := resp.ModuleSource.IntrospectionSchemaJSON.Contents
	if !version.IsZero() {
		if resp.ModuleSource.WithEngineVersion == nil {
			return nil, fmt.Errorf("module introspection response missing withEngineVersion")
		}
		contents = resp.ModuleSource.WithEngineVersion.IntrospectionSchemaJSON.Contents
	}

	var introspResp introspection.Response
	if err := json.Unmarshal([]byte(contents), &introspResp); err != nil {
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
	schema, err := introspectSchema(ctx, client, true)
	if err != nil {
		return nil, fmt.Errorf("introspecting served module schema: %w", err)
	}
	return schema, nil
}
