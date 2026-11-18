package gqlserver

import (
	"sync"
	"time"
)

// Resolver owns one server's data. Its zero value is ready to use.
// It must not be copied after first use.
type Resolver struct {
	mu   sync.Mutex
	data *dataSnapshot
}

// Published snapshots, including their slices, users and posts, are immutable:
// gqlgen serializes returned objects after the resolver releases its lock.
// Mutations replace objects (and posts referencing them), never edit them.
type dataSnapshot struct {
	users      []*User
	posts      []*Post
	nextUserID int
	startTime  time.Time
}

// dataLocked lazily initializes even a zero-value Resolver. The caller holds mu.
func (r *Resolver) dataLocked() *dataSnapshot {
	if r.data == nil {
		r.data = initialData()
	}
	return r.data
}

func (r *Resolver) snapshot() *dataSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dataLocked()
}

// IDs are opaque global IDs, "<Type>:<key>", unique across every type that
// implements Node (the Relay convention), so that node(id:) can tell a User
// from a Post with the same key.

func initialData() *dataSnapshot {
	// Initialize users first
	users := []*User{
		{ID: "User:1", Name: "John Doe", Emails: []string{"john@example.com", "john.doe@work.com"}, Age: func(i int) *int { return &i }(30), Status: StatusActive},
		{ID: "User:2", Name: "Jane Smith", Emails: []string{"jane@example.com"}, Age: func(i int) *int { return &i }(25), Status: StatusPending},
	}

	// Initialize posts with user references - adding more posts to test pagination
	posts := []*Post{
		{ID: "Post:1", Title: "First Post", Content: "Hello World!", Author: users[0], CreatedAt: "2024-01-01T00:00:00Z"},
		{ID: "Post:2", Title: "Second Post", Content: "GraphQL is awesome!", Author: users[1], CreatedAt: "2024-01-02T00:00:00Z"},
		{ID: "Post:3", Title: "Third Post", Content: "Learning GraphQL pagination", Author: users[0], CreatedAt: "2024-01-03T00:00:00Z"},
		{ID: "Post:4", Title: "Fourth Post", Content: "Cursor-based pagination is cool", Author: users[0], CreatedAt: "2024-01-04T00:00:00Z"},
		{ID: "Post:5", Title: "Fifth Post", Content: "Testing connections", Author: users[1], CreatedAt: "2024-01-05T00:00:00Z"},
		{ID: "Post:6", Title: "Sixth Post", Content: "More content for pagination", Author: users[0], CreatedAt: "2024-01-06T00:00:00Z"},
	}

	return &dataSnapshot{
		users:      users,
		posts:      posts,
		nextUserID: len(users) + 1,
		startTime:  time.Now(),
	}
}

func copyAge(age *int) *int {
	if age == nil {
		return nil
	}
	value := *age
	return &value
}
