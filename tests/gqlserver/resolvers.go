package gqlserver

// THIS CODE WILL BE UPDATED WITH SCHEMA CHANGES. PREVIOUS IMPLEMENTATION FOR SCHEMA CHANGES WILL BE KEPT IN THE COMMENT SECTION. IMPLEMENTATION FOR UNCHANGED SCHEMA WILL BE KEPT.

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"time"
)

type Resolver struct{}

// CreateUser is the resolver for the createUser field.
func (r *mutationResolver) CreateUser(ctx context.Context, input CreateUserInput) (*User, error) {
	newID := strconv.Itoa(len(users) + 1)
	user := &User{
		ID:     newID,
		Name:   input.Name,
		Emails: []string{input.Email},
		Age:    input.Age,
		Status: StatusActive,
	}
	users = append(users, user)
	return user, nil
}

// UpdateUser is the resolver for the updateUser field.
func (r *mutationResolver) UpdateUser(ctx context.Context, id string, input UpdateUserInput) (*User, error) {
	for _, user := range users {
		if user.ID == id {
			if input.Name != nil {
				user.Name = *input.Name
			}
			if input.Email != nil {
				user.Emails = []string{*input.Email}
			}
			if input.Age != nil {
				user.Age = input.Age
			}
			return user, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

// DeleteUser is the resolver for the deleteUser field.
func (r *mutationResolver) DeleteUser(ctx context.Context, id string) (bool, error) {
	for i, user := range users {
		if user.ID == id {
			users = append(users[:i], users[i+1:]...)
			return true, nil
		}
	}
	return false, fmt.Errorf("user not found")
}

// Hello is the resolver for the hello field.
func (r *queryResolver) Hello(ctx context.Context, name string) (string, error) {
	return fmt.Sprintf("Hello, %s!", name), nil
}

// Users is the resolver for the users field.
func (r *queryResolver) Users(ctx context.Context) ([]*User, error) {
	return users, nil
}

// User is the resolver for the user field.
func (r *queryResolver) User(ctx context.Context, id string) (*User, error) {
	for _, user := range users {
		if user.ID == id {
			return user, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

// ServerInfo is the resolver for the serverInfo field.
func (r *queryResolver) ServerInfo(ctx context.Context) (*ServerInfo, error) {
	uptime := time.Since(serverStartTime)
	return &ServerInfo{
		Version:    "1.0.0",
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
		Uptime:     uptime.String(),
		TotalUsers: len(users),
		TotalPosts: len(posts),
	}, nil
}

// Posts is the resolver for the posts field.
func (r *queryResolver) Posts(ctx context.Context, authorID *string, limit *int) ([]*Post, error) {
	result := posts

	// Filter by author ID if provided
	if authorID != nil {
		var filtered []*Post
		for _, post := range posts {
			if post.Author.ID == *authorID {
				filtered = append(filtered, post)
			}
		}
		result = filtered
	}

	// Apply limit if provided
	if limit != nil && *limit >= 0 && *limit < len(result) {
		result = result[:*limit]
	}

	return result, nil
}

// UserProfile is the resolver for the userProfile field.
func (r *queryResolver) UserProfile(ctx context.Context, userID *string, includeStats *bool) (*UserProfile, error) {
	var targetUser *User

	// If userID is provided, find that specific user
	if userID != nil {
		for _, user := range users {
			if user.ID == *userID {
				targetUser = user
				break
			}
		}
		if targetUser == nil {
			return nil, fmt.Errorf("user not found")
		}
	} else {
		// If no userID provided, return the first user as default
		if len(users) > 0 {
			targetUser = users[0]
		} else {
			return nil, fmt.Errorf("no users available")
		}
	}

	profile := &UserProfile{
		User:         targetUser,
		JoinedDate:   "2024-01-01T00:00:00Z",
		LastActivity: time.Now().Format(time.RFC3339),
		Bio:          func() *string { s := "A passionate user sharing thoughts and ideas."; return &s }(),
	}

	// Include statistics if requested
	if includeStats != nil && *includeStats {
		var postCount int
		var totalLength int

		for _, post := range posts {
			if post.Author.ID == targetUser.ID {
				postCount++
				totalLength += len(post.Content)
			}
		}

		profile.PostCount = &postCount
		if postCount > 0 {
			avgLength := float64(totalLength) / float64(postCount)
			profile.AveragePostLength = &avgLength
		}
	}

	return profile, nil
}

// PostTitles is the resolver for the postTitles field.
func (r *queryResolver) PostTitles(ctx context.Context) ([]string, error) {
	var titles []string
	for _, post := range posts {
		titles = append(titles, post.Title)
	}
	return titles, nil
}

// Status is the resolver for the status field.
func (r *queryResolver) Status(ctx context.Context) (Status, error) {
	return StatusActive, nil
}

// Now is the resolver for the now field.
func (r *queryResolver) Now(ctx context.Context) (string, error) {
	return "2024-01-15T10:30:00Z", nil
}

// Homepage is the resolver for the homepage field.
func (r *queryResolver) Homepage(ctx context.Context) (string, error) {
	return "https://dang-lang.dev", nil
}

// Posts is the resolver for the posts field.
func (r *userResolver) Posts(ctx context.Context, obj *User, first *int, after *string, last *int, before *string) (*PostConnection, error) {
	// Get all posts for this user
	var userPosts []*Post
	for _, post := range posts {
		if post.Author.ID == obj.ID {
			userPosts = append(userPosts, post)
		}
	}

	// Implement cursor-based pagination
	return paginatePosts(userPosts, first, after, last, before)
}

// Mutation returns MutationResolver implementation.
func (r *Resolver) Mutation() MutationResolver { return &mutationResolver{r} }

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

// User returns UserResolver implementation.
func (r *Resolver) User() UserResolver { return &userResolver{r} }

type mutationResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }
type userResolver struct{ *Resolver }

// !!! WARNING !!!
// The code below was going to be deleted when updating resolvers. It has been copied here so you have
// one last chance to move it out of harms way if you want. There are two reasons this happens:
//  - When renaming or deleting a resolver the old code will be put in here. You can safely delete
//    it when you're done.
//  - You have helper methods in this file. Move them out to keep these resolver files clean.
/*
	type Resolver struct{}
*/
