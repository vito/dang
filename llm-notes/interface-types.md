# GraphQL Interface Type Support

GraphQL interface types are supported in Dang, enabling interface-based polymorphism where types can implement interfaces and be treated polymorphically through their interface type.

## Implementation

### Type Environment (pkg/dang/env.go)

#### 1. Module Kind

Interfaces are represented as Modules with `InterfaceKind`:

```go
const (
    ObjectKind ModuleKind = iota
    EnumKind
    ScalarKind
    InterfaceKind  // Added for interface support
)
```

#### 2. Interface Tracking

The `Module` struct tracks interface relationships bidirectionally:

```go
type Module struct {
    // ... existing fields ...
    interfaces   []Env  // Interfaces this type implements
    implementers []Env  // Types that implement this interface (for interface modules)
}
```

Helper methods:
- `AddInterface(iface Env)` - Register that this type implements an interface
- `GetInterfaces() []Env` - Get all interfaces this type implements
- `AddImplementer(impl Env)` - Register a type as implementing this interface
- `GetImplementers() []Env` - Get all types that implement this interface
- `ImplementsInterface(iface Env) bool` - Check if this type implements an interface

#### 3. Schema Loading

In `NewEnv()` function, interfaces are loaded in three phases:

**Phase 1: Type Creation** (lines 155-172)
- Detect `t.Kind == introspection.TypeKindInterface`
- Create interface modules with `Kind: InterfaceKind`
- Store in type environment like other types

**Phase 2: Field Population** (lines 226-256)
- Interface fields are added to interface modules
- Same logic as object types - convert to function types

**Phase 3: Implementation Linking** (after field population)
- For each type in `schema.Types` that has `Interfaces` field populated
- Look up each interface module in type environment
- Call `type.AddInterface(interfaceModule)` and `interfaceModule.AddImplementer(type)`
- This establishes the bidirectional relationship

#### 4. Making Interfaces Available

Interfaces are made available as values in the root module (lines 209-219):

```go
for _, t := range schema.Types {
    if t.Kind == introspection.TypeKindInterface {
        sub, found := env.NamedType(t.Name)
        if found {
            mod.Add(t.Name, hm.NewScheme(nil, sub))
            mod.SetVisibility(t.Name, PublicVisibility)
        }
    }
}
```

This makes interface types accessible like `Node` or `Timestamped`.

### Type Checking & Inference (pkg/dang/env.go)

#### Subtyping via Module.Eq

Subtyping is implemented directly in the `Module.Eq()` method rather than a separate function. This integrates subtyping into Hindley-Milner type unification:

```go
func (t *Module) Eq(other Type) bool {
    otherMod, ok := other.(*Module)
    if !ok {
        return false
    }
    if t.Named != "" {
        // Check for exact equality
        if t == otherMod {
            return true
        }
        // Check for subtyping: if other is an interface that t implements
        // This allows User.Eq(Node) when User implements Node
        // Note: This makes Eq asymmetric, which is intentional for subtyping
        if otherMod.Kind == InterfaceKind && t.ImplementsInterface(otherMod) {
            return true
        }
        return false
    }
    return t.AsRecord().Eq(otherMod.AsRecord())
}
```

**Key Points:**
- **Asymmetric by design**: `User.Eq(Node)` returns true (User implements Node), but `Node.Eq(User)` returns false
- **Direction matters**: Concrete types can be used where interface types are expected, but not vice versa
- **Unification integration**: This works with Hindley-Milner unification because the asymmetry is sound - you can safely pass a `User` where a `Node` is expected, accessing only `Node` fields

This enables:
- Concrete types can be assigned to interface-typed variables
- Interface types can be used in function signatures with concrete implementations
- Type-safe polymorphism without explicit casts

### Evaluation Environment (pkg/dang/eval.go)

In `populateSchemaFunctions()`, interface types are processed to create runtime values (lines 309-322):

