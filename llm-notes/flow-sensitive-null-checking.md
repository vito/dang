# Flow-Sensitive Null Checking

## Current Implementation

Sprout now supports basic flow-sensitive null checking through conditional type refinement. When a conditional expression contains null checks, the type system automatically narrows types in the appropriate branches.

### Supported Patterns

- `if (x != null)` - x becomes non-null in then branch
- `if (x == null)` - x becomes non-null in else branch  
- `if (null != x)` - x becomes non-null in then branch
- `if (null == x)` - x becomes non-null in else branch

### Example Usage

```sprout
type Example {
  let maybeValue: String = null
  
  pub process: String! {
    if (maybeValue != null) {
      # maybeValue is automatically narrowed from String to String!
      maybeValue + " processed"
    } else {
      "no value"
    }
  }
}
```

### Technical Implementation

- **Location**: `pkg/sprout/null_analysis.go` and modified `Conditional.Infer()` in `ast_expressions.go`
- **Pattern Detection**: `AnalyzeNullAssertions()` scans conditional expressions for null comparison patterns
- **Type Refinement**: `CreateTypeRefinements()` converts detected patterns into type narrowing rules
- **Environment Shadowing**: `ApplyTypeRefinements()` creates separate type environments for then/else branches using copy-on-write semantics

### Limitations

Current implementation is basic and only handles simple null checks:
- **No boolean logic**: Doesn't handle `&&`, `||`, or `!` operators
- **Single-level only**: Only analyzes immediate conditional expressions
- **No function calls**: Doesn't handle null checks within function calls
- **No complex patterns**: Can't handle nested or compound expressions

For production use, would need full control flow analysis with boolean constraint solving.

### Testing

- **Positive test**: `tests/test_flow_sensitive_null.bd` - verifies null checks work correctly
- **Error test**: `tests/errors/flow_sensitive_null_else_branch.bd` - verifies type errors still occur in branches where variable remains nullable