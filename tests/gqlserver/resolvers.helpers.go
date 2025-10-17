package gqlserver

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
