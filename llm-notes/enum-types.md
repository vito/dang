# GraphQL Enum Type Support

GraphQL enum types are supported in Dang. Enums are represented as Modules with fields for each enum value.

## Implementation

### Type Environment (pkg/dang/env.go)

1. **Type Registration**: Enum types are registered as modules in the type environment, just like object types.

2. **Enum Values as Fields**: For enum types (identified by `t.Kind == introspection.TypeKindEnum`), each enum value is added as a field to the enum type module with the enum type itself as its type scheme.

3. **Making Enums Available**: Enum types are made available as values in the root module by adding them with `mod.Add(t.Name, hm.NewScheme(nil, sub))`, making them accessible like `Status.ACTIVE`.

### Evaluation Environment (pkg/dang/eval.go)

In `populateSchemaFunctions()`, enum types are processed to create runtime values:

1. **Module Creation**: For each enum type, a `ModuleValue` is created using the type environment.

2. **Enum Value Population**: Each enum value is added to the module as an `EnumValue` where the value equals the enum value name and includes a reference to the enum type. For example: `EnumValue{Val: "ACTIVE", EnumType: enumTypeEnv}` for `Status.ACTIVE`.

3. **Environment Registration**: The populated enum module is added to the evaluation environment with `env.Set(t.Name, enumModuleVal)`.

### EnumValue Type (pkg/dang/eval.go)

`EnumValue` is a distinct value type that wraps a string with type information:

```go
type EnumValue struct {
    Val      string   // The enum value name (e.g., "ACTIVE")
    EnumType hm.Type  // Reference to the enum type
}
```

This ensures enum values have proper type information at runtime.

### GraphQL Query Handling

#### Direct Function Calls (GraphQLFunction.Call)

1. **Scalar Treatment**: Enum types are treated like scalars in `isScalarType()`, meaning queries that return enums are executed immediately rather than being lazily evaluated.

2. **Return Value Conversion**: When `GraphQLFunction.Call` executes a query that returns an enum, it checks `isEnumType()` and converts the string result to an `EnumValue` with the proper enum type.

3. **Type Environment Access**: Both `GraphQLFunction` and `GraphQLValue` carry a `TypeEnv` field to look up enum types when converting return values.

#### Object Selections (pkg/dang/ast_expressions.go)

When using the `.{field}` syntax on GraphQL objects (e.g., `users.{status}`):

1. **Result Conversion**: `ObjectSelection.convertGraphQLResultToModule()` converts raw GraphQL results to Dang values.

2. **Enum Detection**: For each field, `convertValueWithEnumSupport()` checks if the field type is an enum by:
   - Looking up the field definition in the GraphQL schema
   - Checking if the field's type is `introspection.TypeKindEnum`

3. **Type Environment Threading**: The `typeEnv` parameter is passed through the entire conversion chain to enable enum type lookup.

4. **EnumValue Creation**: When an enum field is detected, the string value from GraphQL is converted to a properly typed `EnumValue` using the enum type from the type environment.

### Value Comparison (pkg/dang/ast.go)

The `valuesEqual()` function handles comparisons between enum values:
- `EnumValue` and `EnumValue`: Compares the string values (e.g., `Status.ACTIVE == Status.ACTIVE`)
- Enum values are NOT comparable with strings - they must be compared with other enum values

## Usage

```dang
# Access enum values as module fields
assert { Status.ACTIVE == Status.ACTIVE }
assert { Status.INACTIVE == Status.INACTIVE }

# Different enum values are not equal
assert { Status.ACTIVE != Status.PENDING }

# Enum values have the proper enum type
pub active = Status.ACTIVE  # active has type Status

# Query results are properly typed as enums
pub statusValue = status  # statusValue has type Status
assert { statusValue == Status.ACTIVE }

# Object selections also preserve enum types
let statuses = users.{status}
assert { statuses[0].status == Status.ACTIVE }
assert { statuses[1].status != Status.ACTIVE }
```

## Schema Requirements

Enum types must be defined in the GraphQL schema:

```graphql
enum Status {
  ACTIVE
  INACTIVE
  PENDING
  ARCHIVED
}
```

## Notes

- Enum values are properly typed with their enum type throughout the system
- Enum values can ONLY be compared with other enum values, not with strings
- Enum types are available in the root scope (no need to import)
- All GraphQL introspection enum types (`__TypeKind`, `__DirectiveLocation`) are also available
- Enum values returned from GraphQL queries are eagerly evaluated and properly typed
- Object selections (`.{field}` syntax) properly preserve enum types
