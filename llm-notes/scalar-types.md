# Scalar Types in Dang

## Overview

Scalars are custom opaque types backed by strings, similar to GraphQL custom scalars. They provide type safety for domain-specific string values (e.g., Timestamp, URL, Email).

## Syntax

```dang
scalar TypeName
```

Declares a new scalar type. The scalar is internally represented as a string but is treated as a distinct type.

## Key Implementation Details

### Runtime Representation
- **Type**: `ScalarValue` struct in `pkg/dang/scalar.go`
- **Fields**: 
  - `Val string` - the underlying string value
  - `ScalarType *ModuleValue` - reference to the scalar's type definition
- **JSON marshaling**: Serializes directly as the string value

### Type System
- **Kind**: `ScalarKind` in `ModuleKind` enum
- **Module**: Each scalar declaration creates a `Module` with `Kind: ScalarKind`
- **Type safety**: Scalars are NOT compatible with `String` type - they're distinct types
- **Comparison**: Two scalars can be compared if they're the same scalar type

### Phased Compilation
Scalars MUST be classified as "types" in `classifyForms()` (pkg/dang/block.go) so they're hoisted in the correct phase:
```go
case *ScalarDecl:
    classified.Types = append(classified.Types, f)
```

This ensures scalar declarations are processed BEFORE function signatures that reference them.

### Dagger SDK Integration

In `dagger-sdk/entrypoint/main.go`:

1. **Input conversion** (`anyToDang`): String inputs are converted to `ScalarValue`
2. **Module initialization** (`initModule`): Scalar modules are skipped (handled as strings)
3. **Type mapping** (`dangTypeToTypeDef`): Scalars map to `StringKind` TypeDef in the Dagger API

Scalars effectively map to `string` in the generated Dagger API.

## Usage Examples

```dang
# Declare scalars
scalar Timestamp
scalar URL

# Use in types
type MyAPI {
  pub getTimestamp(ts: Timestamp!): String! {
    toJSON(ts)
  }
}

# Scalars can be:
# - Stored in variables
pub ts = now  # if 'now' is a Timestamp from GraphQL
# - Compared with each other
assert { ts == ts }
assert { ts != otherTs }
# - Stored in records
pub rec = {{timestamp: ts, url: homepage}}
# - Stored in lists
pub timestamps = [ts, ts]
# - Passed to/returned from functions
```

## GraphQL Integration

Scalars align with GraphQL custom scalar types. When a GraphQL API defines custom scalars, Dang can declare matching scalar types to maintain type safety across the boundary.

## Related Files

- `pkg/dang/scalar.go` - ScalarValue implementation
- `pkg/dang/env.go` - ScalarKind definition, scalar hoisting
- `pkg/dang/block.go` - Form classification for phased compilation
- `pkg/dang/dang.peg` - Grammar for scalar declarations
- `dagger-sdk/entrypoint/main.go` - Dagger SDK scalar support
- `tests/test_scalar.dang` - Basic scalar tests
- `tests/test_scalar_comprehensive.dang` - Comprehensive scalar tests
- `mod/test-scalar/` - Dagger module testing scalars
