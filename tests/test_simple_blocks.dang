# Test simple block expressions

# Block with single expression
pub single_block = { 42 }
assert { single_block == 42 }

# Block with let expression
pub let_block = { let x = 10 , x }
assert { let_block == 10 }

# Block with conditional
pub cond_block = { if true { "yes" } else { "no" } }
assert { cond_block == "yes" }

print("Simple block tests passed!")
