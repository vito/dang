# Test nested field assignment in class methods with Fork() semantics

type Container {
  pub data = {{a: {{b: {{c: 42}}}}}}  # Let type be inferred
  
  # Test: nested field assignment should work with Fork() semantics
  pub updateNested(newValue: Int!): Container! {
    data.a.b.c = newValue  # No self. prefix - should clone data, data.a, data.a.b and update c
    self
  }
  
  # Test: compound assignment on nested fields
  pub incrementNested: Container! {
    data.a.b.c += 10  # No self. prefix
    self
  }
  
  # Test: multiple nested updates in same method
  pub multipleUpdates: Container! {
    data.a.b.c = 100   # No self. prefix
    data.a.b.c += 50   # Should result in 150
    self
  }
}

# Create original with nested structure (uses default)
let original = Container

# Test 1: Basic nested field assignment
let modified1 = original.updateNested(99)
assert("nested field updated in copy") { modified1.data.a.b.c == 99 }
assert("original unchanged") { original.data.a.b.c == 42 }

# Test 2: Compound assignment on nested fields  
let modified2 = original.incrementNested
assert("nested compound assignment works") { modified2.data.a.b.c == 52 }
assert("original still unchanged") { original.data.a.b.c == 42 }

# Test 3: Multiple updates in same method
let modified3 = original.multipleUpdates
assert("multiple nested updates work") { modified3.data.a.b.c == 150 }
assert("original still unchanged") { original.data.a.b.c == 42 }

# Test 4: Chained method calls with nested updates
let chained = original.updateNested(200).incrementNested
assert("chained calls with nested updates") { chained.data.a.b.c == 210 }
assert("original still unchanged") { original.data.a.b.c == 42 }

# Test 5: Verify copy-on-write creates proper clones
let copy1 = original.updateNested(111)
let copy2 = original.updateNested(222)
assert("independent copies") { copy1.data.a.b.c == 111 }
assert("independent copies") { copy2.data.a.b.c == 222 }
assert("original unchanged") { original.data.a.b.c == 42 }

print("Nested field assignment tests passed!")