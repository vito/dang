# Test GraphQLValue object selection functionality
# Tests the GraphQL query optimization for multi-field selection

# Create a mock GraphQL-like object to test object selection
type MockGraphQLObject {
    pub id: String! = "mock-id"
    pub name: String! = "mock-name"
    pub email: String! = "mock@example.com"
    pub platform: String! = "linux/amd64"
    pub status: String! = "active"
    
    pub new(id: String!, name: String!, email: String!): MockGraphQLObject! {
        self.id = id
        self.name = name
        self.email = email
        self
    }
}

# Test basic object selection that would be optimized for GraphQL
pub mock_graphql_api = MockGraphQLObject("test-id", "test-name", "test@example.com")

# Test multi-field selection - this should generate optimized queries for real GraphQL
pub api_info = mock_graphql_api.{id, name, email}

assert { api_info != null }
assert { api_info.id == "test-id" }
assert { api_info.name == "test-name" }
assert { api_info.email == "test@example.com" }

# Test mixed field and object selection
pub api_id = mock_graphql_api.id
pub api_summary = mock_graphql_api.{name, email}

assert { api_id == "test-id" }
assert { api_summary != null }
assert { api_summary.name == "test-name" }
assert { api_summary.email == "test@example.com" }

# Test with multiple object selection patterns
pub api_details = mock_graphql_api.{id, name, email, platform, status}

assert { api_details != null }
assert { api_details.id == "test-id" }
assert { api_details.name == "test-name" }
assert { api_details.email == "test@example.com" }
assert { api_details.platform == "linux/amd64" }
assert { api_details.status == "active" }

print("GraphQL object selection tests passed!")