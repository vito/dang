package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaggerSource(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		enabled     bool
		version     string // the version's VersionString, "" for none
		err         string
	}{
		{name: "true", value: `true`, enabled: true},
		{name: "false", value: `false`},
		{name: "release", value: `"v0.19.0"`, enabled: true, version: "v0.19.0"},
		{name: "without v", value: `"0.21.6"`, enabled: true, version: "v0.21.6"},
		{name: "without patch", value: `"0.19"`, enabled: true, version: "v0.19.0"},
		{name: "major.minor with v", value: `"v1.0"`, enabled: true, version: "v1.0.0"},
		// Prerelease and build suffixes, as Dagger 1.0 betas write
		// engineVersion, name their base release: the engine serves its view.
		{name: "prerelease", value: `"v1.0.0-0"`, enabled: true, version: "v1.0.0"},
		{name: "beta", value: `"v1.0.0-beta.19"`, enabled: true, version: "v1.0.0"},
		{name: "build", value: `"v0.19.0+abc"`, enabled: true, version: "v0.19.0"},
		{name: "rc with build", value: `"v1.0.0-rc.1+abcdef12"`, enabled: true, version: "v1.0.0"},
		{name: "zero", value: `"0.0.0"`, err: `invalid Dagger version "0.0.0"`},
		{name: "major only", value: `"v1"`, err: `invalid Dagger version "v1"`},
		{name: "leading zero", value: `"v0.019.0"`, err: `invalid Dagger version "v0.019.0"`},
		{name: "bad prerelease", value: `"v1.0.0-01"`, err: `invalid Dagger version "v1.0.0-01"`},
		{name: "latest", value: `"latest"`, err: `invalid Dagger version "latest"`},
		{name: "empty", value: `""`, err: `invalid Dagger version ""`},
		{name: "number", value: `0.19`, err: `the Dagger version must be a string, e.g. dagger = "v0.19.0"`},
		{name: "table", value: `{ version = "v0.19.0" }`, err: "the Dagger version must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, err := LoadProjectConfig(writeConfig(t, "[imports.Dagger]\ndagger = "+tc.value+"\n"))
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			src := config.Imports["Dagger"].Dagger
			assert.Equal(t, tc.enabled, src.Enabled)
			assert.Equal(t, tc.version, src.VersionString())
			assert.Equal(t, tc.version, DaggerVersionString(src.Version))
			assert.Equal(t, tc.version == "", src.Version.IsZero())
		})
	}

	t.Run("JSON from a service", func(t *testing.T) {
		for _, tc := range []struct {
			json string
			want DaggerSource
		}{
			{`{"dagger": "v0.19"}`, DaggerSource{Enabled: true, Version: Version{Major: 0, Minor: 19}}},
			{`{"dagger": true}`, DaggerSource{Enabled: true}},
			{`{"dagger": false}`, DaggerSource{}},
			{`{"dagger": null}`, DaggerSource{}},
			{`{}`, DaggerSource{}},
		} {
			var src ImportSource
			require.NoError(t, json.Unmarshal([]byte(tc.json), &src), tc.json)
			assert.Equal(t, tc.want, src.Dagger, tc.json)
		}
		var src ImportSource
		require.ErrorContains(t, json.Unmarshal([]byte(`{"dagger": 1}`), &src), "the Dagger version must be a string")
	})

	sources := []struct {
		src  DaggerSource
		json string
	}{
		{DaggerSource{}, `false`},
		{DaggerSource{Enabled: true}, `true`},
		{DaggerSource{Enabled: true, Version: Version{Major: 0, Minor: 19}}, `"v0.19.0"`},
		{DaggerSource{Enabled: true, Version: Version{Major: 1, Minor: 0, Patch: 2}}, `"v1.0.2"`},
	}

	t.Run("JSON round trip", func(t *testing.T) {
		for _, tc := range sources {
			encoded, err := json.Marshal(tc.src)
			require.NoError(t, err)
			assert.Equal(t, tc.json, string(encoded))

			in := ImportSource{Dagger: tc.src, Service: []string{"dagger", "session"}}
			encoded, err = json.Marshal(in)
			require.NoError(t, err)
			var out ImportSource
			require.NoError(t, json.Unmarshal(encoded, &out), string(encoded))
			assert.Equal(t, in, out, string(encoded))
		}
	})

	t.Run("TOML round trip", func(t *testing.T) {
		type config struct {
			Imports map[string]*ImportSource `toml:"imports"`
		}
		for _, tc := range sources {
			in := config{Imports: map[string]*ImportSource{"Dagger": {Dagger: tc.src, Schema: "schema.graphqls"}}}
			var buf strings.Builder
			require.NoError(t, toml.NewEncoder(&buf).Encode(in))
			var out config
			_, err := toml.Decode(buf.String(), &out)
			require.NoError(t, err, buf.String())
			assert.Equal(t, in, out, buf.String())
			if tc.src.Enabled {
				assert.Contains(t, buf.String(), "dagger = "+tc.json+"\n")
			}
		}
	})
}

