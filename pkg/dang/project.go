package dang

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
		endpoint := expandEnvVars(source.Endpoint)
		authorization := expandEnvVars(source.Authorization)
		headers := make(map[string]string)
		for k, v := range source.Headers {
			headers[k] = expandEnvVars(v)
		}

		httpClient := &http.Client{
			Transport: &customTransport{
				base:          http.DefaultTransport,
				authorization: authorization,
				headers:       headers,
			},
		}
		client := graphql.NewClient(endpoint, httpClient)
		ic.Client = client

		// If no local schema file, introspect from the endpoint
		if ic.Schema == nil {
			// Try cache first
			cachedSchema, err := loadCachedSchema(endpoint)
			if err == nil && cachedSchema != nil {
				ic.Schema = cachedSchema
			} else {
				schema, err := introspectSchema(ctx, client)
				if err != nil {
					return ic, fmt.Errorf("introspecting %s: %w", endpoint, err)
				}
				ic.Schema = schema
				_ = saveCachedSchema(endpoint, schema)
			}
		}
	}

	if ic.Schema == nil && ic.Client == nil {
		return ic, fmt.Errorf("must specify at least one of 'schema' or 'endpoint'")
	}

	return ic, nil
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
		result.Types = append(result.Types, astTypeToIntrospection(t))
	}

	// Convert directives
	for _, d := range schema.Directives {
		// Skip built-in directives
		if d.Name == "skip" || d.Name == "include" || d.Name == "deprecated" || d.Name == "specifiedBy" {
			continue
		}
		result.Directives = append(result.Directives, astDirectiveToIntrospection(d))
	}

	return result
}

func astTypeToIntrospection(t *ast.Definition) *introspection.Type {
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
			it.Fields = append(it.Fields, astFieldToIntrospection(f))
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
			it.Fields = append(it.Fields, astFieldToIntrospection(f))
		}
	case ast.Union:
		it.Kind = introspection.TypeKindUnion
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
			it.InputFields = append(it.InputFields, astInputValueFromField(f))
		}
	}

	return it
}

func astFieldToIntrospection(f *ast.FieldDefinition) *introspection.Field {
	field := &introspection.Field{
		Name:              f.Name,
		Description:       f.Description,
		TypeRef:           astTypeRefToIntrospection(f.Type),
		IsDeprecated:      f.Directives.ForName("deprecated") != nil,
		DeprecationReason: deprecationReason(f.Directives),
	}
	for _, arg := range f.Arguments {
		field.Args = append(field.Args, astInputValueToIntrospection(arg))
	}
	return field
}

func astInputValueToIntrospection(v *ast.ArgumentDefinition) introspection.InputValue {
	iv := introspection.InputValue{
		Name:        v.Name,
		Description: v.Description,
		TypeRef:     astTypeRefToIntrospection(v.Type),
	}
	if v.DefaultValue != nil {
		s := v.DefaultValue.String()
		iv.DefaultValue = &s
	}
	return iv
}

func astInputValueFromField(f *ast.FieldDefinition) introspection.InputValue {
	iv := introspection.InputValue{
		Name:        f.Name,
		Description: f.Description,
		TypeRef:     astTypeRefToIntrospection(f.Type),
	}
	if f.DefaultValue != nil {
		s := f.DefaultValue.String()
		iv.DefaultValue = &s
	}
	return iv
}

func astTypeRefToIntrospection(t *ast.Type) *introspection.TypeRef {
	if t == nil {
		return nil
	}
	if t.NonNull {
		// NON_NULL wraps the inner type
		inner := *t
		inner.NonNull = false
		return &introspection.TypeRef{
			Kind:   introspection.TypeKindNonNull,
			OfType: astTypeRefToIntrospection(&inner),
		}
	}
	if t.Elem != nil {
		// LIST wraps the element type
		return &introspection.TypeRef{
			Kind:   introspection.TypeKindList,
			OfType: astTypeRefToIntrospection(t.Elem),
		}
	}
	// Named type — determine kind from name (we don't have full schema
	// context here, so we use OBJECT as a generic placeholder; the Dang
	// import system looks types up by name anyway).
	return &introspection.TypeRef{
		Kind: introspection.TypeKindObject,
		Name: t.NamedType,
	}
}

func astDirectiveToIntrospection(d *ast.DirectiveDefinition) *introspection.DirectiveDef {
	dd := &introspection.DirectiveDef{
		Name:        d.Name,
		Description: d.Description,
	}
	for _, loc := range d.Locations {
		dd.Locations = append(dd.Locations, string(loc))
	}
	for _, arg := range d.Arguments {
		dd.Args = append(dd.Args, astInputValueToIntrospection(arg))
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
