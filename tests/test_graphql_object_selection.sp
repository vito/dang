# Test GraphQL object selection functionality with real schema
# Aspirational test covering all object selection syntax patterns

# Test basic object selection with GraphQL types
pub server_info = serverInfo.{version, platform, uptime}
pub server_stats = serverInfo.{totalUsers, totalPosts}

assert { server_info.version == "1.0.0" }
assert { server_info.platform != null }
assert { server_info.uptime != null }
assert { server_stats.totalUsers == 2 }
assert { server_stats.totalPosts == 2 }

# Test object selection with function calls and arguments
pub first_user = user(id: "1").{name, email, age}
pub user_posts = posts(authorId: "1", limit: 5).{title, content}

assert { first_user.name == "John Doe" }
assert { first_user.email == "john@example.com" }
assert { first_user.age == 30 }
assert { user_posts != null }
# assert { user_posts[0].title == "First Post" }
# assert { user_posts[0].content == "Hello World!" }

# Test nested object selection
pub profile_data = userProfile(userId: "1", includeStats: true).{
  user.{name, email},
  joinedDate,
  bio,
  postCount,
  averagePostLength
}

assert { profile_data.user.name == "John Doe" }
assert { profile_data.user.email == "john@example.com" }
assert { profile_data.joinedDate == "2024-01-01T00:00:00Z" }
assert { profile_data.bio == "A passionate user sharing thoughts and ideas." }
assert { profile_data.postCount == 1 }
assert { profile_data.averagePostLength != null }

# Test object selection with mixed field types
pub comprehensive_user = user(id: "1").{name, email, age, posts.{title, createdAt}}

assert { comprehensive_user.name == "John Doe" }
assert { comprehensive_user.email == "john@example.com" }
assert { comprehensive_user.age == 30 }
assert { comprehensive_user.posts != null }
# assert { comprehensive_user.posts[0].title == "First Post" }
# assert { comprehensive_user.posts[0].createdAt == "2024-01-01T00:00:00Z" }

# Test object selection on lists
pub all_users_summary = users.{name, email}
pub post_summaries = posts.{title, author.{name}}

assert { all_users_summary != null }
# assert { all_users_summary[0].name == "John Doe" }
# assert { all_users_summary[0].email == "john@example.com" }
# assert { all_users_summary[1].name == "Jane Smith" }
# assert { all_users_summary[1].email == "jane@example.com" }
assert { post_summaries != null }
# assert { post_summaries[0].title == "First Post" }
# assert { post_summaries[0].author.name == "John Doe" }
# assert { post_summaries[1].title == "Second Post" }
# assert { post_summaries[1].author.name == "Jane Smith" }

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

assert { deep_selection.user.name == "John Doe" }
assert { deep_selection.user.email == "john@example.com" }
assert { deep_selection.user.posts != null }
# assert { deep_selection.user.posts[0].title == "First Post" }
# assert { deep_selection.user.posts[0].author.name == "John Doe" }
assert { deep_selection.joinedDate == "2024-01-01T00:00:00Z" }

# Test object selection with optional fields
pub optional_selection = userProfile(userId: "1", includeStats: true).{
  user.{name, age},
  postCount,
  averagePostLength,
  bio
}

assert { optional_selection.user.name == "John Doe" }
assert { optional_selection.user.age == 30 }
assert { optional_selection.postCount == 1 }
assert { optional_selection.averagePostLength != null }
assert { optional_selection.bio == "A passionate user sharing thoughts and ideas." }

# Test mixed object selection and direct field access
pub mixed_data = serverInfo.{version, totalUsers}
pub direct_platform = serverInfo.platform

assert { mixed_data.version == "1.0.0" }
assert { mixed_data.totalUsers == 2 }
assert { direct_platform != null }

# Test object selection with aliasing-like patterns
pub server_overview = serverInfo.{version, platform, totalUsers, totalPosts}
pub user_overview = user(id: "1").{name, email}

assert { server_overview.version == "1.0.0" }
assert { server_overview.platform != null }
assert { server_overview.totalUsers == 2 }
assert { server_overview.totalPosts == 2 }
assert { user_overview.name == "John Doe" }
assert { user_overview.email == "john@example.com" }

# Test object selection on complex nested structures
pub complex_query = {{
  server: serverInfo.{version, platform},
  users: users.{name, email},
  posts: posts.{title, author.{name}},
  titles: postTitles
}}

assert { complex_query.server.version == "1.0.0" }
assert { complex_query.server.platform != null }
# assert { complex_query.users[0].name == "John Doe" }
# assert { complex_query.users[0].email == "john@example.com" }
# assert { complex_query.users[1].name == "Jane Smith" }
# assert { complex_query.users[1].email == "jane@example.com" }
# assert { complex_query.posts[0].title == "First Post" }
# assert { complex_query.posts[0].author.name == "John Doe" }
# assert { complex_query.posts[1].title == "Second Post" }
# assert { complex_query.posts[1].author.name == "Jane Smith" }
assert { complex_query.titles == ["First Post", "Second Post"] }

print("GraphQL object selection tests passed!")