func TestDaggerVersionImport(t *testing.T) {
	load := func(t *testing.T, toml string) (*ProjectConfig, string) {
		t.Helper()
		path := writeConfig(t, toml)
		config, err := LoadProjectConfig(path)
		require.NoError(t, err)
		return config, filepath.Dir(path)
	}
	resolve := func(t *testing.T, toml string) (ImportConfig, *serviceProcess) {
		t.Helper()
		config, dir := load(t, toml)
		resolved, err := ResolveImportConfigs(context.Background(), config, dir)
		require.NoError(t, err)
		require.Len(t, resolved, 1)
		svc, ok := resolved[0].Client.(*serviceProcess)
		require.True(t, ok, "client is a %T", resolved[0].Client)
		return resolved[0], svc
	}

	t.Run("version starts the session at it", func(t *testing.T) {
		ic, svc := resolve(t, "[imports.Dagger]\ndagger = \"v0.19.0\"\n")
		assert.True(t, ic.Dagger)
		assert.Equal(t, Version{Major: 0, Minor: 19}, ic.DaggerVersion)
		assert.Equal(t, []string{"dagger", "session", "--version", "v0.19.0"}, svc.cmd)
		assert.Equal(t, Version{Major: 0, Minor: 19}, svc.daggerVersion)
		assert.True(t, svc.dagger)
	})

	t.Run("version is passed as vX.Y.Z", func(t *testing.T) {
		// Dagger guesses at versions without the v, and the engine drops a
		// prerelease suffix anyway.
		_, svc := resolve(t, "[imports.Dagger]\ndagger = \"0.21\"\n")
		assert.Equal(t, []string{"dagger", "session", "--version", "v0.21.0"}, svc.cmd)
		_, svc = resolve(t, "[imports.Dagger]\ndagger = \"v1.0.0-0\"\n")
		assert.Equal(t, []string{"dagger", "session", "--version", "v1.0.0"}, svc.cmd)
	})

	t.Run("true starts the default session", func(t *testing.T) {
		ic, svc := resolve(t, "[imports.Dagger]\ndagger = true\n")
		assert.True(t, ic.Dagger)
		assert.True(t, ic.DaggerVersion.IsZero())
		assert.Equal(t, []string{"dagger", "session"}, svc.cmd)
		assert.True(t, svc.daggerVersion.IsZero())
	})

	t.Run("version is appended to a custom service", func(t *testing.T) {
		config, dir := load(t, "[imports.Dagger]\ndagger = \"v0.19.0\"\nservice = [\"./bin/dagger\", \"session\"]\n")
		resolved, err := ResolveImportConfigs(context.Background(), config, dir)
		require.NoError(t, err)
		require.Len(t, resolved, 1)
		assert.Equal(t, Version{Major: 0, Minor: 19}, resolved[0].DaggerVersion)
		svc := resolved[0].Client.(*serviceProcess)
		assert.Equal(t, []string{"./bin/dagger", "session", "--version", "v0.19.0"}, svc.cmd)
		assert.Equal(t, []string{"./bin/dagger", "session"}, config.Imports["Dagger"].Service, "config is unchanged")
	})

	t.Run("false is no Dagger import", func(t *testing.T) {
		config, dir := load(t, "[imports.Api]\ndagger = false\nschema = \"schema.graphqls\"\n")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.graphqls"), []byte("type Query { hello: String! }\n"), 0644))
		resolved, err := ResolveImportConfigs(context.Background(), config, dir)
		require.NoError(t, err)
		require.Len(t, resolved, 1)
		assert.False(t, resolved[0].Dagger)
		assert.Nil(t, resolved[0].Client)
	})

	for _, tc := range []struct {
		name, toml, err string
	}{
		{
			name: "version with endpoint",
			toml: "[imports.Dagger]\ndagger = \"v0.19.0\"\nendpoint = \"http://127.0.0.1:1234/query\"\n",
			err:  "a Dagger version cannot be combined with 'endpoint'; use dagger = true",
		},
		{
			name: "version with path",
			toml: "[imports.Lib]\npath = \"./lib\"\ndagger = \"v0.19.0\"\n",
			err:  "'path' cannot be combined with 'dagger'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, dir := load(t, tc.toml)
			_, err := ResolveImportConfigs(context.Background(), config, dir)
			require.ErrorContains(t, err, tc.err)
		})
	}
}

