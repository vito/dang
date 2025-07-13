package gqlserver

import "time"

// Mock data store
var users []*User
var posts []*Post

func init() {
	// Initialize users first
	users = []*User{
		{ID: "1", Name: "John Doe", Emails: []string{"john@example.com", "john.doe@work.com"}, Age: func(i int) *int { return &i }(30)},
		{ID: "2", Name: "Jane Smith", Emails: []string{"jane@example.com"}, Age: func(i int) *int { return &i }(25)},
	}

	// Initialize posts with user references
	posts = []*Post{
		{ID: "1", Title: "First Post", Content: "Hello World!", Author: users[0], CreatedAt: "2024-01-01T00:00:00Z"},
		{ID: "2", Title: "Second Post", Content: "GraphQL is awesome!", Author: users[1], CreatedAt: "2024-01-02T00:00:00Z"},
	}

	// Populate the Posts field for each user
	for _, user := range users {
		for _, post := range posts {
			if post.Author.ID == user.ID {
				user.Posts = append(user.Posts, post)
			}
		}
	}
}

var serverStartTime = time.Now()