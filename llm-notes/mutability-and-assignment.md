# Mutability and Assignment in Sprout

## Overview
Sprout implements a **copy-on-write** mutability model where variables and objects can be reassigned, but modifications create copies rather than mutating the original data structures in place.

## Assignment Operators

### Grammar Rules
From `pkg/sprout/sprout.peg`, Sprout supports two assignment operators:
- `=` (simple assignment)
- `+=` (compound assignment for addition)

```peg
Reassignment <- target:Term _ op:AssignOp _ value:Form
AssignOp <- PlusEqualToken { return "+", nil } / EqualToken { return "=", nil }
```

### Supported Assignment Patterns

#### 1. Simple Variable Assignment (`=`)
```sprout
pub x = 42
x = 100           # Reassigns x to 100
```

#### 2. Compound Assignment (`+=`)
```sprout
pub x = 5
x += 3            # x becomes 8

pub msg = "hello"
msg += " world"   # msg becomes "hello world"

pub nums = [1, 2]
nums += [3, 4]    # nums becomes [1, 2, 3, 4]
```

#### 3. Field Assignment
```sprout
pub obj = {{a: {{b: {{c: 42}}}}}}
obj.a.b.c = 100   # Nested field assignment
obj.a.b.c += 50   # Compound assignment on fields
```

#### 4. Class Field Assignment
```sprout
type MyClass {
  pub val = 1

  pub incr: MyClass! {
    val += 1        # No self. prefix needed (uses Fork() semantics)
    self
  }
  
  pub incrExplicit: MyClass! {
    self.val += 1   # self. prefix still works and is explicit
    self
  }
}
```

## Copy-on-Write Semantics

### Key Behavior
Objects are **copied** when modified, not mutated in place. This provides:
- **Immutability guarantees**: Original references remain unchanged
- **Safe concurrency**: No shared mutable state
- **Predictable behavior**: Modifications don't affect other references

### Example from `test_reassignment.bd`:
```sprout
let original = {{a: {{b: {{c: 1}}}}}}
let modified = original
modified.a.b.c = 2

assert { original.a.b.c == 1 }  # Original unchanged
assert { modified.a.b.c == 2 }  # Modified copy
```

### Block Scoping
Block scoping in Sprout follows these rules:
- If a block **doesn't declare** a local variable, reassignment updates the outer slot
- If a block **declares** a local variable, it shadows the outer variable (normal scoping)

```sprout
pub x = 100
{
  x = 200         # No local declaration, updates outer slot
  assert { x == 200 }
}
assert { x == 200 }  # Outer slot was updated

pub y = 300
{
  let y = 400     # Local declaration shadows outer
  y = 500         # Updates local shadow
  assert { y == 500 }
}
assert { y == 300 }  # Outer slot unchanged
```

## Supported Types for Assignment

### Compound Assignment (`+=`) supports:
- **Integers**: `5 += 3` → `8`
- **Strings**: `"hello" += " world"` → `"hello world"`
- **Lists**: `[1, 2] += [3, 4]` → `[1, 2, 3, 4]`

### Simple Assignment (`=`) supports:
- All primitive types (Int, String, Boolean, Null)
- Objects and nested structures
- Lists and complex data structures

## Implementation Details

### AST Structure
```go
type Reassignment struct {
  Target   Node   // Left-hand side (Symbol, Select, etc.)
  Modifier string // "=" or "+"
  Value    Node   // Right-hand side expression
  Loc      *SourceLocation
}
```

### Evaluation Process
1. **Variable Assignment**: Uses `Reassign()` to respect scoping rules
2. **Field Assignment**:
   - Clones the root object  
   - Traverses the path, cloning intermediate objects
   - Updates the final field
   - Stores the new root in the environment
3. **Class Method Assignment**: Uses `Fork()` to create execution boundary that prevents mutation of original object

### Scoping Mechanisms
Sprout uses two distinct scoping mechanisms:

1. **Lexical Scoping (`Clone()`)**: For blocks and function calls
   - Creates new scope frame in scope chain
   - Child can read from parent
   - Assignments follow scoping rules via `Reassign()`

2. **Execution Isolation (`Fork()`)**: For method calls on objects
   - Creates execution boundary to prevent mutation
   - Child can read from parent but assignments stay local
   - Marked with `IsForked` flag to prevent parent mutation

### Type Checking
- Simple assignment checks type compatibility
- Compound assignment validates addition compatibility
- All assignments must maintain type safety

## Patterns and Best Practices

### 1. Fluent Interface Pattern
```sprout
type Apko {
  pub withPackages(packages: [String!]!): Apko! {
    self.config.contents.packages += packages
    self                        # Return self for chaining
  }
}
```

### 2. Builder Pattern
```sprout
type MyClass {
  pub name: String! = "Jeff"

  pub withName(name: String!): MyClass! {
    self.name = name  # self. prefix needed to avoid parameter shadowing
    self
  }
}
```

### 3. Accumulator Pattern
```sprout
pub counter = 0
counter += 1
counter += 2
counter += 3
# counter is now 6
```

## Constraints and Limitations

### 1. Class Field Assignment Flexibility
Class field assignment supports both prefixed and unprefixed syntax:
```sprout
# Both are valid and equivalent when no shadowing occurs
field = value       # Uses Fork() semantics, no self. needed
self.field = value  # Explicit self. prefix also works

# When parameter names shadow field names, use self. prefix
pub withName(name: String!): MyClass! {
  self.name = name  # Required: parameter 'name' shadows field 'name'
}
```

### 2. Assignment Target Types
Only these target types are supported:
- `Symbol` (simple variables)
- `Select` (field access like `obj.field`)

### 3. Compound Assignment Operators
Currently only `+=` is supported for compound assignment. Other operators like `-=`, `*=`, etc. are not implemented.

## Error Handling
- **Type mismatches**: Compile-time type checking prevents invalid assignments
- **Undefined variables**: Runtime error if trying to assign to non-existent variable
- **Unsupported operations**: Runtime error for unsupported compound operations

## Examples from Codebase

### Real-world Usage (apko.bd)
```sprout
pub withAlpine(branch: String! = "edge"): Apko! {
  self.config.contents.packages += ["apk-tools"]
  self.config.contents.repositories += [
    ("https://dl-cdn.alpinelinux.org/alpine/" + branch) + "/main"
  ]
  self
}
```

### Test Examples
- `test_reassignment.bd`: Basic assignment patterns
- `test_plus_equals.bd`: Compound assignment with various types
- `test_self.bd`: Class field reassignment patterns  
- `test_self_method_execution.bd`: Method chaining with reassignment
- `test_block_scoping.bd`: Block scoping with outer slot reassignment
- `test_class_desired_behavior.bd`: Class methods without self prefix
- `test_class_immutability.bd`: Fork() semantics for class methods

### Class Method Assignment Examples
```sprout
type Counter {
  pub value: Int!
  
  pub incr: Counter! {
    value += 1    # Works without self. prefix
    self
  }
}

# Usage preserves immutability
let original = Counter(42)
let modified = original.incr
assert { original.value == 42 }  # Original unchanged
assert { modified.value == 43 }  # Modified copy

# Chain calls work naturally
assert { Counter(10).incr.incr.value == 12 }
```
