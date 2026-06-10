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
	"github.com/vito/dang/v2/pkg/introspection"
)

// schemaCache represents a cached GraphQL schema
type schemaCache struct {
	Schema    *introspection.Schema `json:"schema"`
	Timestamp time.Time             `json:"timestamp"`
	Endpoint  string                `json:"endpoint"`
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

// IntrospectSchema performs GraphQL introspection on the given client. When
// dagger is true, it uses Dagger's extended introspection query, which exposes
// directive applications on fields, enum values, and input values. Plain
// GraphQL endpoints (such as GitHub's) should pass false.
func IntrospectSchema(ctx context.Context, client graphql.Client, dagger bool) (*introspection.Schema, error) {
	return introspectSchema(ctx, client, dagger)
}

// introspectSchema performs GraphQL introspection on the given client. When
// dagger is true, it uses Dagger's extended introspection query; otherwise it
// uses a spec-compliant query that works against plain GraphQL endpoints.
func introspectSchema(ctx context.Context, client graphql.Client, dagger bool) (*introspection.Schema, error) {
	query := introspection.Query
	if dagger {
		query = introspection.DaggerQuery
	}

	var introspectionResp introspection.Response
	err := client.MakeRequest(ctx, &graphql.Request{
		Query:  query,
		OpName: "IntrospectionQuery",
	}, &graphql.Response{
		Data: &introspectionResp,
	})
	if err != nil {
		return nil, fmt.Errorf("introspection query failed: %w", err)
	}

	return introspectionResp.Schema, nil
}
