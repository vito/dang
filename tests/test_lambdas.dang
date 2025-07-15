# Simple lambda
pub identity = \x -> x
assert { identity(42) == 42 }
assert { identity("test") == "test" }

# Lambda with typed arguments
pub typed_identity = \(x: Int!) -> x
assert { typed_identity(123) == 123 }

pub typed_string_fn = \(s: String!) -> s
assert { typed_string_fn("hello") == "hello" }

# Lambda with multiple typed parameters
pub typed_add = \(x: Int!, y: Int!) -> x + y
assert { typed_add(3, 4) == 7 }
assert { typed_add(10, 20) == 30 }
