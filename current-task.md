# GraphQL Interface Support Implementation Plan

## Overview

Add support for GraphQL interfaces to Dang, allowing types to implement interfaces and enabling interface-based polymorphism. This will align Dang's type system with GraphQL's interface capabilities.

## Current State

### What Works
- **GraphQL Types**: Object types, enums, and scalars are fully supported
- **Introspection**: The introspection query already fetches `interfaces` and `possibleTypes` fields (lines 37-39, 46-48 in `introspection/introspection.graphql`)
- **Type System Infrastructure**: Excellent foundation with phased compilation, composite modules, and Hindley-Milner type inference
- **Data Available**: `introspection.Type` struct has an `Interfaces []*Type` field (line 108 in `introspection/introspection.go`)

### What's Missing
- **No Interface Kind**: `ModuleKind` enum only has `ObjectKind`, `EnumKind`, `ScalarKind` (lines 30-37 in `pkg/dang/env.go`)
- **No Interface Syntax**: Grammar has no `interface` keyword or syntax
- **No Subtyping**: Type system treats all types independently - no inheritance or implementation relationships
- **Lost Information**: When loading GraphQL schemas, interface implementation info is discarded

### Current Behavior
If a GraphQL schema has:
```graphql
interface Node {
  id: ID!
}

type User implements Node {
  id: ID!
  name: String!
}
```

Dang currently treats both as independent object types with no relationship. The `implements Node` relationship is **lost**.

## Design Decisions

### 1. Interface Representation Strategy

**Decision**: Interfaces should be represented as Modules with `InterfaceKind`, similar to how enums and scalars work.

**Rationale**:
- Consistent with existing type system architecture
- Modules already support fields/methods
- Easy to track which types implement which interfaces
- Aligns with GraphQL's interface model

### 2. Syntax Strategy

**Option A: Explicit Interface Declarations (Recommended)**
```dang
interface Node {
  pub id: ID!
}

type User implements Node {
  pub id: ID!
  pub name: String!
}
```

**Option B: No Dang Syntax (GraphQL-Only)**
- Interfaces exist only at runtime from GraphQL schemas
- No Dang code can declare interfaces
- Types automatically inherit interface implementations from GraphQL

**Recommendation**: Start with **Option B** for initial implementation, add Option A later if needed.

**Rationale**:
- GraphQL interfaces are already in the schema
- Most Dang code queries existing GraphQL APIs
- Simpler initial implementation
- Can add syntax later if users need to declare interfaces in pure Dang code

### 3. Subtyping Strategy

**Decision**: Use structural subtyping with interface field requirements.

**Rationale**:
- A type implements an interface if it has all the interface's fields with compatible types
- Matches GraphQL's structural interface model
- No need for explicit `implements` checks in type inference
- Works well with Hindley-Milner type system

### 4. Polymorphism Strategy

**Decision**: Interface-typed values can reference any implementing type. Field access on interfaces resolves to the interface's declared fields.

**Example**:
```dang
# If 'node' has type 'Node' interface
let nodeId = node.id  # Works - 'id' is declared on Node interface

# If 'user' has type 'User' which implements 'Node'
let n: Node = user    # Works - User implements Node
let userId = n.id     # Works - can access interface fields
let userName = n.name # Error - 'name' not declared on Node interface
```

## Implementation Plan

### Phase 1: Type System Foundation

#### 1.1 Add Interface Kind
**Files**: `pkg/dang/env.go`

- [ ] Add `InterfaceKind` to `ModuleKind` enum (after `ScalarKind`)
- [ ] Update any `ModuleKind` switch statements to handle `InterfaceKind`

#### 1.2 Track Interface Implementations
**Files**: `pkg/dang/env.go`, `pkg/dang/types.go`

- [ ] Add `Interfaces []Env` field to `Module` struct to track implemented interfaces
- [ ] Add `Implementers []Env` field to `Module` struct (for interfaces) to track types that implement them
- [ ] Add helper methods:
  - `Module.AddInterface(iface Env)`
  - `Module.GetInterfaces() []Env`
  - `Module.AddImplementer(impl Env)` (for interface modules)
  - `Module.GetImplementers() []Env` (for interface modules)
  - `Module.ImplementsInterface(iface Env) bool`

### Phase 2: Schema Loading

#### 2.1 Load Interface Types
**Files**: `pkg/dang/env.go` (function `WithSchema`)

- [ ] In the first loop over `schema.Types` (lines 150-163):
  - Detect `t.Kind == introspection.TypeKindInterface`
  - Create interface modules with `Kind: InterfaceKind`
  - Store in type environment like other types

