* [ ] Add boolean operators (&&, ||, !)
* [ ] Add negation support

## Union Types

### Phase 1: Introspection & Type System

* [x] Add `PossibleTypes []*Type` field to `introspection.Type` struct (the introspection query already fetches `possibleTypes`)
* [x] Add `UnionKind` to `ModuleKind` in `pkg/dang/env.go`
* [x] Wire up `introspection.TypeKindUnion` in `ModuleKindFromGraphQLKind` (currently commented out)
* [x] Add union member tracking to `Module`: `members []Env` field, `AddMember`/`GetMembers`/`HasMember` methods
* [x] Load union members from schema in `NewEnv()` — link `PossibleTypes` to member modules
* [x] Make union types available as values in the root module (like interfaces and enums)
* [x] Subtyping: `Module.Supertypes()` returns unions the type is a member of
* [x] Subtyping: `Module.Eq()` allows concrete member assignment where union is expected

### Phase 2: Grammar & AST

* [x] Add `union` keyword and `Union` rule to `pkg/dang/dang.peg`:
      ```
      union SearchResult = User | Post | Comment
      ```
* [x] Add `UnionDecl` AST node to `pkg/dang/slots.go` (Name, Members, Visibility, Inferred, etc.)
* [x] Classify `UnionDecl` as a type in `pkg/dang/block.go`
* [x] Hoisting: pass 0 registers the union module, pass 1 resolves and links members
* [x] Validation: each member must resolve to an object type (not scalar, enum, or another union)
* [x] `UnionDecl.Eval`: register the union module value (like `InterfaceDecl.Eval`)

### Phase 3: Type Discrimination (case with type patterns)

* [ ] Extend `CaseClause` in the grammar to support type patterns: `binding: TypeName => expr`
* [ ] Type narrowing in `Case.Infer`: when the operand is a union type and the clause has a type pattern, introduce the binding with the narrowed concrete type
* [ ] Runtime discrimination for Dang-native unions: check the concrete type at runtime
* [ ] Runtime discrimination for GraphQL unions: emit `__typename` in the query, dispatch on it

### Phase 4: GraphQL Query Generation

* [ ] Automatically include `__typename` when selecting from a union-typed field
* [ ] Generate inline fragments (`... on TypeName { fields }`) from type-discriminated case branches
* [ ] Map JSON response back to the correct Dang type based on `__typename`

### Phase 5: Editor Syntax & Formatting

* [x] Add `union` keyword to tree-sitter grammar and editor syntax definitions
* [x] Add `UnionDecl` formatting in `format.go` (like `formatInterfaceDecl`)
