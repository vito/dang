package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dang/pkg/introspection"
)

// ProjectConfig represents a dang.toml project configuration file.
type ProjectConfig struct {
	// Imports maps import names to their source configurations.
	Imports map[string]*ImportSource `toml:"imports"`
}

// ImportSource describes where an import's GraphQL schema comes from.
type ImportSource struct {
	// Schema is a path to a local .graphqls SDL file (relative to dang.toml).
	// Provides type information for the LSP and type checker.
	Schema string `toml:"schema,omitempty"`

	// Endpoint is a GraphQL HTTP endpoint URL for runtime queries.
	// If set without Schema, the schema is introspected from the endpoint.
	Endpoint string `toml:"endpoint,omitempty"`

	// Service is a command that starts a GraphQL server. The command must
	// print its endpoint URL as the first line to stdout, then keep running.
	// The process is started lazily on first use and killed when Dang exits.
	// If Schema is also set, the schema is used for type checking and the
	// service is only started when runtime queries are needed.
	Service []string `toml:"service,omitempty"`

	// Authorization is the Authorization header value (e.g. "Bearer token").
	// Supports ${ENV_VAR} expansion.
	Authorization string `toml:"authorization,omitempty"`

	// Headers contains additional HTTP headers for the endpoint.
	// Values support ${ENV_VAR} expansion.
	Headers map[string]string `toml:"headers,omitempty"`
}

// LoadProjectConfig loads a dang.toml file from the given path.
func LoadProjectConfig(path string) (*ProjectConfig, error) {
	var config ProjectConfig
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &config, nil
}

// FindProjectConfig searches for a dang.toml file starting from dir and
// walking up to parent directories. Returns the path to dang.toml and the
// parsed config, or ("", nil, nil) if not found.
func FindProjectConfig(dir string) (string, *ProjectConfig, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", nil, err
	}
	for {
		path := filepath.Join(dir, "dang.toml")
		if _, err := os.Stat(path); err == nil {
			config, err := LoadProjectConfig(path)
			if err != nil {
				return "", nil, err
			}
			return path, config, nil
		}

		// Stop at .git boundary
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return "", nil, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil, nil
		}
		dir = parent
	}
}

// ResolveImportConfigs converts project config imports into ImportConfigs
// that can be used with ContextWithImportConfigs. The configDir is the
// directory containing dang.toml (used to resolve relative paths).
//
// For schema-only imports, the Client will be nil (type info only, no runtime).
// For endpoint imports, both Client and Schema are provided.
func ResolveImportConfigs(ctx context.Context, config *ProjectConfig, configDir string) ([]ImportConfig, error) {
	if config == nil || len(config.Imports) == 0 {
		return nil, nil
	}

	var configs []ImportConfig
	for name, source := range config.Imports {
		ic, err := resolveImportSource(ctx, name, source, configDir)
		if err != nil {
			return nil, fmt.Errorf("import %q: %w", name, err)
		}
		configs = append(configs, ic)
	}
	return configs, nil
}

func resolveImportSource(ctx context.Context, name string, source *ImportSource, configDir string) (ImportConfig, error) {
	ic := ImportConfig{Name: name}

	// Load schema from SDL file if specified
	if source.Schema != "" {
		schemaPath := source.Schema
		if !filepath.IsAbs(schemaPath) {
			schemaPath = filepath.Join(configDir, schemaPath)
		}
		schema, err := SchemaFromSDLFile(schemaPath)
		if err != nil {
			return ic, fmt.Errorf("loading schema %s: %w", schemaPath, err)
		}
		ic.Schema = schema
	}

	// Set up HTTP client for endpoint if specified
	if source.Endpoint != "" {
		ic.Client = makeClient(expandEnvVars(source.Endpoint), source.Authorization, source.Headers)
	}

	// Set up service launcher for lazy endpoint discovery
	if len(source.Service) > 0 && ic.Client == nil {
		svc := &serviceProcess{
			cmd:      source.Service,
			dir:      configDir,
			parent:   source,
			services: servicesFromContext(ctx),
		}
		ic.Client = svc
	}

	// If we have a client but no schema, introspect
	if ic.Client != nil && ic.Schema == nil {
		endpoint := source.Endpoint
		if endpoint != "" {
			endpoint = expandEnvVars(endpoint)
			cachedSchema, err := loadCachedSchema(endpoint)
			if err == nil && cachedSchema != nil {
				ic.Schema = cachedSchema
			} else {
				schema, err := introspectSchema(ctx, ic.Client)
				if err != nil {
					return ic, fmt.Errorf("introspecting %s: %w", endpoint, err)
				}
				ic.Schema = schema
				_ = saveCachedSchema(endpoint, schema)
			}
		}
	}

	if ic.Schema == nil && ic.Client == nil {
		return ic, fmt.Errorf("must specify at least one of 'schema', 'endpoint', or 'service'")
	}

	return ic, nil
}

