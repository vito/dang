# GraphQL Interface Type Support

GraphQL interface types are fully supported in Dang, enabling interface-based polymorphism where types can implement interfaces and be treated polymorphically through their interface type. Interfaces can be loaded from GraphQL schemas or defined directly in Dang code.

## Implementation

### Grammar (pkg/dang/dang.peg)

#### Interface Declaration Syntax

Interfaces are declared using the `interface` keyword:

```peg
Interface <- InterfaceToken _ name:IdSymbol _ block:Block {
  return &InterfaceDecl{
    Name: name.(*Symbol),
    Value: block.(*Block),
    Visibility: PublicVisibility,
    Loc: c.Loc(),
  }, nil
}
```

Example:
```dang
interface Named {
  pub name: String!
}
```

#### Type Implementation Syntax

Types declare interface implementation using the `implements` keyword with `&` for multiple interfaces:

```peg
Class <- TypeToken _ name:IdSymbol implements:(_ ImplementsToken _ first:IdSymbol rest:(_ '&' _ i:IdSymbol { return i, nil })* { return sliceOfAppend[*Symbol](rest, first), nil })? ...
```

Example:
```dang
type Person implements Named {
  pub name: String!
  pub age: Int!
}

type Book implements Named & Serializable {
  pub name: String!
  pub data: String!
}
```

### AST Structure (pkg/dang/slots.go)

#### InterfaceDecl

```go
type InterfaceDecl struct {
    InferredTypeHolder
    Name       *Symbol
    Value      *Block           // Interface body with field declarations
    Visibility Visibility
    DocString  string
    Loc        *SourceLocation
    Inferred   *Module         // Populated during hoisting
}
```

#### ClassDecl Enhancement

```go
type ClassDecl struct {
    // ... existing fields ...
    Implements []*Symbol  // List of interface names this type implements
}
```

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

### Hoisting & Compilation (pkg/dang/slots.go, pkg/dang/block.go)

#### Form Classification

Interfaces must be classified as types for proper hoisting. In `block.go:classifyForms()`:

```go
case *InterfaceDecl:
    classified.Types = append(classified.Types, f)
```

This ensures interfaces are hoisted in the type phase, before classes that implement them.

#### InterfaceDecl.Hoist

Interfaces are hoisted in two passes:

**Pass 0 (all hoists):**
- Create interface module with `InterfaceKind`
- Register in type environment via `mod.AddClass()`
- Add to environment so it can be referenced
- **Does NOT populate fields yet**

**Pass 1 (all hoists):**
- Hoist interface body (field declarations)
- Store inferred module
- Set doc string if present

```go
func (i *InterfaceDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
    // Create/get interface module (pass 0)
    iface, found := mod.NamedType(i.Name.Name)
    if !found {
        iface = NewModule(i.Name.Name)
        iface.(*Module).Kind = InterfaceKind
        mod.AddClass(i.Name.Name, iface)
    }
    
    // Add to environment
    interfaceScheme := hm.NewScheme(nil, iface)
    env.Add(i.Name.Name, interfaceScheme)
    
    // Populate fields (pass 1)
    if pass > 0 {
        if err := i.Value.Hoist(ctx, inferEnv, fresh, pass); err != nil {
            return err
        }
        i.Inferred = iface.(*Module)
    }
    return nil
}
```

#### ClassDecl.Hoist Enhancement

Classes link to interfaces and validate implementations:

**Pass 0:**
- Create class module
- Create constructor
- **Does NOT link interfaces yet** (interfaces may not be hoisted)

**Pass 1:**
- **Link interfaces** - look up each interface by name and establish bidirectional relationship
- Hoist class body
- **Validate interface implementations** - ensure all interface requirements are met

```go
func (c *ClassDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
    // ... create class module ...
    
    // Link interfaces (pass 1 only, after all types are registered)
    if len(c.Implements) > 0 && pass == 1 {
        for _, ifaceSym := range c.Implements {
            ifaceType, found := mod.NamedType(ifaceSym.Name)
            if !found {
                return fmt.Errorf("interface %s not found", ifaceSym.Name)
            }
            
            ifaceMod, ok := ifaceType.(*Module)
            if !ok || ifaceMod.Kind != InterfaceKind {
                return fmt.Errorf("%s is not an interface", ifaceSym.Name)
            }
            
            classMod.AddInterface(ifaceType)
            ifaceMod.AddImplementer(class)
        }
    }
    
    // ... hoist body ...
    
    // Validate implementations (pass 1, after body is hoisted)
    if pass > 0 && len(c.Implements) > 0 {
        if err := c.validateInterfaceImplementations(classMod, mod); err != nil {
            return fmt.Errorf("type %s: %w", c.Name.Name, err)
        }
    }
}
```

