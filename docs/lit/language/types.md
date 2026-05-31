\use-plugin{dang}

# Types and nullability {#types}

> Meta: nullability is the page's main attraction. Most readers won't have seen GraphQL-style `T!` outside of a schema and may misread it as "definitely-null" or "force-unwrap."

## Built-in types

- `Int!`, `Float!`, `String!`, `Boolean!`, `ID!`
- `[T]` and `[T]!` — lists
- `{{ ... }}` — records
- custom GraphQL scalars: `URL`, `Timestamp`, `JSON`, ...
- user-defined: `type`, `interface`, `union`, `enum`, `scalar` (see their pages)

## The `!` sigil

- `T!` — non-null `T`
- `T` (no bang) — nullable `T`
- assignability: `T!` satisfies `T`, but `T` does **not** satisfy `T!`
- the bang is at the *outside* of the type: `[String!]!` = non-null list of non-null strings

## Lists, nullability matrix

| written | meaning |
|---|---|
| `[T]` | nullable list of nullable T |
| `[T]!` | non-null list of nullable T |
| `[T!]` | nullable list of non-null T |
| `[T!]!` | non-null list of non-null T |

## Null propagation

- `nullable.field` is nullable even if `field: T!`
- chains short-circuit: `a.b.c` is null if any link is null
- recovers via `??` (see [operators](./operators.md)) or narrowing (see [flow typing](./flow-typing.md))

## Type variables

- single lowercase letters: `a`, `b`
- used in generic function signatures
- inferred at call sites

## Type hints / casts: `::`

- `expr :: Type!` annotates an expression's type
- the **only** place implicit scalar/enum coercion happens (e.g. `String!` → `URL!`)
- nullable → non-null casts are a runtime assertion (will fail on `null`)

> Meta: `::` deserves a worked example showing the difference between a type *hint* (narrowing/disambiguation) and a type *cast* (coercion). The runtime-assertion behavior for non-null casts is a footgun worth a short callout.

## Coercion rules

- assignment / arguments: pure subtyping, **no** scalar coercion
- `::` casts: scalar/enum/ID coercion permitted
- ongoing work — see `soundness.md` for the model being moved toward
