---
name: dang-language
description: Dang language reference for writing, editing, and reviewing `.dang` code — syntax, types/nullability, prototype objects, copy-on-write mutation, control flow, errors, GraphQL interop, stdlib, and CLI. Use when authoring or reviewing `.dang` files or Dang modules, or any time precise Dang language behavior matters.
---

# Dang Language Reference

**Audience: people writing Dang.** This skill is for *using* the language —
authoring `.dang` scripts and modules and calling GraphQL APIs (including
Dagger) with it. It is not about developing the Dang compiler itself; for that,
see the contributor skills (`builtin-dsl`, `dang-internals`, `editor-syntaxes`,
`testing`).

Dang is a statically typed scripting language whose types and root functions
come from a **GraphQL schema**. Hindley-Milner inference; type annotations are
usually optional. `T` is nullable, `T!` is non-null.

## Mental model (read this first)

Four ideas the rest of the language hangs on:

- **Schema-driven types** — `import`ing a schema makes every type and root
  `Query`/`Mutation` field part of the language. The "standard library" is
  whatever schema you connect (Dagger, GitHub, your own API).
- **Prototype objects** — `type Foo` declares both a type *and* its constructor
  function. Methods and fields are indistinguishable in syntax.
- **Immutability + copy-on-write** — values never change. Methods that look
  mutating return a **forked copy** of the receiver; mutating methods must
  `return self` to surface it. `Foo(42).incr.a == 43`, original untouched.
- **Null tracking** — `String` ≠ `String!` in the type system, with
  flow-sensitive narrowing so you rarely write casts.

## Distinctive features (what surprises newcomers)

- **Optional parens** for zero-arg calls — a field and a zero-arg method read
  the same (`obj.greet`, not `obj.greet()`); zero-arg functions/constructors
  *auto-call* on bare reference. Use `&name` to get the function without calling.
- **No `return` for the normal result** — the last expression is the value.
  `return` is for *early* exit only.
- **No truthiness** — `if` conditions must be `Boolean!`.
- **Everything is an expression** — `if`, `case`, `loop`, `rescue` all yield values.
- **Multi-field selection** — `user.{{name, posts.{{title}}}}` becomes one GraphQL
  query (lazy; sent when forced). Fields can be aliased (`user.{{full: name}}`). It
  is the same `{{ }}` construct as a record literal; every `{{ }}` evaluates its
  fields concurrently.
- **`#` comments, no `//`**; docstrings are real `"""..."""` strings before a
  declaration. Record literals use **double braces** `{{ ... }}`.
- **No `for`/`while` keyword** — iterate with `xs.each { x => ... }`; repeat-until-`break`
  is the `loop { ... }` builtin.
- **Directives** (`@deprecated`) are typed declarations, not comment pragmas.
- **Errors are for errors**, not control flow or expected absence (use `null`).

## Quick syntax

```dang
x: Int! = 42                     # public binding (a type makes it public)
let secret = "hidden"            # private to file/type
add(a: Int!, b: Int!): Int! { a + b }   # function; last expr is result
motd: String! { "hi" }           # zero-arg method/computed field (no parens)

type Counter {
  value: Int!                    # no default -> required constructor param
  incr: Counter! { value += 1; self }   # forks self, returns the copy
}
assert { Counter(0).incr.incr.value == 2 }

[1, 2, 3].map { x => x * 2 }     # block arg (Ruby-style), trailing braces
if (v != null) { v } else { "?" }   # v narrowed to T! in the then-branch
```

## Where to look (routing)

Load the reference file that matches the question:

| File | Covers |
|---|---|
| `reference/syntax.md` | file layout, comments, identifiers, reserved words, docstrings, **literals** (numbers/strings/lists/records), **operators** + precedence, the PEG **grammar** |
| `reference/types.md` | built-in types, the `!` nullability sigil, list nullability matrix, null propagation, `::` type hints/casts, coercion rules, **flow-sensitive narrowing** (and its gaps), **enums**, **custom scalars** |
| `reference/objects.md` | **fields** & `let`, **functions** & `&fn` refs, **blocks** & control-flow handoff, **`type` objects** & constructors (`new`), `self`, computed fields, **mutation / copy-on-write**, **interfaces** & **unions** + variance |
| `reference/control-flow.md` | `if`/`else`, `case` (value + type patterns), `loop`, `break`/`continue`/`return`, **errors** (`raise`, the postfix `rescue` operator, the `Error` interface, when to raise vs. return null) |
| `reference/stdlib.md` | top-level builtins (`assert`/`print`/`loop`/`toString`), **`String!` methods** (incl. regex/`Match`, base64), **list `[T]!` methods**, **`JSON`/`YAML`/`TOML` codec namespaces** (`encode`/`decode`), `Random`, `UUID`, error types |
| `reference/graphql.md` | **GraphQL interop** (selection, inline fragments, laziness/forcing, mutations), **modules** & directory modules, **`dang.toml`**, `import`, shadowing, **directives** |
| `reference/cli.md` | the `dang` CLI, `dang fmt`, the REPL and its `:` commands, exit codes, LSP/editor integration |

When writing non-trivial `.dang` code, the most load-bearing files are
`objects.md` (CoW mutation is the #1 source of confusion) and `types.md`
(nullability + coercion). For Dagger-module specifics, see the separate
`dang-dagger-modules` skill.

## Testing your code

`assert { expr }` is built in — no framework needed. It runs the block and
raises an `AssertionError` (with the source expression and sub-values in the
message) if the result isn't truthy. Drop assertions straight into a script:

```dang
assert { Counter(0).incr.value == 1 }
assert(message: "must be positive") { x > 0 }
```
