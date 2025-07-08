# GraphQL Directives in Sprout

## Background
GraphQL directives are annotations that can be applied to various parts of a GraphQL schema or query to modify execution behavior. They use `@directiveName(args...)` syntax.

## Current State
Sprout already has comprehensive GraphQL directive support in its introspection system (`introspection/introspection.go`):
- `DirectiveDef` struct for directive definitions
- `Directive` struct for directive applications  
- `DirectiveArg` struct for directive arguments
- Built-in directives: `@experimental`, `@sourceMap`, `@enumValue`

However, the Sprout language itself has **no syntax** for declaring or using directives in `.sp` files.

## Proposed Implementation

### Directive Declaration Syntax
```sprout
# Declare a directive that can be used on fields and types
directive @deprecated(reason: String = "No longer supported") on FIELD_DEFINITION | OBJECT

# Simple directive with no arguments
directive @experimental on FIELD_DEFINITION | ARGUMENT_DEFINITION
```

### Directive Application Syntax
```sprout
type User {
  pub id: String!
  pub name: String! @deprecated(reason: "Use displayName instead")
  pub displayName: String!
  pub email: String! @experimental
}
```

### Directive Locations
Standard GraphQL directive locations:
- `SCHEMA`, `SCALAR`, `OBJECT`, `FIELD_DEFINITION`, `ARGUMENT_DEFINITION`
- `INTERFACE`, `UNION`, `ENUM`, `ENUM_VALUE`, `INPUT_OBJECT`, `INPUT_FIELD_DEFINITION`

## Implementation Plan

### 1. Grammar Extension (`pkg/sprout/sprout.peg`)
```peg
DirectiveDecl <- DirectiveToken _ name:DirectiveName _ args:ArgTypes? _ OnToken _ locs:DirectiveLocations

DirectiveApplication <- '@' name:Id args:ArgValues?

DirectiveLocations <- loc:DirectiveLocation (_ '|' _ loc:DirectiveLocation)*
```

### 2. AST Nodes
```go
type DirectiveDecl struct {
    Name      string
    Args      []SlotDecl
    Locations []DirectiveLocation
    Loc       *SourceLocation
}

type DirectiveApplication struct {
    Name string
    Args []Keyed[Node]
    Loc  *SourceLocation
}
```

### 3. Type Environment
Store declared directives in `Env` for validation:
```go
type Module struct {
    // ... existing fields
    directives map[string]*DirectiveDecl
}
```

### 4. Validation Rules
- Check directive exists before use
- Validate directive arguments match declaration
- Ensure directive location is valid for usage context
- Type-check directive argument values

## Usage Examples

### Custom Directives
```sprout
# Declare custom directives
directive @auth(role: String!) on FIELD_DEFINITION
directive @cache(ttl: Int! = 300) on FIELD_DEFINITION

type Query {
  pub adminUsers: [User!]! @auth(role: "admin")
  pub publicData: String! @cache(ttl: 60)
}
```

### Integration with GraphQL Schema
When generating GraphQL schema from Sprout code, directive applications would be preserved and included in the output schema.

## Benefits
- **Static validation**: Catch directive typos at compile time
- **Type safety**: Ensure directive arguments are correctly typed
- **GraphQL compatibility**: Use familiar `@directive` syntax
- **Documentation**: Directives serve as metadata for API consumers

## Design Considerations
- **Simplicity**: Keep directive syntax minimal and familiar
- **Type safety**: Leverage Sprout's type system for directive validation
- **GraphQL compliance**: Follow GraphQL specification for directive behavior
- **Backward compatibility**: Don't break existing Sprout code

This implementation would make Sprout's GraphQL integration even more powerful while maintaining its core principles of type safety and simplicity.