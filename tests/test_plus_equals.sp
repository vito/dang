# Test += operator

# Integer addition assignment
pub x = 5
x += 3
assert { x == 8 }

# String concatenation assignment
pub msg = "hello"
msg += " world"
assert { msg == "hello world" }

# List concatenation assignment
pub nums = [1, 2]
nums += [3, 4]
assert { nums == [1, 2, 3, 4] }

# Multiple += operations
pub counter = 0
counter += 1
counter += 2
counter += 3
assert { counter == 6 }

# Empty list +=
pub empty_list = []
empty_list += [1, 2]
assert { empty_list == [1, 2] }

# List += empty list
pub some_list = [1, 2]
some_list += []
assert { some_list == [1, 2] }

# String list concatenation assignment
pub words = ["hello"]
words += ["world", "test"]
assert { words == ["hello", "world", "test"] }

print("Plus equals tests passed!")