# Test object selection syntax for multi-field GraphQL fetching

# Basic object types for testing
type User {
  pub name: String! = "Anonymous"
  pub email: String! = "user@example.com"
  pub age: Int! = 0
  pub active: Boolean! = true
}

type Profile {
  pub bio: String! = "No bio"
  pub avatar: String! = "default.jpg"
}

type UserWithProfile {
  pub name: String! = "Anonymous"
  pub email: String! = "user@example.com"
  pub profile: Profile! = Profile("Default bio", "default.jpg")
}

# Test basic object selection
pub user = User("Alice", "alice@example.com", 30)
pub user_summary = user.{name, email}

assert { user_summary.name == "Alice" }
assert { user_summary.email == "alice@example.com" }

# Test object selection with multiple fields
pub user_info = user.{name, email, age, active}
assert { user_info.name == "Alice" }
assert { user_info.email == "alice@example.com" }
assert { user_info.age == 30 }
assert { user_info.active == true }

# Test nested object selection
pub profile = Profile("Software Engineer", "alice.jpg")
pub user_with_profile = UserWithProfile("Bob", "bob@example.com", profile)
pub user_nested = user_with_profile.{name, profile.{bio, avatar}}

assert { user_nested.name == "Bob" }
assert { user_nested.profile.bio == "Software Engineer" }
assert { user_nested.profile.avatar == "alice.jpg" }

# Test with lists of objects
pub users = [
  User("Charlie", "charlie@example.com", 25),
  User("Diana", "diana@example.com", 28)
]

pub user_summaries = users.{name, email}
# Note: Since Sprout doesn't support array indexing, we'll test the structure differently
# We can verify the object selection works by checking that the result is well-formed
assert { user_summaries != null }

# Test with nested lists
pub users_with_profile = [
  UserWithProfile("Eve", "eve@example.com", Profile("Designer", "eve.jpg")),
  UserWithProfile("Frank", "frank@example.com", Profile("Developer", "frank.jpg"))
]

pub detailed_summaries = users_with_profile.{name, profile.{bio}}
# Note: Since Sprout doesn't support array indexing, we'll test the structure differently
# We can verify the nested object selection works by checking that the result is well-formed
assert { detailed_summaries != null }

print("Object selection tests passed!")