```go
if t.Kind == introspection.TypeKindInterface {
    // Get the interface type environment
    interfaceTypeEnv, found := typeEnv.NamedType(t.Name)
    if !found {
        continue
    }

    // Create a module for the interface type (just a type placeholder)
    interfaceModuleVal := NewModuleValue(interfaceTypeEnv)

    // Add the interface module to the environment
    env.Set(t.Name, interfaceModuleVal)
}
```

This makes interface types available as runtime values, accessible via `Symbol.Eval`.

### Runtime Behavior

**No Special Interface Value Type**: Unlike enums which use `EnumValue`, interfaces don't need a special value wrapper. Concrete values flow naturally through the system.

**Field Access**: When accessing fields on interface-typed values:
1. Type checking ensures only interface-declared fields are accessible
2. At runtime, the concrete value's fields are accessed
3. GraphQL handles the interface dispatch automatically

**GraphQL Query Results**: When GraphQL returns interface-typed results:
- The concrete type (User, Post, etc.) is returned as a normal object
- Type checking has already ensured only interface fields are accessed
- No runtime conversion needed

## Usage

```dang
# Interface types are available as values
assert { Node != null }
assert { Timestamped != null }

# Query returning interface type
pub nodeResult = node(id: "1").{id}
assert { nodeResult.id == "1" }

# Query returning list of interfaces
pub allNodes = nodes.{id}
assert { allNodes[0].id != null }

# Access interface fields only
let firstNode = allNodes[0]
assert { firstNode.id != null }  # OK - 'id' is on Node interface

# This would be a type error:
# let userName = firstNode.name  # Error - 'name' not on Node interface

# Multiple interfaces
pub timestampedItems = timestamped.{createdAt}
assert { timestampedItems[0].createdAt != null }
```

## Schema Requirements

Interface types must be defined in the GraphQL schema:

```graphql
interface Node {
  id: String!
}

interface Timestamped {
  createdAt: String!
}

type User implements Node {
  id: String!
  name: String!
}

type Post implements Node & Timestamped {
  id: String!
  title: String!
  createdAt: String!
}

type Query {
  node(id: String!): Node
  nodes: [Node!]!
  timestamped: [Timestamped!]!
}
```

## Key Design Decisions

### 1. Structural Subtyping
Interfaces use structural subtyping - a type implements an interface if it has all the interface's fields with compatible types. This matches GraphQL's interface model.

### 2. No Syntax (Yet)
Currently, interfaces are only loaded from GraphQL schemas. There's no Dang syntax to declare interfaces (like `interface Node { ... }`). This may be added in the future.

### 3. Runtime Transparency
Interface values are just concrete values - no wrapper type like `EnumValue`. The type system enforces interface boundaries at compile time, and GraphQL handles dispatch at runtime.

### 4. Bidirectional Tracking
Both implementing types and interface types track their relationships, enabling efficient type checking in both directions.

## Implementation Phases (Completed)

1. ✅ **Type System Foundation**: Added `InterfaceKind` and tracking fields/methods
2. ✅ **Schema Loading**: Load interfaces from GraphQL and link implementations
3. ✅ **Type Checking**: Subtyping support in `IsSubtype()` and `Module.Eq()`
4. ✅ **Runtime Support**: Make interfaces available as runtime values
5. ✅ **Testing**: Test schema with Node and Timestamped interfaces

## Notes

- Interface types are properly typed throughout the system
- Only interface-declared fields are accessible on interface-typed values
- Type checking enforces interface boundaries at compile time
- GraphQL handles the actual interface dispatch at runtime
- Interface types are available in the root scope (no need to import)
- Multiple interface implementation is supported (e.g., `Post implements Node & Timestamped`)
- All GraphQL interfaces from the schema are automatically available

## Related Files

- `pkg/dang/env.go` - InterfaceKind, Module tracking, schema loading
- `pkg/dang/types.go` - IsSubtype function
- `pkg/dang/eval.go` - Runtime interface value availability
- `tests/gqlserver/schema.graphqls` - Test schema with interfaces
- `tests/test_interface.dang` - Interface functionality tests
- `introspection/introspection.go` - Interface introspection support
- `introspection/introspection.graphql` - Introspection query for interfaces
