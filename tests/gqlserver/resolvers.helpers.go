package gqlserver

import "strings"

// paginatePosts implements cursor-based pagination for posts using Post ID as cursor
func paginatePosts(posts []*Post, first *int, after *string, last *int, before *string) (*PostConnection, error) {
	if len(posts) == 0 {
		return &PostConnection{
			Posts: []*Post{},
			PageInfo: &PageInfo{
				HasNextPage:     false,
				HasPreviousPage: false,
				StartCursor:     nil,
				EndCursor:       nil,
			},
		}, nil
	}

	startIndex := 0
	endIndex := len(posts) - 1

	// Handle 'after' cursor (using Post ID)
	if after != nil {
		for i, post := range posts {
			if post.ID == *after {
				startIndex = i + 1
				break
			}
		}
	}

	// Handle 'before' cursor (using Post ID)
	if before != nil {
		for i, post := range posts {
			if post.ID == *before {
				endIndex = i - 1
				break
			}
		}
	}

	// Apply bounds
	if startIndex > endIndex || startIndex >= len(posts) {
		return &PostConnection{
			Posts: []*Post{},
			PageInfo: &PageInfo{
				HasNextPage:     false,
				HasPreviousPage: false,
				StartCursor:     nil,
				EndCursor:       nil,
			},
		}, nil
	}

	if endIndex >= len(posts) {
		endIndex = len(posts) - 1
	}

	// Handle 'first' parameter
	if first != nil && *first >= 0 {
		if startIndex+*first-1 < endIndex {
			endIndex = startIndex + *first - 1
		}
	}

	// Handle 'last' parameter
	if last != nil && *last >= 0 {
		if endIndex-*last+1 > startIndex {
			startIndex = endIndex - *last + 1
		}
	}

	// Get the posts slice
	var resultPosts []*Post
	for i := startIndex; i <= endIndex; i++ {
		resultPosts = append(resultPosts, posts[i])
	}

	// Calculate page info
	hasNextPage := endIndex < len(posts)-1
	hasPreviousPage := startIndex > 0

	var startCursor, endCursor *string
	if len(resultPosts) > 0 {
		startCursor = &resultPosts[0].ID
		endCursor = &resultPosts[len(resultPosts)-1].ID
	}

	return &PostConnection{
		Posts: resultPosts,
		PageInfo: &PageInfo{
			HasNextPage:     hasNextPage,
			HasPreviousPage: hasPreviousPage,
			StartCursor:     startCursor,
			EndCursor:       endCursor,
		},
	}, nil
}

// findNodeByID finds a node (User or Post) by ID
func (r *queryResolver) findNodeByID(id string) (Node, error) {
	// Try to find a user first
	for _, user := range users {
		if user.ID == id {
			return user, nil
		}
	}
	// Try to find a post
	for _, post := range posts {
		if post.ID == id {
			return post, nil
		}
	}
	return nil, nil
}

// getAllNodes returns all nodes (Users and Posts)
func (r *queryResolver) getAllNodes() []Node {
	var nodes []Node
	for _, user := range users {
		nodes = append(nodes, user)
	}
	for _, post := range posts {
		nodes = append(nodes, post)
	}
	return nodes
}

func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) && strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// getAllTimestamped returns all timestamped objects (Posts)
func (r *queryResolver) getAllTimestamped() []Timestamped {
	var timestamped []Timestamped
	for _, post := range posts {
		timestamped = append(timestamped, post)
	}
	return timestamped
}
