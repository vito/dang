# Test nullable propagation in ObjectSelection operations
# If foo is nullable, then foo.{bar, baz} should also be nullable

type TestRecord {
  pub name: String! = "test"
  pub id: Int! = 42
  pub active: Boolean! = true
}

type NestedRecord {
  pub inner: TestRecord! = TestRecord
  pub count: Int! = 10
}

# Test case 1: nullable object selection
pub maybeRecord: TestRecord = TestRecord
assert { maybeRecord.{name, id}.name == "test" }
assert { maybeRecord.{name, id}.id == 42 }

# Now set it to null and test that object selection propagates nullability
maybeRecord = null
# Test direct access (avoid phased evaluation)
assert { maybeRecord.{name, id} == null }
assert { maybeRecord.{name, active} == null }

# Test case 2: nested nullable object selection
pub maybeNested: NestedRecord = NestedRecord
assert { maybeNested.{inner, count}.inner.name == "test" }
assert { maybeNested.{inner, count}.count == 10 }

# When maybeNested is null, object selection should be null
maybeNested = null
assert { maybeNested.{inner, count} == null }

# Test case 3: nested field selections in object selection
type ChainRecord {
  pub level1: NestedRecord! = NestedRecord
  pub value: String! = "chain"
}

pub maybeChain: ChainRecord = ChainRecord
assert { maybeChain.{level1.{inner}, value}.value == "chain" }

# When maybeChain is null, nested object selection should be null
maybeChain = null
assert { maybeChain.{level1.{inner}, value} == null }

# Test case 4: object selection with multiple fields on nullable receiver
type ComplexRecord {
  pub field1: String! = "value1"
  pub field2: Int! = 100
  pub field3: Boolean! = false
}

pub maybeComplex: ComplexRecord = ComplexRecord
assert { maybeComplex.{field1, field2, field3}.field1 == "value1" }
assert { maybeComplex.{field1, field2, field3}.field2 == 100 }

maybeComplex = null
assert { maybeComplex.{field1, field2, field3} == null }