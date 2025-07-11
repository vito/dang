package sprout

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadGraphQLConfig(t *testing.T) {
	// Save original env vars
	originalEndpoint := os.Getenv("SPROUT_GRAPHQL_ENDPOINT")
	originalAuth := os.Getenv("SPROUT_GRAPHQL_AUTHORIZATION")
	originalHeader := os.Getenv("SPROUT_GRAPHQL_HEADER_X_API_KEY")

	defer func() {
		// Restore original env vars
		os.Setenv("SPROUT_GRAPHQL_ENDPOINT", originalEndpoint)
		os.Setenv("SPROUT_GRAPHQL_AUTHORIZATION", originalAuth)
		os.Setenv("SPROUT_GRAPHQL_HEADER_X_API_KEY", originalHeader)
	}()

	t.Run("default config", func(t *testing.T) {
		// Clear env vars
		os.Unsetenv("SPROUT_GRAPHQL_ENDPOINT")
		os.Unsetenv("SPROUT_GRAPHQL_AUTHORIZATION")
		os.Unsetenv("SPROUT_GRAPHQL_HEADER_X_API_KEY")

		config := LoadGraphQLConfig()

		assert.Empty(t, config.Endpoint)
		assert.Empty(t, config.Authorization)
		assert.Empty(t, config.Headers)
	})

	t.Run("config from env vars", func(t *testing.T) {
		os.Setenv("SPROUT_GRAPHQL_ENDPOINT", "https://api.example.com/graphql")
		os.Setenv("SPROUT_GRAPHQL_AUTHORIZATION", "Bearer token123")
		os.Setenv("SPROUT_GRAPHQL_HEADER_X_API_KEY", "secret-key")

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
