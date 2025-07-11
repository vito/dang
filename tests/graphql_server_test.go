package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/sprout/introspection"
	"github.com/vito/sprout/pkg/sprout"
)

// GraphQLRequest represents a GraphQL request
type GraphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

// GraphQLResponse represents a GraphQL response
type GraphQLResponse struct {
	Data   interface{} `json:"data,omitempty"`
	Errors []string    `json:"errors,omitempty"`
}

// TestGraphQLServer creates a test GraphQL server for testing object selection
func TestGraphQLServer(t *testing.T) {
	// Create a test server that tracks queries
	var receivedQueries []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
			return
		}

		var req GraphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Failed to decode request: %v", err)
			return
		}

		// Track the query
		receivedQueries = append(receivedQueries, req.Query)

		// Log the query for debugging
		log.Printf("Received GraphQL query: %s", req.Query)

		// Mock response based on query
		response := GraphQLResponse{
			Data: map[string]interface{}{
				"user": map[string]interface{}{
					"id":    "user123",
					"name":  "Test User",
					"email": "test@example.com",
					"profile": map[string]interface{}{
						"bio":    "Test bio",
						"avatar": "avatar.jpg",
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Set the GraphQL endpoint for Sprout
	os.Setenv("SPROUT_GRAPHQL_ENDPOINT", server.URL)
	defer os.Unsetenv("SPROUT_GRAPHQL_ENDPOINT")

	// Create a test Sprout script that uses object selection
	testScript := `
# Test current object selection implementation
pub user = dag.Container(
    platform: "linux/amd64",
    withExec: ["echo", "test"]
)

# Test multi-field selection - this should be optimized into a single query
pub user_info = user.{id, withExec}

assert { user_info != null }
print("GraphQL object selection test completed")
`

	// Write test script to temporary file
	testFile := "test_object_selection_real.sp"
	if err := os.WriteFile(testFile, []byte(testScript), 0644); err != nil {
		t.Fatalf("Failed to write test script: %v", err)
	}
	defer os.Remove(testFile)

	// Run the test script
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Note: This will fail because we don't have a real Dagger connection
	// But we can still observe the queries being made
	schema := &introspection.Schema{} // Mock schema
	err := sprout.RunFile(ctx, &mockGraphQLClient{server.URL}, schema, testFile, false)

	// Log what queries were received
	t.Logf("Received %d queries:", len(receivedQueries))
	for i, query := range receivedQueries {
		t.Logf("Query %d: %s", i+1, query)
	}

	// The test might fail due to missing Dagger setup, but we can observe the queries
	if err != nil {
		t.Logf("Expected error (no real Dagger connection): %v", err)
	}

	// Verify we received queries (even if they failed)
	if len(receivedQueries) == 0 {
		t.Error("No GraphQL queries were received - object selection may not be working")
	}

	// Check if we got the expected number of queries
	// Current implementation likely creates multiple queries instead of one optimized query
	if len(receivedQueries) > 1 {
		t.Logf("ISSUE: Received %d queries, expected 1 optimized query", len(receivedQueries))
		t.Logf("Current implementation is not optimizing multi-field selections")
	}
}

// mockGraphQLClient implements the graphql.Client interface for testing
type mockGraphQLClient struct {
	endpoint string
}

func (m *mockGraphQLClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	// This is a mock implementation - in real tests this would make HTTP requests
	// to our test server, but for now we'll just return an error to see the behavior
	return fmt.Errorf("mock client - endpoint: %s", m.endpoint)
}

// TestCurrentImplementationBehavior tests the current behavior to understand the issue
func TestCurrentImplementationBehavior(t *testing.T) {
	t.Log("Testing current object selection implementation...")

	// Create a minimal test to see what happens with object selection
	testScript := `
# Create a simple object to test selection
type TestObject {
    pub field1: String! = "value1"
    pub field2: String! = "value2"
    pub field3: String! = "value3"
}

pub obj = TestObject()

# Test object selection
pub selected = obj.{field1, field2}

assert { selected.field1 == "value1" }
assert { selected.field2 == "value2" }

print("Object selection test passed")
`

	testFile := "test_current_behavior.sp"
	if err := os.WriteFile(testFile, []byte(testScript), 0644); err != nil {
		t.Fatalf("Failed to write test script: %v", err)
	}
	defer os.Remove(testFile)

	// Run the test
	ctx := context.Background()
	schema := &introspection.Schema{}
	err := sprout.RunFile(ctx, &mockGraphQLClient{}, schema, testFile, false)

	if err != nil {
		t.Logf("Test result: %v", err)
	} else {
		t.Log("Object selection works for regular objects")
	}
}
