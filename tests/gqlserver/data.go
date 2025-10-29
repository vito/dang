package gqlserver

import "time"

// Mock data store
var users []*User
var posts []*Post

func init() {
	// Initialize users first
	users = []*User{
		{ID: "1", Name: "John Doe", Emails: []string{"john@example.com", "john.doe@work.com"}, Age: func(i int) *int { return &i }(30), Status: StatusActive},
		{ID: "2", Name: "Jane Smith", Emails: []string{"jane@example.com"}, Age: func(i int) *int { return &i }(25), Status: StatusPending},
	}

	// Initialize posts with user references - adding more posts to test pagination
	posts = []*Post{
		{ID: "1", Title: "First Post", Content: "Hello World!", Author: users[0], CreatedAt: "2024-01-01T00:00:00Z"},
		{ID: "2", Title: "Second Post", Content: "GraphQL is awesome!", Author: users[1], CreatedAt: "2024-01-02T00:00:00Z"},
		{ID: "3", Title: "Third Post", Content: "Learning GraphQL pagination", Author: users[0], CreatedAt: "2024-01-03T00:00:00Z"},
		{ID: "4", Title: "Fourth Post", Content: "Cursor-based pagination is cool", Author: users[0], CreatedAt: "2024-01-04T00:00:00Z"},
		{ID: "5", Title: "Fifth Post", Content: "Testing connections", Author: users[1], CreatedAt: "2024-01-05T00:00:00Z"},
		{ID: "6", Title: "Sixth Post", Content: "More content for pagination", Author: users[0], CreatedAt: "2024-01-06T00:00:00Z"},
	}

	// Posts field is now handled by the resolver - no need to populate it here
}

var serverStartTime = time.Now()
