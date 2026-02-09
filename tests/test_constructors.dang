# Test class constructors with various parameter configurations

# Basic constructor with no parameters
type Empty {
  pub val = null
}

assert { Empty.val == null }

# Constructor with required parameters
type Required {
  pub name: String!
  pub age: Int!
}

assert { Required("Alice", 25).name == "Alice" }
assert { Required("Alice", 25).age == 25 }

# Constructor with optional parameters (default values)
type WithDefaults {
  pub name: String! = "Unknown"
  pub age: Int! = 0
  pub active: Boolean! = true
}

assert { WithDefaults.name == "Unknown" }
assert { WithDefaults.age == 0 }
assert { WithDefaults.active == true }
assert { WithDefaults("Bob").name == "Bob" }
assert { WithDefaults("Bob").age == 0 }
assert { WithDefaults("Bob", 30).name == "Bob" }
assert { WithDefaults("Bob", 30).age == 30 }
assert { WithDefaults("Bob", 30, false).active == false }

# Constructor with mixed required and optional parameters
type Mixed {
  pub id: Int!
  pub name: String!
  pub description: String! = "No description"
  pub enabled: Boolean! = true
}

assert { Mixed(1, "Test").id == 1 }
assert { Mixed(1, "Test").name == "Test" }
assert { Mixed(1, "Test").description == "No description" }
assert { Mixed(1, "Test").enabled == true }
assert { Mixed(1, "Test", "Custom desc").description == "Custom desc" }
assert { Mixed(1, "Test", "Custom desc", false).enabled == false }

# Constructor with methods
type WithMethods {
  pub value: Int! = 42

  pub getValue: Int! {
    self.value
  }

  pub increment: WithMethods! {
    self.value += 1
    self
  }
}

assert { WithMethods.getValue == 42 }
assert { WithMethods.increment.getValue == 43 }
assert { WithMethods(100).getValue == 100 }

# Constructor where default values reference other parameters
type Computed {
  pub prefix: String! = "prefix"
  pub suffix: String! = "suffix"
  pub combined: String! = prefix + "_" + suffix
}

assert { Computed.combined == "prefix_suffix" }
assert { Computed("hello").combined == "hello_suffix" }
assert { Computed("hello", "world").combined == "hello_world" }

# Constructor with complex default values
type Complex {
  pub items: [String!]! = ["default"]
  pub config = {{
    enabled: true,
    count: 5
  }}
  pub computed: Int! = config.count * 2
}

assert { Complex.items == ["default"] }
assert { Complex.config.enabled == true }
assert { Complex.config.count == 5 }
assert { Complex.computed == 10 }

# Constructor with self-referencing default values
type SelfRef {
  pub name: String! = "test"
  pub greeting: String! = "Hello, " + name + "!"
}

assert { SelfRef.greeting == "Hello, test!" }
assert { SelfRef("Alice").greeting == "Hello, Alice!" }
assert { SelfRef("Alice", "What's up?").greeting == "What's up?" }

print("Constructor tests passed!")
