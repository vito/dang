# Test class method mutation behavior

type Counter {
  pub value: Int!
  
  pub incr: Counter! {
    self.value += 1
    self
  }
  
  pub add(amount: Int!): Counter! {
    self.value += amount
    self
  }
  
  pub getValue: Int! {
    self.value
  }
}

# Test 1: Basic increment
let counter1 = Counter(value: 42)
let counter2 = counter1.incr
assert("incr should return updated counter") { counter2.getValue == 43 }
assert("original counter should be unchanged") { counter1.getValue == 42 }

# Test 2: Chained method calls
let counter3 = Counter(value: 10)
let result = counter3.incr.incr.add(5).getValue
assert("chained calls should work") { result == 17 }
assert("original counter should be unchanged") { counter3.getValue == 10 }

# Test 3: Direct field access after method call
let counter4 = Counter(value: 20)
let updated = counter4.incr
assert("direct field access should work") { updated.value == 21 }
assert("original should be unchanged") { counter4.value == 20 }

# Test 4: The specific example from the issue
type Foo {
  pub a: Int!
  
  pub incr: Foo! {
    self.a += 1
    self
  }
}

let foo_result = Foo(42).incr.a
assert("Foo(42).incr.a should return 43") { foo_result == 43 }

print("Class mutation tests completed!")