# List Indexing Implementation

## Feature Overview
Added support for list indexing syntax `foo[0]` to access elements in lists.

## Implementation Details

### Grammar Changes
- Added `IndexOrCall` rule in `pkg/sprout/sprout.peg` 
- Added to `Term` definition with higher precedence than `SelectOrCall`
- Supports both indexing (`foo[0]`) and indexing with function calls (`foo[0]()`)

### AST Node
- Added `Index` struct in `pkg/sprout/ast_expressions.go`
- Implements Node, Evaluator interfaces
- Supports AutoCall functionality like other access operations

### Type System
- Index must be `Int!` type (enforced at type checking)
- List receiver can be nullable or non-null
- Result is always nullable (indexing can fail due to out-of-bounds)
- Out-of-bounds access returns `null` rather than throwing an error

### Evaluation
- Supports null propagation (null list returns null)
- Bounds checking for safety
- Negative indices treated as out-of-bounds
- Zero-based indexing

## Examples
```sprout
pub numbers = [1, 2, 3]
assert { numbers[0] == 1 }
assert { numbers[5] == null }  # out of bounds

pub nested = [[1, 2], [3, 4]]
assert { nested[0][1] == 2 }   # chained indexing
```

## Testing
- Comprehensive tests in `tests/test_list_indexing.sp`
- Tests basic indexing, nested lists, out-of-bounds, variables as indices

## Known Limitations
- Negative literal numbers like `-1` don't parse correctly as indices due to grammar limitations
- Use computed negative values: `pub neg = 0 - 1; list[neg]`