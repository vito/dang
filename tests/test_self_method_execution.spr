# Regression test for the original apko self issue
# This reproduces the exact issue: self not found during method execution

type SimpleApko {
  pub config = {{
    items: []
  }}

  # This method uses self and should fail if self is not available during method execution
  pub withItems(items: [String!]!): SimpleApko! {
    self.config.items += items
    self
  }
}

# Test the pattern that was failing: accessing self within method body during execution
assert { SimpleApko.withItems(["test"]).config.items == ["test"] }
assert { SimpleApko.withItems(["a"]).withItems(["b"]).config.items == ["a", "b"] }