func makeClient(endpoint, authorization string, headers map[string]string) graphql.Client {
	authz := expandEnvVars(authorization)
	expandedHeaders := make(map[string]string)
	for k, v := range headers {
		expandedHeaders[k] = expandEnvVars(v)
	}
	httpClient := &http.Client{
		Transport: &customTransport{
			base:          http.DefaultTransport,
			authorization: authz,
			headers:       expandedHeaders,
		},
	}
	return graphql.NewClient(endpoint, httpClient)
}

// serviceProcess implements graphql.Client by lazily starting a service
// process and proxying requests to it.
//
// The service command prints a JSON object to stdout matching the
// ImportSource schema (e.g. {"endpoint": "http://..."}), then closes
// stdout and stays running. If the response contains another "service"
// field, that service is started recursively. Resolution must eventually
// produce an "endpoint" or it is an error.
type serviceProcess struct {
	cmd     []string
	dir     string
	parent  *ImportSource // static config fields to use as defaults
	depth   int

	services *ServiceRegistry

	once     sync.Once
	delegate graphql.Client
	initErr  error
}

const maxServiceDepth = 10

func (s *serviceProcess) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	s.once.Do(func() {
		s.delegate, s.initErr = s.start(ctx)
	})
	if s.initErr != nil {
		return fmt.Errorf("starting service %v: %w", s.cmd, s.initErr)
	}
	return s.delegate.MakeRequest(ctx, req, resp)
}

func (s *serviceProcess) start(ctx context.Context) (graphql.Client, error) {
	if s.depth >= maxServiceDepth {
		return nil, fmt.Errorf("service recursion depth exceeded (%d)", maxServiceDepth)
	}

	slog.Info("starting import service", "cmd", s.cmd, "dir", s.dir)

	cmd := exec.CommandContext(ctx, s.cmd[0], s.cmd[1:]...)
	cmd.Dir = s.dir
	cmd.Stderr = os.Stderr
	// Use process group so we can kill the whole tree (e.g. go run -> child)
	setProcessGroup(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %v: %w", s.cmd, err)
	}

	// Register for cleanup
	if s.services != nil {
		s.services.register(cmd)
	}

	// Read a JSON object from stdout
	resultCh := make(chan *ImportSource, 1)
	errCh := make(chan error, 1)
	go func() {
		var source ImportSource
		if err := json.NewDecoder(stdout).Decode(&source); err != nil {
			errCh <- fmt.Errorf("reading service configuration: %w", err)
			return
		}
		resultCh <- &source
	}()

	var source *ImportSource
	select {
	case source = <-resultCh:
	case err := <-errCh:
		_ = killProcessGroup(cmd)
		return nil, err
	case <-time.After(30 * time.Second):
		_ = killProcessGroup(cmd)
		return nil, fmt.Errorf("timed out waiting for service configuration")
	}

	return s.resolve(ctx, source)
}

// resolve turns an ImportSource (returned by a service) into a client,
// recursively starting nested services if needed.
func (s *serviceProcess) resolve(ctx context.Context, source *ImportSource) (graphql.Client, error) {
	if source.Endpoint != "" {
		endpoint := expandEnvVars(source.Endpoint)
		slog.Info("import service ready", "endpoint", endpoint)
		return makeClient(endpoint, source.Authorization, source.Headers), nil
	}

	if len(source.Service) > 0 {
		nested := &serviceProcess{
			cmd:      source.Service,
			dir:      s.dir,
			parent:   source,
			depth:    s.depth + 1,
			services: s.services,
		}
		return nested.start(ctx)
	}

	return nil, fmt.Errorf("service output must include 'endpoint' or 'service'")
}

// ServiceRegistry tracks service processes for cleanup.
type ServiceRegistry struct {
	mu    sync.Mutex
	procs []*exec.Cmd
}

func (r *ServiceRegistry) register(cmd *exec.Cmd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.procs = append(r.procs, cmd)
}

// StopAll kills all registered service processes.
func (r *ServiceRegistry) StopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cmd := range r.procs {
		if cmd.Process != nil {
			slog.Info("stopping import service", "pid", cmd.Process.Pid)
			// Kill the entire process group to handle go run -> child
			_ = killProcessGroup(cmd)
		}
	}
	r.procs = nil
}

