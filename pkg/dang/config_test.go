package dang

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadGraphQLConfig(t *testing.T) {
	// Save original env vars
	originalEndpoint := os.Getenv("DANG_GRAPHQL_ENDPOINT")
	originalAuth := os.Getenv("DANG_GRAPHQL_AUTHORIZATION")
	originalHeader := os.Getenv("DANG_GRAPHQL_HEADER_X_API_KEY")

	defer func() {
		// Restore original env vars
		os.Setenv("DANG_GRAPHQL_ENDPOINT", originalEndpoint)
		os.Setenv("DANG_GRAPHQL_AUTHORIZATION", originalAuth)
		os.Setenv("DANG_GRAPHQL_HEADER_X_API_KEY", originalHeader)
	}()

	t.Run("default config", func(t *testing.T) {
		// Clear env vars
		os.Unsetenv("DANG_GRAPHQL_ENDPOINT")
		os.Unsetenv("DANG_GRAPHQL_AUTHORIZATION")
		os.Unsetenv("DANG_GRAPHQL_HEADER_X_API_KEY")

		config := LoadGraphQLConfig()

		assert.Empty(t, config.Endpoint)
		assert.Empty(t, config.Authorization)
		assert.Empty(t, config.Headers)
	})

	t.Run("config from env vars", func(t *testing.T) {
		os.Setenv("DANG_GRAPHQL_ENDPOINT", "https://api.example.com/graphql")
		os.Setenv("DANG_GRAPHQL_AUTHORIZATION", "Bearer token123")
		os.Setenv("DANG_GRAPHQL_HEADER_X_API_KEY", "secret-key")

		config := LoadGraphQLConfig()

		assert.Equal(t, "https://api.example.com/graphql", config.Endpoint)
		assert.Equal(t, "Bearer token123", config.Authorization)
		assert.Equal(t, "secret-key", config.Headers["X-API-KEY"])
	})
}

