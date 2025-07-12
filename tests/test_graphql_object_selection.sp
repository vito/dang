# Test GraphQL object selection functionality with real schema
# Aspirational test covering all object selection syntax patterns

# Test basic object selection with GraphQL types
pub server_info = serverInfo.{version, platform, uptime}
pub server_stats = serverInfo.{totalUsers, totalPosts}

assert { server_info.version == "1.0.0" }
assert { server_info.platform != null }
assert { server_stats.totalUsers == 2 }
assert { server_stats.totalPosts == 2 }

# Test object selection with function calls and arguments
pub first_user = user(id: "1").{name, email, age}
pub user_posts = posts(authorId: "1", limit: 5).{title, content}

assert { first_user.name == "John Doe" }
assert { first_user.email == "john@example.com" }
assert { user_posts != null }

# Test nested object selection
pub profile_data = userProfile(userId: "1", includeStats: true).{
  user.{name, email}, 
  joinedDate, 
  bio,
  postCount,
  averagePostLength
}

assert { profile_data.user.name == "John Doe" }
assert { profile_data.joinedDate != null }
assert { profile_data.postCount != null }

# Test object selection with mixed field types
pub comprehensive_user = user(id: "1").{name, email, age, posts.{title, createdAt}}

assert { comprehensive_user.name != null }
assert { comprehensive_user.posts != null }

# Test object selection on lists
pub all_users_summary = users.{name, email}
pub post_summaries = posts.{title, author.{name}}

assert { all_users_summary != null }
assert { post_summaries != null }

# Test deeply nested object selection
pub deep_selection = userProfile(userId: "1").{
  user.{
    name,
    email,
    posts.{
      title,
      author.{name}
    }
  },
  joinedDate
}

assert { deep_selection.user.name != null }
assert { deep_selection.user.posts != null }

# Test object selection with optional fields
pub optional_selection = userProfile(userId: "1", includeStats: true).{
  user.{name, age},
  postCount,
  averagePostLength,
  bio
}

assert { optional_selection.user.name != null }
assert { optional_selection.postCount != null }

# Test mixed object selection and direct field access
pub mixed_data = serverInfo.{version, totalUsers}
pub direct_platform = serverInfo.platform

assert { mixed_data.version == "1.0.0" }
assert { direct_platform != null }

# Test object selection with aliasing-like patterns
pub server_overview = serverInfo.{version, platform, totalUsers, totalPosts}
pub user_overview = user(id: "1").{name, email}

assert { server_overview != null }
assert { user_overview != null }

# Test object selection on complex nested structures
pub complex_query = {
  server: serverInfo.{version, platform},
  users: users.{name, email},
  posts: posts.{title, author.{name}},
  titles: postTitles
}

assert { complex_query.server.version == "1.0.0" }
assert { complex_query.titles == ["First Post", "Second Post"] }

print("GraphQL object selection tests passed!")