### Interface Implementation Validation (pkg/dang/env.go)

When a type declares `implements InterfaceName`, Dang validates that the type actually fulfills the interface contract. This validation follows GraphQL specification rules:

#### validateInterfaceImplementations

```go
func (c *ClassDecl) validateInterfaceImplementations(classMod *Module, env Env) error {
    for _, iface := range classMod.GetInterfaces() {
        ifaceMod := iface.(*Module)
        
        // Check each interface field
        for name, vis := range ifaceMod.Bindings(PrivateVisibility) {
            ifaceFieldScheme := ifaceMod.SchemeOf(name)
            classFieldScheme := classMod.SchemeOf(name)
            
            if classFieldScheme == nil {
                return fmt.Errorf("missing required field %q from interface %s",
                    name, ifaceMod.Named)
            }
            
            // Validate field type compatibility
            if err := validateFieldImplementation(
                name,
                ifaceFieldScheme.Type(),
                classFieldScheme.Type(),
                ifaceMod.Named,
                classMod.Named,
            ); err != nil {
                return err
            }
        }
    }
    return nil
}
```

#### validateFieldImplementation

This function implements GraphQL's field implementation rules:

**For Simple Fields (non-function types):**
- Return type must be covariant (implementation can be more specific)
- Example: Interface requires `String`, implementation can provide `String!` (non-null)

**For Methods (function types):**
1. **Return Type**: Covariant (same as simple fields)
2. **Arguments**: All interface arguments must be present
3. **Argument Types**: Contravariant (implementation can be more general)
   - Example: Interface requires `String!`, implementation can accept `String`
4. **Additional Arguments**: Must be optional (nullable or have defaults)

```go
func validateFieldImplementation(
    fieldName string,
    ifaceFieldType, classFieldType hm.Type,
    ifaceName, className string,
) error {
    // Check if it's a function (method)
    ifaceFn, ifaceIsFunc := ifaceFieldType.(*hm.FunctionType)
    classFn, classIsFunc := classFieldType.(*hm.FunctionType)
    
    if ifaceIsFunc != classIsFunc {
        return fmt.Errorf("field %q: type mismatch", fieldName)
    }
    
    if ifaceIsFunc {
        // Validate return type (covariant)
        if !isSubtypeOf(classFn.Result, ifaceFn.Result) {
            return fmt.Errorf("field %q: incompatible return type", fieldName)
        }
        
        // Validate all interface arguments are present
        // and argument types are contravariant
        // Implementation details in the full function...
    } else {
        // Simple field: validate covariance
        if !isSubtypeOf(classFieldType, ifaceFieldType) {
            return fmt.Errorf("field %q: incompatible type", fieldName)
        }
    }
    return nil
}
```

#### Type Variance Helpers

```go
// isSubtypeOf checks if subType can be used where superType is expected (covariance)
func isSubtypeOf(subType, superType hm.Type) bool {
    // Non-null is subtype of nullable
    if nonNull, ok := subType.(hm.NonNullType); ok {
        return isSubtypeOf(nonNull.Type, superType)
    }
    
    // Nullable accepts non-null
    if nonNull, ok := superType.(hm.NonNullType); ok {
        return isSubtypeOf(subType, nonNull.Type)
    }
    
    // Lists are covariant
    if subList, ok := subType.(*ListType); ok {
        if superList, ok := superType.(*ListType); ok {
            return isSubtypeOf(subList.Type, superList.Type)
        }
    }
    
    // Modules check interface implementation
    if subMod, ok := subType.(*Module); ok {
        if superMod, ok := superType.(*Module); ok {
            return subMod.Eq(superMod)  // Uses Module.Eq which checks implements
        }
    }
    
    return hm.Eq(subType, superType)
}

// isSupertypeOf checks contravariance for argument types
func isSupertypeOf(superType, subType hm.Type) bool {
    return isSubtypeOf(subType, superType)  // Reverse direction
}
```

### Evaluation (pkg/dang/slots.go)

#### InterfaceDecl.Eval

Interfaces are pure type declarations without runtime evaluation:

```go
func (i *InterfaceDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
    // Interfaces don't have runtime values - just register the type
    interfaceModule := NewModuleValue(i.Inferred)
    env.SetWithVisibility(i.Name.Name, interfaceModule, i.Visibility)
    return interfaceModule, nil
}
```

Unlike classes (which create constructors) or enums (which create enum values), interfaces only exist at the type level. No interface body is evaluated, and no values are created.

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

### Defining Interfaces in Dang