// fakeGraphQLClient answers requests by operation name with canned data and
// records them.
type fakeGraphQLClient struct {
	mu       sync.Mutex
	requests []*graphql.Request
	data     map[string]string // OpName -> JSON data
}

func (c *fakeGraphQLClient) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()
	data, ok := c.data[req.OpName]
	if !ok {
		return fmt.Errorf("unexpected operation %q", req.OpName)
	}
	return json.Unmarshal([]byte(data), resp.Data)
}

func TestCheckDaggerVersion(t *testing.T) {
	for _, tc := range []struct {
		name, version, data, err string
	}{
		{
			name: "same release",
			data: `{"version": "v0.19.0", "__schemaVersion": "v0.19.0"}`,
		},
		{
			name: "newer engine",
			data: `{"version": "v0.21.6+abcdef12.dirty", "__schemaVersion": "v0.19.0"}`,
		},
		{
			name: "prerelease of a newer release",
			data: `{"version": "v1.0.0-beta.17", "__schemaVersion": "v0.19.0"}`,
		},
		{
			name: "unparseable engine version",
			data: `{"version": "devel", "__schemaVersion": "v0.19.0"}`,
		},
		{
			name: "older engine",
			data: `{"version": "v0.18.19", "__schemaVersion": "v0.19.0"}`,
			err:  "this project's Dagger version is v0.19.0, but the Dagger engine is v0.18.19; upgrade Dagger",
		},
		{
			name: "session ignored the version",
			data: `{"version": "v0.21.6", "__schemaVersion": "v0.21.6"}`,
			err:  `the Dagger session serves the API of "v0.21.6", not of the declared v0.19.0`,
		},
		{
			name:    "prerelease version is served as its base release",
			version: "v1.0.0-beta.19",
			data:    `{"version": "v1.0.0-beta.17+abcdef12", "__schemaVersion": "v1.0.0"}`,
		},
		{
			name:    "prerelease version on an older engine",
			version: "v1.0.0-0",
			data:    `{"version": "v0.21.6", "__schemaVersion": "v1.0.0"}`,
			err:     "this project's Dagger version is v1.0.0, but the Dagger engine is v0.21.6; upgrade Dagger",
		},
		{
			name:    "minor-only version",
			version: "0.19",
			data:    `{"version": "v0.19.3", "__schemaVersion": "v0.19.0"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.version
			if s == "" {
				s = "v0.19.0"
			}
			version, err := parseVersion("Dagger", s)
			require.NoError(t, err)
			client := &fakeGraphQLClient{data: map[string]string{"DangDaggerVersion": tc.data}}
			err = checkDaggerVersion(context.Background(), client, version)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
		})
	}
}

const fakeModuleSchemaJSON = `{"__schema": {"queryType": {"name": "Query"}, "types": [{"kind": "OBJECT", "name": "Query", "fields": []}], "directives": []}, "__schemaVersion": "v0.19.0"}`

func TestDaggerModuleSchemaVersion(t *testing.T) {
	contents, err := json.Marshal(fakeModuleSchemaJSON)
	require.NoError(t, err)
	schemaJSON := fmt.Sprintf(`{"introspectionSchemaJSON": {"contents": %s}}`, contents)

	t.Run("without a version, at the module's engineVersion", func(t *testing.T) {
		client := &fakeGraphQLClient{data: map[string]string{
			"DangModuleSchema": fmt.Sprintf(`{"moduleSource": %s}`, schemaJSON),
		}}
		schema, err := DaggerModuleSchema(context.Background(), client, "/mod")
		require.NoError(t, err)
		require.NotNil(t, schema.Types.Get("Query"))
		require.Len(t, client.requests, 1)
		assert.NotContains(t, client.requests[0].Query, "withEngineVersion")
		assert.Equal(t, map[string]any{"ref": "/mod"}, client.requests[0].Variables)
	})

	t.Run("with a version, at the version", func(t *testing.T) {
		client := &fakeGraphQLClient{data: map[string]string{
			"DangModuleSchema": fmt.Sprintf(`{"moduleSource": {"withEngineVersion": %s}}`, schemaJSON),
		}}
		schema, err := daggerModuleSchema(context.Background(), client, "/mod", Version{Major: 0, Minor: 19})
		require.NoError(t, err)
		require.NotNil(t, schema.Types.Get("Query"))
		require.Len(t, client.requests, 1)
		assert.Contains(t, client.requests[0].Query, "withEngineVersion(version: $version)")
		assert.Equal(t, map[string]any{"ref": "/mod", "version": "v0.19.0"}, client.requests[0].Variables)
	})

	t.Run("ResolveDaggerImport passes the import's version", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "dagger.json"), []byte("{}"), 0644))
		client := &fakeGraphQLClient{data: map[string]string{
			"DangModuleSchema": fmt.Sprintf(`{"moduleSource": {"withEngineVersion": %s}}`, schemaJSON),
		}}
		configs := ResolveDaggerImport(context.Background(), []ImportConfig{{
			Name:          "Dagger",
			Client:        client,
			Dagger:        true,
			DaggerVersion: Version{Major: 0, Minor: 19},
		}}, root)
		require.Len(t, configs, 1)
		require.NotNil(t, configs[0].Schema)
		require.Len(t, client.requests, 1)
		assert.Equal(t, "v0.19.0", client.requests[0].Variables.(map[string]any)["version"])
	})
}

// TestDaggerVersionSession runs a dang.toml Dagger import against a fake
// `dagger` CLI: a declared version must reach `dagger session` as --version,
// and the session is checked before it is used.
func TestDaggerVersionSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake dagger CLI is a shell script")
	}

	type request struct {
		OpName string `json:"operationName"`
		Query  string `json:"query"`
	}

	setup := func(t *testing.T, dagger, engineVersion string) (graphql.Client, string, *[]string) {
		t.Helper()
		var mu sync.Mutex
		var ops []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user, _, ok := r.BasicAuth(); !ok || user != "token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var req request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			ops = append(ops, req.OpName)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			switch req.OpName {
			case "DangDaggerVersion":
				fmt.Fprintf(w, `{"data": {"version": %q, "__schemaVersion": "v0.19.0"}}`, engineVersion)
			case "IntrospectionQuery":
				fmt.Fprintf(w, `{"data": %s}`, fakeModuleSchemaJSON)
			default:
				w.WriteHeader(http.StatusBadRequest)
			}
		}))
		t.Cleanup(srv.Close)
		port := srv.Listener.Addr().(*net.TCPAddr).Port

		bin := t.TempDir()
		argsFile := filepath.Join(bin, "args")
		script := fmt.Sprintf("#!/bin/sh\necho \"$@\" > %q\necho '{\"port\": %d, \"session_token\": \"token\"}'\nexec cat > /dev/null\n", argsFile, port)
		require.NoError(t, os.WriteFile(filepath.Join(bin, "dagger"), []byte(script), 0755))
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

		path := writeConfig(t, "[imports.Dagger]\ndagger = "+dagger+"\n")
		config, err := LoadProjectConfig(path)
		require.NoError(t, err)

		services := &ServiceRegistry{}
		t.Cleanup(services.StopAll)
		ctx := ContextWithServices(context.Background(), services)
		resolved, err := ResolveImportConfigs(ctx, config, filepath.Dir(path))
		require.NoError(t, err)
		require.Len(t, resolved, 1)
		return resolved[0].Client, argsFile, &ops
	}

	t.Run("engine serves the version", func(t *testing.T) {
		client, argsFile, ops := setup(t, `"0.19"`, "v0.21.6")
		schema, err := IntrospectSchema(context.Background(), client, true)
		require.NoError(t, err)
		require.NotNil(t, schema.Types.Get("Query"))

		args, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		assert.Equal(t, "session --version v0.19.0\n", string(args))
		assert.Equal(t, []string{"DangDaggerVersion", "IntrospectionQuery"}, *ops)
	})

	t.Run("engine older than the version", func(t *testing.T) {
		client, _, ops := setup(t, `"v0.19.0"`, "v0.18.19")
		_, err := IntrospectSchema(context.Background(), client, true)
		require.ErrorContains(t, err, "starting service [dagger session --version v0.19.0]: this project's Dagger version is v0.19.0, but the Dagger engine is v0.18.19; upgrade Dagger")
		assert.Equal(t, []string{"DangDaggerVersion"}, *ops)
	})

	t.Run("true follows the CLI", func(t *testing.T) {
		client, argsFile, ops := setup(t, `true`, "v0.18.19")
		_, err := IntrospectSchema(context.Background(), client, true)
		require.NoError(t, err)

		args, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		assert.Equal(t, "session\n", string(args))
		assert.Equal(t, []string{"IntrospectionQuery"}, *ops, "no version to check")
	})
}
