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

	"dagger.io/dagger"
	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/introspection"
)

// GraphQLConfig holds configuration for connecting to a GraphQL API
type GraphQLConfig struct {
	// Endpoint is the GraphQL endpoint URL (e.g., "https://api.example.com/graphql")
	// If empty, defaults to Dagger
	Endpoint string `json:"endpoint,omitempty"`

	// Authorization header value (e.g., "Bearer token123")
	Authorization string `json:"authorization,omitempty"`

	// Headers contains additional HTTP headers to send with requests
	Headers map[string]string `json:"headers,omitempty"`
}

// GraphQLClientProvider provides a configured GraphQL client and schema
type GraphQLClientProvider struct {
	config     GraphQLConfig
	daggerConn *dagger.Client // Keep reference to close connection if needed
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
	// If no endpoint is configured, use Dagger (default behavior)
	if p.config.Endpoint == "" {
		return p.getDaggerClientAndSchema(ctx)
	}

	// Configure custom GraphQL endpoint
	return p.getCustomClientAndSchema(ctx)
}

// getDaggerClientAndSchema provides the default Dagger client and schema
func (p *GraphQLClientProvider) getDaggerClientAndSchema(ctx context.Context) (graphql.Client, *introspection.Schema, error) {
	// Connect to Dagger
	dag, err := dagger.Connect(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to Dagger: %w", err)
	}

	// Store the connection for cleanup
	p.daggerConn = dag

	client := dag.GraphQLClient()

	// Introspect the schema
	schema, err := introspectSchema(ctx, client)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to introspect Dagger schema: %w", err)
	}

	return client, schema, nil
}

// getCustomClientAndSchema provides a client and schema for a custom GraphQL endpoint
func (p *GraphQLClientProvider) getCustomClientAndSchema(ctx context.Context) (graphql.Client, *introspection.Schema, error) {
	// Create HTTP client with custom headers
	httpClient := &http.Client{}

	// Create custom transport to add headers
	transport := &customTransport{
		base:          http.DefaultTransport,
		authorization: p.config.Authorization,
		headers:       p.config.Headers,
	}
	httpClient.Transport = transport

	// Create GraphQL client with custom endpoint
	client := graphql.NewClient(p.config.Endpoint, httpClient)

	// Try to load from cache first
	cachedSchema, err := loadCachedSchema(p.config.Endpoint)
	if err == nil && cachedSchema != nil {
		// Cache hit!
		return client, cachedSchema, nil
	}

	// Cache miss or error - perform introspection
	schema, err := introspectSchema(ctx, client)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to introspect schema from %s: %w", p.config.Endpoint, err)
	}

	// Save to cache for future use (best effort, ignore errors)
	_ = saveCachedSchema(p.config.Endpoint, schema)

	return client, schema, nil
}

// getCacheDir returns the directory for schema caches
func getCacheDir() string {
	// Try XDG_CACHE_HOME first
	if cacheHome := os.Getenv("XDG_CACHE_HOME"); cacheHome != "" {
		return filepath.Join(cacheHome, "dang", "schemas")
	}
	// Fall back to ~/.cache
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cache", "dang", "schemas")
	}
	// Last resort: temp dir
	return filepath.Join(os.TempDir(), "dang-cache", "schemas")
}

// getCacheKey generates a cache key for an endpoint
func getCacheKey(endpoint string) string {
	h := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(h[:])
}

// loadCachedSchema attempts to load a cached schema for the given endpoint
func loadCachedSchema(endpoint string) (*introspection.Schema, error) {
	cacheDir := getCacheDir()
	cacheFile := filepath.Join(cacheDir, getCacheKey(endpoint)+".json")

	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, err // Cache miss
	}

	var cached schemaCache
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, err
	}

	// Verify endpoint matches (paranoid check)
	if cached.Endpoint != endpoint {
		return nil, fmt.Errorf("cache corruption: endpoint mismatch")
	}

	return cached.Schema, nil
}

// saveCachedSchema saves a schema to the cache
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

// clearSchemaCache removes all cached schemas
func clearSchemaCache() error {
	cacheDir := getCacheDir()
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		// Cache directory doesn't exist, nothing to clear
		return nil
	}

	// Remove all files in the cache directory
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

// ClearSchemaCache is a public function to clear the schema cache
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
	// Clone the request to avoid modifying the original
	req = req.Clone(req.Context())

	// Add authorization header if provided
	if t.authorization != "" {
		req.Header.Set("Authorization", t.authorization)
	}

	// Add custom headers
	for key, value := range t.headers {
		req.Header.Set(key, value)
	}

	return t.base.RoundTrip(req)
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

	// Parse additional headers from environment variables
	// Format: DANG_GRAPHQL_HEADER_<NAME>=<value>
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

// Close closes any open connections (e.g., Dagger connections)
func (p *GraphQLClientProvider) Close() error {
	if p.daggerConn != nil {
		return p.daggerConn.Close()
	}
	return nil
}