```dang
# Simple interface with fields
interface Named {
  pub name: String!
}

# Interface with multiple fields
interface Identifiable {
  pub id: String!
  pub title: String!
}

# Interface for serialization
interface Serializable {
  pub data: String!
}
```

### Implementing Interfaces

```dang
# Single interface implementation
type Person implements Named {
  pub name: String!
  pub age: Int!
}

# Multiple interface implementation
type Book implements Named & Serializable {
  pub name: String!
  pub author: String!
  pub data: String!
}
```

### Using Interface-Implementing Types

```dang
# Create instances
pub person = Person(name: "Alice", age: 30)
assert { person.name == "Alice" }

pub book = Book(name: "1984", author: "Orwell", data: "{}")
assert { book.name == "1984" }
```

### GraphQL Interfaces

GraphQL interfaces work seamlessly:

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

### 2. Dang Syntax + GraphQL Loading
Interfaces can be defined in Dang code using `interface` keyword, or loaded automatically from GraphQL schemas. Both work seamlessly together.

### 3. Implementation Validation
When a type declares `implements Interface`, Dang validates at compile time that:
- All interface fields are present
- Field types are covariant (return types can be more specific)
- Method argument types are contravariant (can be more general)
- Additional arguments in implementations must be optional

This matches GraphQL specification validation rules exactly.

### 4. Multi-Pass Hoisting
- **Pass 0**: Register all type names (interfaces and classes)
- **Pass 1**: Link implementations and validate

This ensures interfaces are available before classes that implement them, regardless of declaration order.

### 5. Runtime Transparency
Interface values are just concrete values - no wrapper type like `EnumValue`. The type system enforces interface boundaries at compile time, and GraphQL handles dispatch at runtime.

### 6. Bidirectional Tracking
Both implementing types and interface types track their relationships, enabling efficient type checking in both directions.

### 7. Evaluation Semantics
Interfaces are pure type declarations - they don't evaluate their bodies or create runtime values beyond the type itself.

## Implementation Phases (Completed)

### Phase 1: GraphQL Interface Loading (Initial)
1. ✅ **Type System Foundation**: Added `InterfaceKind` and tracking fields/methods
2. ✅ **Schema Loading**: Load interfaces from GraphQL and link implementations
3. ✅ **Type Checking**: Subtyping support in `IsSubtype()` and `Module.Eq()`
4. ✅ **Runtime Support**: Make interfaces available as runtime values
5. ✅ **Testing**: Test schema with Node and Timestamped interfaces

### Phase 2: Dang Interface Declaration (Current)
1. ✅ **Grammar**: Added `interface` keyword and `implements` clause syntax
2. ✅ **AST**: Created `InterfaceDecl` and enhanced `ClassDecl` with `Implements` field
3. ✅ **Form Classification**: Added `InterfaceDecl` to type classification in `block.go`
4. ✅ **Hoisting**: Implemented multi-pass hoisting for interfaces and implementation linking
5. ✅ **Validation**: Implemented GraphQL-spec-compliant interface validation
6. ✅ **Evaluation**: Interface declarations evaluate to type values only
7. ✅ **Testing**: Tests for Dang-defined interfaces and validation errors
8. ✅ **Documentation**: Updated this file with complete implementation details

## Notes

- Interfaces can be defined in Dang or loaded from GraphQL schemas
- Interface types are properly typed throughout the system
- Only interface-declared fields are accessible on interface-typed values
- Type checking enforces interface boundaries at compile time with full validation
- GraphQL handles the actual interface dispatch at runtime
- Interface types are available in the root scope (no need to import)
- Multiple interface implementation is supported (e.g., `type Book implements Named & Serializable`)
- All GraphQL interfaces from the schema are automatically available
- Implementation validation follows GraphQL specification rules exactly
- Declaration order doesn't matter - multi-pass hoisting handles forward references

## Related Files

- `pkg/dang/dang.peg` - Grammar for `interface` and `implements` syntax
- `pkg/dang/slots.go` - InterfaceDecl and ClassDecl implementation
- `pkg/dang/block.go` - Form classification for interfaces
- `pkg/dang/env.go` - InterfaceKind, Module tracking, schema loading, validation helpers
- `pkg/dang/types.go` - IsSubtype function
- `pkg/dang/eval.go` - Runtime interface value availability
- `tests/gqlserver/schema.graphqls` - Test schema with interfaces
- `tests/test_interface.dang` - GraphQL interface functionality tests
- `tests/test_interface_dang.dang` - Dang interface definition tests
- `tests/test_interface_validation_error.dang` - Interface validation error tests
- `introspection/introspection.go` - Interface introspection support
- `introspection/introspection.graphql` - Introspection query for interfaces
