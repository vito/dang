# Test list concatenation with + operator

# Basic list concatenation with integers
assert { ([1, 2] + [3, 4]) == [1, 2, 3, 4] }

# Concatenation with empty list
assert { ([1, 2] + []) == [1, 2] }
assert { ([] + [3, 4]) == [3, 4] }
assert { ([] + []) == [] }

# String list concatenation
assert { (["hello", "world"] + ["foo", "bar"]) == ["hello", "world", "foo", "bar"] }

# Boolean list concatenation
assert { ([true, false] + [true]) == [true, false, true] }

# Single element lists
assert { ([1] + [2]) == [1, 2] }

# Nested list concatenation
assert { ([[1, 2], [3]] + [[4, 5]]) == [[1, 2], [3], [4, 5]] }

# Associativity test
assert { (([1] + [2]) + [3]) == [1, 2, 3] }
assert { ([1] + ([2] + [3])) == [1, 2, 3] }

print("List concatenation tests passed!")