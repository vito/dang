# Test constructor with arguments

type WithArgs {
  pub name: String! = "default"
  pub value: Int! = 0
}

# Test default values work
assert { WithArgs.name == "default" }
assert { WithArgs.value == 0 }

# Test calling with arguments
# Let's try calling it and see what happens
assert { WithArgs("custom").name == "custom" }

print("Constructor args tests passed!")