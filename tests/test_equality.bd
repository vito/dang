# Test equality operator edge cases

# Basic equality
pub a = 42
pub b = 42
pub c = 43

assert { a == b }
assert { a == 42 }
assert { b == 42 }

# Different types should not be equal
pub num = 42
pub str = "42"
pub bool_val = true

# Cross-type comparisons should return false
assert("Number should not equal string") { (num == str) == false }

print("Equality edge case tests completed")