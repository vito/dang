# Test variable declarations with different visibility and types

# Public variables (default)
pub public_var = 42
pub typed_var: Int! = 100
pub string_var: String! = "hello"

# Private variables
let private_var = 24
let private_typed: String! = "private"

# Test that variables have correct values
assert { public_var == 42 }
assert { typed_var == 100 }
assert { string_var == "hello" }
assert { private_var == 24 }
assert { private_typed == "private" }

# Type-only declarations (would need default values to test properly)
# pub type_only: Int!

print("Variable declaration tests passed!")
