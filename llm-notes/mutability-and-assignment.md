# Mutability and Assignment in Bind

## Overview
Bind implements a **copy-on-write** mutability model where variables and objects can be reassigned, but modifications create copies rather than mutating the original data structures in place.

## Assignment Operators

### Grammar Rules
From `pkg/bind/bind.peg`, Bind supports two assignment operators:
- `=` (simple assignment)
- `+=` (compound assignment for addition)

```peg
Reassignment <- target:Term _ op:AssignOp _ value:Form
AssignOp <- PlusEqualToken { return "+", nil } / EqualToken { return "=", nil }
```

### Supported Assignment Patterns

#### 1. Simple Variable Assignment (`=`)
```bind
pub x = 42
x = 100           # Reassigns x to 100
```

#### 2. Compound Assignment (`+=`)
```bind
pub x = 5
x += 3            # x becomes 8

pub msg = "hello"
msg += " world"   # msg becomes "hello world"

pub nums = [1, 2]
nums += [3, 4]    # nums becomes [1, 2, 3, 4]
```

#### 3. Field Assignment
```bind
pub obj = {{a: {{b: {{c: 42}}}}}}
obj.a.b.c = 100   # Nested field assignment
obj.a.b.c += 50   # Compound assignment on fields
```

#### 4. Self Assignment in Classes
```bind
type MyClass {
  pub val = 1

  pub incr: MyClass! {
    self.val += 1   # self. prefix required for reassignment
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
```bind
let original = {{a: {{b: {{c: 1}}}}}}
let modified = original
modified.a.b.c = 2

assert { original.a.b.c == 1 }  # Original unchanged
assert { modified.a.b.c == 2 }  # Modified copy
```

### Block Scoping
```bind
pub x = 100
{
  x = 200         # Creates local copy
  assert { x == 200 }
}
assert { x == 100 }  # Original unchanged outside block
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
1. **Variable Assignment**: Updates environment directly
2. **Field Assignment**:
   - Clones the root object
   - Traverses the path, cloning intermediate objects
   - Updates the final field
   - Stores the new root in the environment

### Type Checking
- Simple assignment checks type compatibility
- Compound assignment validates addition compatibility
- All assignments must maintain type safety

## Patterns and Best Practices

### 1. Fluent Interface Pattern
```bind
type Apko {
  pub withPackages(packages: [String!]!): Apko! {
    self.config.contents.packages += packages
    self                        # Return self for chaining
  }
}
```

### 2. Builder Pattern
```bind
type MyClass {
  pub withName(name: String!): MyClass! {
    self.name = name
    self
  }
}
```

### 3. Accumulator Pattern
```bind
pub counter = 0
counter += 1
counter += 2
counter += 3
# counter is now 6
```

## Constraints and Limitations

### 1. Self Requirement
Reassignment of class fields requires the `self.` prefix:
```bind
self.field = value  # Required
field = value       # Not valid for reassignment
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
```bind
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
