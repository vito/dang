---
name: dang-tests
description: Write and run Dang language tests. Use when creating test files, debugging test failures, or needing to understand Dang test syntax.
---

# Dang Tests

Tests live in `tests/test_*.dang`. Run them with the `test:language` check.

## Syntax Quick Reference

Study the grammar at `pkg/dang/dang.peg` and existing tests before writing Dang code. Key patterns:

```dang
# Comments start with #

# Bindings (immutable by default)
pub name = "hello"
pub count = 5
pub flag = true

# Assertions
assert { count == 5 }
assert { name == "hello" }

# Method calls (zero-param methods don't need parens)
pub upper = "hello".toUpper
pub parts = "a,b,c".split(",")

# Function calls
print("test passed!")

# Chaining
pub result = "  hello  ".trim.toUpper

# Lists
pub items = ["a", "b", "c"]
assert { items.length == 3 }

# Conditionals
pub x = if flag { "yes" } else { "no" }

# Functions
pub add = fn(a: Int!, b: Int!): Int! { a + b }
```

## Conventions

- File naming: `tests/test_<feature>.dang`
- Use `pub` for top-level bindings
- End with a `print("Description tests passed!")` line
- Use two-space indentation
- Test edge cases (empty strings, zero values, etc.)
