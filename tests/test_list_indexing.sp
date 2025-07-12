# Test list indexing functionality

# Basic indexing
pub numbers = [1, 2, 3, 4, 5]
assert { numbers[0] == 1 }
assert { numbers[1] == 2 }
assert { numbers[4] == 5 }

# String list indexing
pub words = ["hello", "world", "test"]
assert { words[0] == "hello" }
assert { words[1] == "world" }
assert { words[2] == "test" }

# Boolean list indexing
pub flags = [true, false, true]
assert { flags[0] == true }
assert { flags[1] == false }
assert { flags[2] == true }

# Nested list indexing
pub nested = [[1, 2], [3, 4], [5, 6]]
assert { nested[0] == [1, 2] }
assert { nested[1] == [3, 4] }
assert { nested[0][0] == 1 }
assert { nested[0][1] == 2 }
assert { nested[1][0] == 3 }
assert { nested[2][1] == 6 }

# Out of bounds access should return null
pub small_list = [1, 2]
assert { small_list[5] == null }
pub negative_index = 0 - 1
assert { small_list[negative_index] == null }

# Empty list indexing
pub empty = []
assert { empty[0] == null }

# Indexing with variables
pub index = 1
assert { numbers[index] == 2 }

# Indexing with expressions
assert { numbers[0 + 1] == 2 }
assert { numbers[2 - 1] == 2 }

print("List indexing tests passed!")