# Comprehensive test of working Bind language features

# =================== LITERALS ===================
pub number = 42
pub text = "hello world"
pub flag = true
pub nothing = null
pub numbers_list = [1, 2, 3, 4, 5]
pub words_list = ["apple", "banana", "cherry"]

assert { number == 42 }
assert { text == "hello world" }
assert { flag == true }
assert { nothing == null }
assert { numbers_list == [1, 2, 3, 4, 5] }
assert { words_list == ["apple", "banana", "cherry"] }

# =================== CONDITIONALS ===================
pub max_num = if number == 42 { number } else { 100 }
pub min_result = if false { "false branch" } else { "true branch" }
pub no_else_result = if true { "has value" }

assert { max_num == 42 }
assert { min_result == "true branch" }
assert { no_else_result == "has value" }

# =================== FUNCTIONS ===================
pub square(n: Int!): Int! { n }
pub greet(name: String!): String! { name }
pub zero_arity_func: String! { "constant_value" }

pub squared = square(n: 8)
pub greeting = greet(name: "Alice")
pub constant = zero_arity_func

assert { squared == 8 }
assert { greeting == "Alice" }
assert { constant == "constant_value" }

# =================== POSITIONAL ARGUMENTS ===================
pub add_nums(a: Int!, b: Int!): Int! { a }
pub mixed_result = add_nums(10, b: 20)
pub named_result = add_nums(a: 30, b: 40)

assert { mixed_result == 10 }
assert { named_result == 30 }

# =================== LAMBDA EXPRESSIONS ===================
pub identity_func = \x -> x
pub string_processor = \s -> s

# Note: Can't test lambda calls directly without function calling syntax
# But we can verify they can be assigned

# =================== DECLARATIONS WITH TYPES ===================
pub typed_int: Int! = 999
pub typed_string: String! = "typed"
let private_value = 123

assert { typed_int == 999 }
assert { typed_string == "typed" }
assert { private_value == 123 }

print("Comprehensive language feature test passed!")
