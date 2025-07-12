package gqlserver

import "time"

// Mock data store
var users = []*User{
	{ID: "1", Name: "John Doe", Email: "john@example.com", Age: func(i int) *int { return &i }(30)},
	{ID: "2", Name: "Jane Smith", Email: "jane@example.com", Age: func(i int) *int { return &i }(25)},
}

var posts = []*Post{
	{ID: "1", Title: "First Post", Content: "Hello World!", Author: users[0], CreatedAt: "2024-01-01T00:00:00Z"},
	{ID: "2", Title: "Second Post", Content: "GraphQL is awesome!", Author: users[1], CreatedAt: "2024-01-02T00:00:00Z"},
}

var serverStartTime = time.Now()