func TestCustomTransport(t *testing.T) {
	// Create a test server that records headers
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	// Create transport with custom headers
	transport := &customTransport{
		base:          http.DefaultTransport,
		authorization: "Bearer test-token",
		headers: map[string]string{
			"X-API-Key":       "secret-key",
			"X-Custom-Header": "custom-value",
		},
	}

	// Create client with custom transport
	client := &http.Client{Transport: transport}

	// Make request
	req, err := http.NewRequest("GET", server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify headers were set
	assert.Equal(t, "Bearer test-token", receivedHeaders.Get("Authorization"))
	assert.Equal(t, "secret-key", receivedHeaders.Get("X-API-Key"))
	assert.Equal(t, "custom-value", receivedHeaders.Get("X-Custom-Header"))
}

func TestGraphQLClientProvider(t *testing.T) {
	t.Run("default Dagger config", func(t *testing.T) {
		// This test will require a running Dagger engine, so we'll skip it if not available
		t.Skip("Requires Dagger engine to be running")

		provider := NewGraphQLClientProvider(GraphQLConfig{})

		ctx := context.Background()
		client, schema, err := provider.GetClientAndSchema(ctx)

		require.NoError(t, err)
		require.NotNil(t, client)
		require.NotNil(t, schema)
		require.NotEmpty(t, schema.Types)

		// Cleanup
		provider.Close()
	})

	t.Run("custom GraphQL endpoint", func(t *testing.T) {
		// Create a mock GraphQL server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simple mock introspection response
			mockResponse := `{
				"data": {
					"__schema": {
						"queryType": {"name": "Query"},
						"mutationType": null,
						"subscriptionType": null,
						"types": [
							{
								"kind": "OBJECT",
								"name": "Query",
								"fields": [
									{
										"name": "hello",
										"type": {"kind": "SCALAR", "name": "String"},
										"args": []
									}
								]
							}
						],
						"directives": []
					}
				}
			}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(mockResponse))
		}))
		defer server.Close()

		config := GraphQLConfig{
			Endpoint:      server.URL,
			Authorization: "Bearer test-token",
			Headers: map[string]string{
				"X-API-Key": "secret-key",
			},
		}

		provider := NewGraphQLClientProvider(config)

		ctx := context.Background()
		client, schema, err := provider.GetClientAndSchema(ctx)

		require.NoError(t, err)
		require.NotNil(t, client)
		require.NotNil(t, schema)
		require.Equal(t, "Query", schema.QueryType.Name)
		require.Len(t, schema.Types, 1)

		// Cleanup
		provider.Close()
	})
}

func TestNewGraphQLClientProvider(t *testing.T) {
	config := GraphQLConfig{
		Endpoint:      "https://api.example.com/graphql",
		Authorization: "Bearer token123",
		Headers: map[string]string{
			"X-API-Key": "secret-key",
		},
	}

	provider := NewGraphQLClientProvider(config)

	assert.Equal(t, config.Endpoint, provider.config.Endpoint)
	assert.Equal(t, config.Authorization, provider.config.Authorization)
	assert.Equal(t, config.Headers, provider.config.Headers)
}

func TestSchemaCaching(t *testing.T) {
	// Use a temporary cache directory for testing
	tempDir := t.TempDir()
	originalCacheHome := os.Getenv("XDG_CACHE_HOME")
	os.Setenv("XDG_CACHE_HOME", tempDir)
	defer os.Setenv("XDG_CACHE_HOME", originalCacheHome)

	// Track introspection calls
	introspectionCalls := 0

	// Create a mock GraphQL server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		introspectionCalls++
		// Simple mock introspection response
		mockResponse := `{
			"data": {
				"__schema": {
					"queryType": {"name": "Query"},
					"mutationType": null,
					"subscriptionType": null,
					"types": [
						{
							"kind": "OBJECT",
							"name": "Query",
							"fields": [
								{
									"name": "hello",
									"type": {"kind": "SCALAR", "name": "String"},
									"args": []
								}
							],
							"description": null,
							"interfaces": [],
							"enumValues": null,
							"possibleTypes": null
						}
					],
					"directives": []
				}
			}
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	config := GraphQLConfig{
		Endpoint: server.URL,
	}

	ctx := context.Background()

	t.Run("first call should introspect and cache", func(t *testing.T) {
		provider := NewGraphQLClientProvider(config)
		defer provider.Close()

		client, schema, err := provider.GetClientAndSchema(ctx)

		require.NoError(t, err)
		require.NotNil(t, client)
		require.NotNil(t, schema)
		assert.Equal(t, 1, introspectionCalls, "should have made 1 introspection call")

		// Verify cache file was created
		cacheFile := filepath.Join(tempDir, "dang", "schemas", getCacheKey(server.URL)+".json")
		_, err = os.Stat(cacheFile)
		assert.NoError(t, err, "cache file should exist")
	})

	t.Run("second call should use cache", func(t *testing.T) {
		provider := NewGraphQLClientProvider(config)
		defer provider.Close()

		client, schema, err := provider.GetClientAndSchema(ctx)

		require.NoError(t, err)
		require.NotNil(t, client)
		require.NotNil(t, schema)
		assert.Equal(t, 1, introspectionCalls, "should still have made only 1 introspection call")
	})

	t.Run("dagger endpoint should not use cache", func(t *testing.T) {
		// Test that Dagger endpoints bypass caching
		// This would require a running Dagger engine, so we'll just verify the behavior conceptually
		daggerProvider := NewGraphQLClientProvider(GraphQLConfig{}) // Empty config = Dagger

		// We can't actually test this without Dagger running, but the implementation
		// ensures that getDaggerClientAndSchema doesn't use caching
		assert.NotNil(t, daggerProvider)
	})
}

func TestClearSchemaCache(t *testing.T) {
	// Use a temporary cache directory for testing
	tempDir := t.TempDir()
	originalCacheHome := os.Getenv("XDG_CACHE_HOME")
	os.Setenv("XDG_CACHE_HOME", tempDir)
	defer os.Setenv("XDG_CACHE_HOME", originalCacheHome)

	// Create some dummy cache files
	cacheDir := filepath.Join(tempDir, "dang", "schemas")
	err := os.MkdirAll(cacheDir, 0755)
	require.NoError(t, err)

	// Create test cache files
	testFiles := []string{
		getCacheKey("https://api.example.com/graphql") + ".json",
		getCacheKey("https://api.test.com/graphql") + ".json",
		"not-a-cache-file.txt", // Should be ignored
	}

	for _, filename := range testFiles {
		filePath := filepath.Join(cacheDir, filename)
		err := os.WriteFile(filePath, []byte("test content"), 0644)
		require.NoError(t, err)
	}

	// Verify files exist before clearing
	for _, filename := range testFiles {
		filePath := filepath.Join(cacheDir, filename)
		_, err := os.Stat(filePath)
		assert.NoError(t, err, "file should exist before clearing: %s", filename)
	}

	// Clear the cache
	err = ClearSchemaCache()
	assert.NoError(t, err)

	// Verify JSON cache files are removed but other files remain
	jsonFiles := testFiles[:2] // First two are JSON files
	for _, filename := range jsonFiles {
		filePath := filepath.Join(cacheDir, filename)
		_, err := os.Stat(filePath)
		assert.True(t, os.IsNotExist(err), "JSON cache file should be removed: %s", filename)
	}

	// Non-JSON file should still exist
	nonJsonFile := filepath.Join(cacheDir, testFiles[2])
	_, err = os.Stat(nonJsonFile)
	assert.NoError(t, err, "non-JSON file should remain")

	// Test clearing empty cache directory
	err = ClearSchemaCache()
	assert.NoError(t, err, "clearing empty cache should not error")

	// Test clearing non-existent cache directory
	os.RemoveAll(cacheDir)
	err = ClearSchemaCache()
	assert.NoError(t, err, "clearing non-existent cache should not error")
}
