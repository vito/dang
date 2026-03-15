---
name: codebase-map
description: Orientation guide for the Dang codebase. Use when you need to find where something is implemented or understand the project structure.
---

# Codebase Map

## Core Language (`pkg/dang/`)

| File | Purpose |
|------|---------|
| `dang.peg` | PEG grammar definition |
| `ast_*.go` | AST node types and Eval methods |
| `eval.go` | Evaluator setup, `addBuiltinFunctions()` |
| `env.go` | Environment/scope, `registerBuiltinTypes()` |
| `stdlib.go` | All builtin methods and functions (String, Int, List, etc.) |
| `stdlib_random.go` | Random/UUID builtins |
| `stdlib_yaml.go` | YAML builtins |
| `builtins.go` | `Method()`/`Builtin()` DSL framework, `Args`, `ToValue` |
| `ast_literals.go` | Type singletons (`StringType`, `IntType`, etc.) |
| `hm/` | Hindley-Milner type system |

## Tests

| Location | Purpose |
|----------|---------|
| `tests/test_*.dang` | Dang language tests |
| `tests/*_test.go` | Go test harness |

## GraphQL Test Server

| Location | Purpose |
|----------|---------|
| `tests/internal/server/` | Test GraphQL server |
| `tests/internal/server/resolvers.go` | Auto-generated resolvers |
| `tests/internal/server/resolvers.helpers.go` | Helper functions (add new helpers here, NOT in resolvers.go) |

## Key Checks

- `test:language` — Dang language tests
- `test:dagger` — Dagger SDK tests
- `test:lsp` — LSP tests
- `test:treesitter` — Tree-sitter tests
- `lint` — Linter
