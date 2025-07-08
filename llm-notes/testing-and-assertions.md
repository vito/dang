# Testing and Assertions in Sprout

## Assert Syntax
Use `assert { condition }` to test expected behaviors in Sprout code:
```sprout
type Person(name: String!) {
  pub greet: String! {
    "Hello, I'm " + self.name
  }
}

# Test constructor and method calls
assert { Person("Alice").greet == "Hello, I'm Alice" }
assert { Person("Bob").name == "Bob" }
```

## Testing Patterns
- Place `assert` statements directly in test files
- Use descriptive variable names for complex expressions before asserting
- Test both positive cases and edge cases
- Assert expected return values, not just that code doesn't crash

## Directory Module Testing
The special `test_dir_module` test requires manual execution:
```bash
go run ./cmd/sprout ./tests/test_dir_module
```
This tests cross-file references and module boundaries, which the Go test suite doesn't cover.

## Test File Organization
- Individual `.sp` files in `/tests/` are picked up by Go test runner
- Use meaningful test file names like `test_constructor_syntax.sp`
- Keep tests focused on specific features
- Use comments to explain complex test scenarios