#### 2.2 Populate Interface Fields
**Files**: `pkg/dang/env.go` (function `WithSchema`)

- [ ] In the field population loop (lines 226-256):
  - Interface fields should be added to interface modules
  - Same logic as object types - convert to function types

#### 2.3 Link Implementations
**Files**: `pkg/dang/env.go` (function `WithSchema`)

- [ ] Add new loop after field population:
  - For each type in `schema.Types`
  - If type has `Interfaces` field populated
  - For each interface in `t.Interfaces`:
    - Look up interface module in type environment
    - Call `type.AddInterface(interfaceModule)`
    - Call `interfaceModule.AddImplementer(type)`

#### 2.4 Make Interfaces Available
**Files**: `pkg/dang/env.go` (function `WithSchema`)

- [ ] Add interfaces to root module like enums/scalars (lines 165-175 pattern):
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

### Phase 3: Type Checking & Inference

#### 3.1 Subtyping Rules
**Files**: `pkg/hm/unify.go` or new `pkg/dang/subtyping.go`

- [ ] Implement subtyping check function:
  ```go
  func IsSubtype(sub hm.Type, super hm.Type) bool
  ```
- [ ] Logic:
  - If `super` is an interface type (Module with InterfaceKind)
  - Check if `sub` is a Module that implements `super`
  - Use `Module.ImplementsInterface()` helper
  - Return true if implementation exists

#### 3.2 Unification with Subtyping
**Files**: `pkg/hm/unify.go`

- [ ] Modify `Unify` function to check subtyping:
  - When unifying two Modules
  - If exact types don't match
  - Try `IsSubtype(t1, t2)` and `IsSubtype(t2, t1)`
  - If either succeeds, allow unification

#### 3.3 Field Access on Interfaces
**Files**: `pkg/dang/ast.go` or `pkg/dang/ast_expressions.go`

- [ ] In `Select.Infer()` method:
  - When receiver type is an interface Module
  - Look up field in the interface Module (not implementing types)
  - Return interface's declared field type

### Phase 4: Runtime Support

#### 4.1 Interface Value Wrapping
**Files**: `pkg/dang/eval.go`

- [ ] Add `InterfaceValue` type:
  ```go
  type InterfaceValue struct {
    Value        Value   // Underlying implementing type value
    InterfaceType hm.Type // The interface type
  }
  ```
- [ ] When assigning implementing type to interface-typed variable:
  - Wrap in `InterfaceValue`
  - Store both concrete value and interface type

#### 4.2 Field Access on Interface Values
**Files**: `pkg/dang/eval.go`, `pkg/dang/ast_expressions.go`

- [ ] In `Select.Eval()` method:
  - If receiver is `InterfaceValue`
  - Unwrap to get concrete value
  - Access field on concrete value
  - Ensure field exists on interface type (should be type-checked already)

#### 4.3 GraphQL Query Results
**Files**: `pkg/dang/eval.go`, `pkg/dang/ast_expressions.go`

- [ ] When GraphQL returns interface-typed results:
  - Detect field return type is interface
  - Create `InterfaceValue` wrapping actual returned object
  - May need to inspect `__typename` field for concrete type

### Phase 5: Testing

#### 5.1 Update Test Schema
**Files**: `tests/gqlserver/schema.graphqls`, `tests/gqlserver/resolvers.go`

- [ ] Add sample interface to test schema:
  ```graphql
  interface Node {
    id: ID!
  }
  
  interface Timestamped {
    createdAt: Timestamp!
  }
  
  type User implements Node & Timestamped {
    id: ID!
    name: String!
    email: String!
    createdAt: Timestamp!
  }
  
  type Post implements Node & Timestamped {
    id: ID!
    title: String!
    content: String!
    authorId: ID!
    createdAt: Timestamp!
  }
  ```
- [ ] Add query returning interface types:
  ```graphql
  type Query {
    node(id: ID!): Node
    nodes: [Node!]!
    timestamped: [Timestamped!]!
  }
  ```
- [ ] Implement resolvers in `resolvers.helpers.go`

#### 5.2 Create Dang Test File
**Files**: `tests/test_interface.dang`

- [ ] Test interface type availability
  ```dang
  # Interfaces should be available as types
  assert { Node != null }
  assert { Timestamped != null }
  ```
- [ ] Test querying interfaces
  ```dang
  # Query returning interface type
  pub nodeResult = node({{id: "1"}})
  assert { nodeResult.id != null }
  ```