type servicesKey struct{}

// ContextWithServices adds a ServiceRegistry to the context for tracking
// service processes that need cleanup.
func ContextWithServices(ctx context.Context, services *ServiceRegistry) context.Context {
	return context.WithValue(ctx, servicesKey{}, services)
}

func servicesFromContext(ctx context.Context) *ServiceRegistry {
	if v := ctx.Value(servicesKey{}); v != nil {
		return v.(*ServiceRegistry)
	}
	return nil
}

// expandEnvVars expands ${VAR} references in a string using os.Getenv.
func expandEnvVars(s string) string {
	return os.Expand(s, func(key string) string {
		return os.Getenv(key)
	})
}

// SchemaFromSDLFile parses a .graphqls SDL file and converts it to an
// introspection.Schema suitable for use with Dang's import system.
func SchemaFromSDLFile(path string) (*introspection.Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return SchemaFromSDL(string(data), path)
}

// SchemaFromSDL parses a GraphQL SDL string and converts it to an
// introspection.Schema.
func SchemaFromSDL(sdl string, filename string) (*introspection.Schema, error) {
	source := &ast.Source{
		Name:  filename,
		Input: sdl,
	}
	schema, err := gqlparser.LoadSchema(source)
	if err != nil {
		return nil, fmt.Errorf("parsing GraphQL schema: %w", err)
	}
	return astSchemaToIntrospection(schema), nil
}

// astSchemaToIntrospection converts a gqlparser ast.Schema to an
// introspection.Schema that Dang's import system understands.
// astTypeKind maps ast.Definition.Kind to introspection TypeKind for named types.
var astTypeKindMap = map[ast.DefinitionKind]introspection.TypeKind{
	ast.Scalar:      introspection.TypeKindScalar,
	ast.Object:      introspection.TypeKindObject,
	ast.Interface:   introspection.TypeKindInterface,
	ast.Union:       introspection.TypeKindUnion,
	ast.Enum:        introspection.TypeKindEnum,
	ast.InputObject: introspection.TypeKindInputObject,
}

func astSchemaToIntrospection(schema *ast.Schema) *introspection.Schema {
	result := &introspection.Schema{}

	// Set root types
	if schema.Query != nil {
		result.QueryType.Name = schema.Query.Name
	}
	if schema.Mutation != nil {
		result.MutationType = &struct {
			Name string `json:"name,omitempty"`
		}{Name: schema.Mutation.Name}
	}
	if schema.Subscription != nil {
		result.SubscriptionType = &struct {
			Name string `json:"name,omitempty"`
		}{Name: schema.Subscription.Name}
	}

	// Convert types
	for _, t := range schema.Types {
		// Skip built-in introspection types
		if strings.HasPrefix(t.Name, "__") {
			continue
		}
		result.Types = append(result.Types, astTypeToIntrospection(schema, t))
	}

	// Convert directives
	for _, d := range schema.Directives {
		// Skip built-in directives
		if d.Name == "skip" || d.Name == "include" || d.Name == "deprecated" || d.Name == "specifiedBy" {
			continue
		}
		result.Directives = append(result.Directives, astDirectiveToIntrospection(schema, d))
	}

	return result
}

func astTypeToIntrospection(schema *ast.Schema, t *ast.Definition) *introspection.Type {
	it := &introspection.Type{
		Name:        t.Name,
		Description: t.Description,
	}

	switch t.Kind {
	case ast.Scalar:
		it.Kind = introspection.TypeKindScalar
	case ast.Object:
		it.Kind = introspection.TypeKindObject
		for _, f := range t.Fields {
			// Skip built-in introspection fields
			if strings.HasPrefix(f.Name, "__") {
				continue
			}
			it.Fields = append(it.Fields, astFieldToIntrospection(schema, f))
		}
		for _, iface := range t.Interfaces {
			it.Interfaces = append(it.Interfaces, &introspection.Type{
				Kind: introspection.TypeKindObject,
				Name: iface,
			})
		}
	case ast.Interface:
		it.Kind = introspection.TypeKindInterface
		for _, f := range t.Fields {
			it.Fields = append(it.Fields, astFieldToIntrospection(schema, f))
		}
	case ast.Union:
		it.Kind = introspection.TypeKindUnion
		for _, memberName := range t.Types {
			it.PossibleTypes = append(it.PossibleTypes, &introspection.Type{
				Kind: introspection.TypeKindObject,
				Name: memberName,
			})
		}
	case ast.Enum:
		it.Kind = introspection.TypeKindEnum
		for _, v := range t.EnumValues {
			it.EnumValues = append(it.EnumValues, introspection.EnumValue{
				Name:              v.Name,
				Description:       v.Description,
				IsDeprecated:      v.Directives.ForName("deprecated") != nil,
				DeprecationReason: deprecationReason(v.Directives),
			})
		}
	case ast.InputObject:
		it.Kind = introspection.TypeKindInputObject
		for _, f := range t.Fields {
			it.InputFields = append(it.InputFields, astInputValueFromField(schema, f))
		}
	}

	return it
}

