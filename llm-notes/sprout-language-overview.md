# Sprout Language Overview

## What is Sprout?
Sprout is a strongly typed scripting language with Hindley-Milner type inference, designed for GraphQL API integration. Types are derived from GraphQL schemas, providing compile-time safety for API interactions.

## Core Language Features

### Type System
- **Hindley-Milner inference**: Types are inferred automatically, no explicit annotations needed
- **GraphQL integration**: Types derived from GraphQL schema introspection
- **Null safety**: Explicit nullable (`Type`) vs non-null (`Type!`) types
- **Flow-sensitive null checking**: Automatic type narrowing in conditional branches
- **Primitive types**: `Int`, `String`, `Boolean`, `Null`
- **Composite types**: Lists `[Type]`, Objects `{{field: Type}}`, Records

### Syntax Basics
```sprout
# Variable declarations
pub x = 42              # Public, type inferred as Int!
let y: String! = "hi"   # Private, explicit type
pub z: Int              # Type-only declaration

# Functions
pub add(a: Int!, b: Int!): Int! {
  a + b
}

# Classes (modules)
type Person {
  pub name: String!
  pub greet: String! {
    "Hello, I'm " + self.name
  }
}

# Assertions for testing
assert { add(2, 3) == 5 }
```

### Key Principles
- **Simplicity over complexity**: Clear, readable syntax
- **Type safety first**: Compile-time error prevention
- **GraphQL native**: Seamless API integration
- **Immutable by default**: Copy-on-write semantics for mutations

## Grammar Structure
The language grammar is defined in `pkg/sprout/sprout.peg` using PEG (Parsing Expression Grammar):

- **Expressions**: `Expr <- Class / Slot / Reassignment / Form`
- **Forms**: `Form <- Conditional / Lambda / Match / Assert / DefaultExpr / TypeHint / Term`
- **Terms**: `Term <- Literal / SelectOrCall / List / Object / Block / ParenForm / SymbolOrCall`

## Development Workflow
1. **Add test first**: Create `.bd` file with `assert { ... }` statements
2. **Modify grammar**: Update `pkg/sprout/sprout.peg` for syntax changes
3. **Update AST**: Add/modify structs in `pkg/sprout/ast_*.go`
4. **Implement methods**: Add `Hoist()`, `Infer()`, `Eval()` methods
5. **Regenerate**: Run `./hack/generate` after grammar changes
6. **Test**: Run `./tests/run_all_tests.sh` or specific tests

## File Organization
- `pkg/sprout/sprout.peg` - Grammar definition (PEG format)
- `pkg/sprout/ast_*.go` - AST node definitions and implementations
- `pkg/sprout/env.go` - Type environment and module system
- `pkg/sprout/infer.go` - Type inference engine
- `pkg/sprout/eval.go` - Runtime evaluation
- `tests/*.bd` - Test files (auto-discovered by test runner)

## Type Inference Flow
1. **Parse**: PEG parser generates AST from source
2. **Hoist**: Multi-pass hoisting for forward references
3. **Infer**: Hindley-Milner type inference with GraphQL schema
4. **Eval**: Runtime evaluation with type-safe operations

## Common Patterns
- **Builder pattern**: Methods return `self` for chaining
- **Copy-on-write**: Mutations create new objects, preserving immutability
- **Auto-calling**: Zero-argument functions called automatically
- **GraphQL integration**: Types and functions derived from schema

## Error Handling
- **Compile-time**: Type mismatches, undefined symbols, invalid syntax
- **Runtime**: Null pointer access, division by zero, assertion failures
- **Detailed messages**: Error locations and context provided

This language prioritizes safety, simplicity, and GraphQL integration over performance or low-level control.