- [ ] Test field access on interfaces
  ```dang
  # Can access interface fields
  let allNodes = nodes
  assert { allNodes[0].id != null }
  ```
- [ ] Test polymorphism
  ```dang
  # Interface can hold different implementing types
  let items = timestamped
  assert { items[0].createdAt != null }
  ```
- [ ] Test that non-interface fields are inaccessible
  ```dang
  # Should fail at type check:
  # let n = node({{id: "1"}})
  # let name = n.name  # Error: 'name' not on Node interface
  ```

#### 5.3 Add Go Tests
**Files**: `tests/test_interface_test.go`

- [ ] Create test file following existing test patterns
- [ ] Test schema loading with interfaces
- [ ] Test type checking with interface assignments
- [ ] Test error cases (accessing non-interface fields)

### Phase 6: Documentation

#### 6.1 Create LLM Notes
**Files**: `llm-notes/interface-types.md`

- [ ] Document interface support similar to `enum-types.md` and `scalar-types.md`
- [ ] Include:
  - Type environment structure
  - Schema loading process
  - Subtyping rules
  - Runtime representation
  - Usage examples
  - GraphQL integration notes

#### 6.2 Update Existing Notes
**Files**: Various in `llm-notes/`

- [ ] Update any notes that mention "types" to include interfaces
- [ ] Ensure accuracy of notes about ModuleKind enum

## Future Enhancements (Not in Initial Implementation)

### Future: Interface Declaration Syntax
- Add `interface` keyword to grammar
- Support Dang-native interface declarations
- Pattern similar to `type` but with `InterfaceKind`

### Future: Union Type Support
- Similar strategy to interfaces
- GraphQL unions map to Dang union types
- Type-safe pattern matching

### Future: Type Narrowing
- Use type guards or pattern matching
- Narrow interface types to concrete types
- Access concrete type fields safely

### Future: Interface Extensions
- Support interface inheritance (interface extends interface)
- Already in GraphQL spec

## Risk Mitigation

### Breaking Changes
- **Risk**: Low - interfaces don't exist yet, so no breaking changes
- **Mitigation**: Ensure existing tests still pass

### Type System Complexity
- **Risk**: Medium - subtyping adds complexity to type inference
- **Mitigation**: 
  - Start with simple structural subtyping
  - Extensive testing
  - Clear error messages

### Performance
- **Risk**: Low - interface checks are structural
- **Mitigation**: 
  - Cache interface implementation checks
  - Avoid unnecessary type conversions

### GraphQL Spec Compliance
- **Risk**: Low - following standard GraphQL interface semantics
- **Mitigation**: Test against real GraphQL schemas

## Success Criteria

- [ ] GraphQL schemas with interfaces load successfully
- [ ] Interface types are available in Dang code
- [ ] Types implementing interfaces can be assigned to interface-typed variables
- [ ] Field access on interfaces works correctly
- [ ] Type checking enforces interface boundaries
- [ ] All existing tests continue to pass
- [ ] New interface tests pass
- [ ] Documentation is complete

## Implementation Checklist

### Prerequisites
- [x] Research GraphQL interfaces in codebase
- [x] Understand current type system
- [x] Create implementation plan
- [x] Write plan to current-task.md

### Phase 1: Type System Foundation
- [ ] Add `InterfaceKind` to `ModuleKind` enum
- [ ] Add `Interfaces` field to `Module` struct
- [ ] Add `Implementers` field to `Module` struct
- [ ] Add helper methods for interface tracking

### Phase 2: Schema Loading
- [ ] Load interface types from GraphQL schema
- [ ] Populate interface fields
- [ ] Link implementations to interfaces
- [ ] Make interfaces available in root module

### Phase 3: Type Checking
- [ ] Implement subtyping check function
- [ ] Modify unification to support subtyping
- [ ] Update field access for interface types

### Phase 4: Runtime Support
- [ ] Add `InterfaceValue` type
- [ ] Handle interface value wrapping
- [ ] Update field access evaluation
- [ ] Handle GraphQL query results

### Phase 5: Testing
- [ ] Update test GraphQL schema
- [ ] Implement test resolvers
- [ ] Create Dang test file
- [ ] Add Go integration tests
- [ ] Run full test suite

### Phase 6: Documentation
- [ ] Create `llm-notes/interface-types.md`
- [ ] Update existing documentation
- [ ] Verify all notes are accurate

### Final Steps
- [ ] Run `./tests/run_all_tests.sh`
- [ ] Verify all tests pass
- [ ] Review implementation for edge cases
- [ ] Clean up debug code
- [ ] Consider future enhancements