func astFieldToIntrospection(schema *ast.Schema, f *ast.FieldDefinition) *introspection.Field {
	field := &introspection.Field{
		Name:              f.Name,
		Description:       f.Description,
		TypeRef:           astTypeRefToIntrospection(schema, f.Type),
		IsDeprecated:      f.Directives.ForName("deprecated") != nil,
		DeprecationReason: deprecationReason(f.Directives),
	}
	for _, arg := range f.Arguments {
		field.Args = append(field.Args, astInputValueToIntrospection(schema, arg))
	}
	return field
}

func astInputValueToIntrospection(schema *ast.Schema, v *ast.ArgumentDefinition) introspection.InputValue {
	iv := introspection.InputValue{
		Name:        v.Name,
		Description: v.Description,
		TypeRef:     astTypeRefToIntrospection(schema, v.Type),
	}
	if v.DefaultValue != nil {
		s := v.DefaultValue.String()
		iv.DefaultValue = &s
	}
	return iv
}

func astInputValueFromField(schema *ast.Schema, f *ast.FieldDefinition) introspection.InputValue {
	iv := introspection.InputValue{
		Name:        f.Name,
		Description: f.Description,
		TypeRef:     astTypeRefToIntrospection(schema, f.Type),
	}
	if f.DefaultValue != nil {
		s := f.DefaultValue.String()
		iv.DefaultValue = &s
	}
	return iv
}

func astTypeRefToIntrospection(schema *ast.Schema, t *ast.Type) *introspection.TypeRef {
	if t == nil {
		return nil
	}
	if t.NonNull {
		inner := *t
		inner.NonNull = false
		return &introspection.TypeRef{
			Kind:   introspection.TypeKindNonNull,
			OfType: astTypeRefToIntrospection(schema, &inner),
		}
	}
	if t.Elem != nil {
		return &introspection.TypeRef{
			Kind:   introspection.TypeKindList,
			OfType: astTypeRefToIntrospection(schema, t.Elem),
		}
	}
	// Named type — look up the actual kind from the schema
	kind := introspection.TypeKindObject // fallback
	if def := schema.Types[t.NamedType]; def != nil {
		if k, ok := astTypeKindMap[def.Kind]; ok {
			kind = k
		}
	}
	return &introspection.TypeRef{
		Kind: kind,
		Name: t.NamedType,
	}
}

func astDirectiveToIntrospection(schema *ast.Schema, d *ast.DirectiveDefinition) *introspection.DirectiveDef {
	dd := &introspection.DirectiveDef{
		Name:        d.Name,
		Description: d.Description,
	}
	for _, loc := range d.Locations {
		dd.Locations = append(dd.Locations, string(loc))
	}
	for _, arg := range d.Arguments {
		dd.Args = append(dd.Args, astInputValueToIntrospection(schema, arg))
	}
	return dd
}

func deprecationReason(directives ast.DirectiveList) string {
	d := directives.ForName("deprecated")
	if d == nil {
		return ""
	}
	arg := d.Arguments.ForName("reason")
	if arg == nil || arg.Value == nil {
		return ""
	}
	return arg.Value.Raw
}

// projectConfigKey is a context key for passing the resolved project config path.
type projectConfigKey struct{}

// ContextWithProjectConfig adds a project config to the context so that
// the import resolver can find it.
func ContextWithProjectConfig(ctx context.Context, configPath string, config *ProjectConfig) context.Context {
	return context.WithValue(ctx, projectConfigKey{}, &projectConfigEntry{
		path:   configPath,
		config: config,
	})
}

type projectConfigEntry struct {
	path   string
	config *ProjectConfig
}

func projectConfigFromContext(ctx context.Context) (string, *ProjectConfig) {
	if v := ctx.Value(projectConfigKey{}); v != nil {
		e := v.(*projectConfigEntry)
		return e.path, e.config
	}
	return "", nil
}
