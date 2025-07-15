# Function with one default argument
pub reqWithDefault(str: String! = "defaulted"): String! {
  str + "!"
}

# Function with multiple default arguments
pub multipleDefaults(name: String! = "world", prefix: String! = "Hello"): String! {
  (prefix + " ") + (name + "!")
}

# Function with mixed required and optional arguments
pub mixedArgs(required: String!, optional: String! = "default"): String! {
  (required + "-") + optional
}

# Test auto-calling (property access without parentheses)
assert { reqWithDefault == "defaulted!" }

# Test explicit no-argument calls
assert { reqWithDefault() == "defaulted!" }

# Test explicit argument calls
assert { reqWithDefault("hey") == "hey!" }

# Test null handling - null gets replaced with default
assert { reqWithDefault(null) == "defaulted!" }

# Test multiple default arguments with auto-calling
assert { multipleDefaults == "Hello world!" }

# Test multiple defaults with explicit no-arg call
assert { multipleDefaults() == "Hello world!" }

# Test partial argument specification (positional)
assert { multipleDefaults("Alice") == "Hello Alice!" }

# Test all arguments specified (positional)
assert { multipleDefaults("Bob", "Hi") == "Hi Bob!" }

# Test mixed args with only required argument
assert { mixedArgs("test") == "test-default" }

# Test mixed args with both arguments
assert { mixedArgs("test", "custom") == "test-custom" }

# Test mixed args with null optional (should use default)
assert { mixedArgs("test", null) == "test-default" }

# Lambda default arguments testing
pub lambdaWithDefault = \(str: String! = "lambda-default") -> str + "!"

# Lambda with multiple defaults
pub lambdaMultipleDefaults = \(name: String! = "world", prefix: String! = "Hello") -> (prefix + " ") + (name + "!")

# Lambda with mixed required and optional arguments
pub lambdaMixed = \(required: String!, optional: String! = "optional-default") -> (required + "-") + optional

# Test lambda auto-calling (property access without parentheses)
assert { lambdaWithDefault == "lambda-default!" }

# Test lambda explicit no-argument calls
assert { lambdaWithDefault() == "lambda-default!" }

# Test lambda explicit argument calls
assert { lambdaWithDefault("hey") == "hey!" }

# Test lambda null handling - null gets replaced with default
assert { lambdaWithDefault(null) == "lambda-default!" }

# Test lambda multiple default arguments with auto-calling
assert { lambdaMultipleDefaults == "Hello world!" }

# Test lambda multiple defaults with explicit no-arg call
assert { lambdaMultipleDefaults() == "Hello world!" }

# Test lambda partial argument specification (positional)
assert { lambdaMultipleDefaults("Alice") == "Hello Alice!" }

# Test lambda all arguments specified (positional)
assert { lambdaMultipleDefaults("Bob", "Hi") == "Hi Bob!" }

# Test lambda mixed args with only required argument
assert { lambdaMixed("test") == "test-optional-default" }

# Test lambda mixed args with both arguments
assert { lambdaMixed("test", "custom") == "test-custom" }

# Test lambda mixed args with null optional (should use default)
assert { lambdaMixed("test", null) == "test-optional-default" }
