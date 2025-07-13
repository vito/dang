# Test error cases for class constructors

# This file tests various error conditions that should be caught
# by the type checker or runtime

# Test will be added here when we have a proper error testing framework
# For now, we'll focus on positive test cases

type ValidConstructor {
  pub name: String! = "test"
  pub value: Int! = 42
}

# Test that constructors work correctly
assert { ValidConstructor.name == "test" }
assert { ValidConstructor.value == 42 }
assert { ValidConstructor("custom").name == "custom" }
assert { ValidConstructor("custom", 100).value == 100 }

# Test constructor auto-calling with zero-arity constructors
type ZeroArity {
  pub message: String! = "auto-called"
}

# ZeroArity should be auto-called since all parameters have defaults
assert { ZeroArity.message == "auto-called" }

# Test that we can still call it explicitly
assert { ZeroArity().message == "auto-called" }

print("Constructor error tests passed!")