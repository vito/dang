package dang

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/pkg/introspection"
)

// GraphQLConfig holds configuration for connecting to a GraphQL API
type GraphQLConfig struct {
	// Endpoint is the GraphQL endpoint URL (e.g., "https://api.example.com/graphql")
	Endpoint string `json:"endpoint,omitempty"`

	// Authorization header value (e.g., "Bearer token123")
	Authorization string `json:"authorization,omitempty"`

	// Headers contains additional HTTP headers to send with requests
	Headers map[string]string `json:"headers,omitempty"`
}

// GraphQLClientProvider provides a configured GraphQL client and schema
type GraphQLClientProvider struct {
	config GraphQLConfig
}

// schemaCache represents a cached GraphQL schema
type schemaCache struct {
	Schema    *introspection.Schema `json:"schema"`
	Timestamp time.Time             `json:"timestamp"`
	Endpoint  string                `json:"endpoint"`
}

// NewGraphQLClientProvider creates a new provider with the given configuration
func NewGraphQLClientProvider(config GraphQLConfig) *GraphQLClientProvider {
	return &GraphQLClientProvider{config: config}
}

// GetClientAndSchema returns a configured GraphQL client and introspected schema
func (p *GraphQLClientProvider) GetClientAndSchema(ctx context.Context) (graphql.Client, *introspection.Schema, error) {
	if p.config.Endpoint == "" {
		return nil, nil, fmt.Errorf("no endpoint configured")
	}
	return p.getCustomClientAndSchema(ctx)
}

// getCustomClientAndSchema provides a client and schema for a custom GraphQL endpoint
func (p *GraphQLClientProvider) getCustomClientAndSchema(ctx context.Context) (graphql.Client, *introspection.Schema, error) {
	httpClient := &http.Client{
		Transport: &customTransport{
			base:          http.DefaultTransport,
			authorization: p.config.Authorization,
			headers:       p.config.Headers,
		},
	}

	client := graphql.NewClient(p.config.Endpoint, httpClient)

	// Try to load from cache first
	cachedSchema, err := loadCachedSchema(p.config.Endpoint)
	if err == nil && cachedSchema != nil {
		return client, cachedSchema, nil
	}

	// Cache miss or error - perform introspection
	schema, err := introspectSchema(ctx, client)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to introspect schema from %s: %w", p.config.Endpoint, err)
	}

	_ = saveCachedSchema(p.config.Endpoint, schema)

	return client, schema, nil
}

// Close is a no-op retained for API compatibility.
func (p *GraphQLClientProvider) Close() error {
	return nil
}

// getCacheDir returns the directory for schema caches
func getCacheDir() string {
	if cacheHome := os.Getenv("XDG_CACHE_HOME"); cacheHome != "" {
		return filepath.Join(cacheHome, "dang", "schemas")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cache", "dang", "schemas")
	}
	return filepath.Join(os.TempDir(), "dang-cache", "schemas")
}

func getCacheKey(endpoint string) string {
	h := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(h[:])
}

func loadCachedSchema(endpoint string) (*introspection.Schema, error) {
	cacheDir := getCacheDir()
	cacheFile := filepath.Join(cacheDir, getCacheKey(endpoint)+".json")

	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, err
	}

	var cached schemaCache
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, err
	}

	if cached.Endpoint != endpoint {
		return nil, fmt.Errorf("cache corruption: endpoint mismatch")
	}

	return cached.Schema, nil
}

func saveCachedSchema(endpoint string, schema *introspection.Schema) error {
	cacheDir := getCacheDir()
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}

	cached := schemaCache{
		Schema:    schema,
		Timestamp: time.Now(),
		Endpoint:  endpoint,
	}

	data, err := json.MarshalIndent(cached, "", "  ")
	if err != nil {
		return err
	}

	cacheFile := filepath.Join(cacheDir, getCacheKey(endpoint)+".json")
	return os.WriteFile(cacheFile, data, 0644)
}

func clearSchemaCache() error {
	cacheDir := getCacheDir()
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return fmt.Errorf("failed to read cache directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			filePath := filepath.Join(cacheDir, entry.Name())
			if err := os.Remove(filePath); err != nil {
				return fmt.Errorf("failed to remove cache file %s: %w", filePath, err)
			}
		}
	}

	return nil
}

// ClearSchemaCache removes all cached schemas
func ClearSchemaCache() error {
	return clearSchemaCache()
}

// customTransport wraps http.RoundTripper to add custom headers
type customTransport struct {
	base          http.RoundTripper
	authorization string
	headers       map[string]string
}

func (t *customTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())

	if t.authorization != "" {
		req.Header.Set("Authorization", t.authorization)
	}

	for key, value := range t.headers {
		req.Header.Set(key, value)
	}

	return t.base.RoundTrip(req)
}

// IntrospectSchema performs GraphQL introspection on the given client.
func IntrospectSchema(ctx context.Context, client graphql.Client) (*introspection.Schema, error) {
	return introspectSchema(ctx, client)
}

// introspectSchema performs GraphQL introspection on the given client
func introspectSchema(ctx context.Context, client graphql.Client) (*introspection.Schema, error) {
	var introspectionResp introspection.Response
	err := client.MakeRequest(ctx, &graphql.Request{
		Query:  introspection.Query,
		OpName: "IntrospectionQuery",
	}, &graphql.Response{
		Data: &introspectionResp,
	})
	if err != nil {
		return nil, fmt.Errorf("introspection query failed: %w", err)
	}

	return introspectionResp.Schema, nil
}

// LoadGraphQLConfig loads GraphQL configuration from environment variables
func LoadGraphQLConfig() GraphQLConfig {
	config := GraphQLConfig{
		Endpoint:      os.Getenv("DANG_GRAPHQL_ENDPOINT"),
		Authorization: os.Getenv("DANG_GRAPHQL_AUTHORIZATION"),
		Headers:       make(map[string]string),
	}

	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "DANG_GRAPHQL_HEADER_") {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				headerName := strings.TrimPrefix(parts[0], "DANG_GRAPHQL_HEADER_")
				headerName = strings.ReplaceAll(headerName, "_", "-")
				config.Headers[headerName] = parts[1]
			}
		}
	}

	return config
}
