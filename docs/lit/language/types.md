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
- assignability: `T!` satisfies `T`, but `T` does **not** satisfy `T!` (`null` can't init a `T!` slot)
- grammar: `!` is a postfix wrapper applied to a (possibly list) type — `NonNull <- inner:Type BangToken`. So `[String!]!` parses as `NonNull(List(NonNull(String)))` = non-null list of non-null strings
- object/`type` literals are always non-null in Dang (no nullable-object form)
- `[T]` and `Int!` are unrelated — you can't assign `Int!` to `[Int]`

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
- recovers via `??` (see [#operators]) or flow narrowing (see [#flow-typing])

## Type variables

- single lowercase letters: `a`, `b`
- used in generic function signatures
- inferred at call sites

## Type hints / casts: `::`

- `expr :: Type!` annotates an expression's type (`TypeHint` node)
- grammar: binds only a bare `Term` on the left, so wrap compound exprs in parens — `(a + b) :: T!` (see [#operators] for precedence)
- `::` is the explicit materialization/coercion boundary: `String` → custom scalar (`URL!`, `Timestamp!`, …), enum (`"PASSED" :: Status!`), and `ID` coercions go here
- coercion source is limited: only **`String`** values coerce to custom scalars/enums. `42 :: URL!` is rejected **statically** ("type hint mismatch: Int!, but hint expects URL!")
- enum casts are checked at **runtime**: `"NOPE" :: Status!` → "invalid enum value"
- nullable → non-null casts do **not** strip the wrapper statically; they defer to a runtime `Coerce` that rejects null: `fromJSON("null") :: String!` → "null is not allowed for String!"

> Meta: `::` deserves a worked example showing the difference between a type *hint* (narrowing/disambiguation, e.g. `[] :: [String!]!` binding a type variable) and a type *cast* (coercion, e.g. `myUrl :: URL!`). The runtime-assertion behavior for non-null casts is a footgun worth a short callout.

## Coercion rules

- assignment / arguments: pure subtyping (`hm.Assignable`), **no** scalar coercion — a non-literal `String!` won't pass where `URL!` is expected: `fetchURL(url: myUrl)` errors "cannot use String! as Test.URL!"
- **exception**: *literal* expressions (string/template literals, list literals of them) auto-coerce to compatible scalars at value-handoff boundaries (call args, typed slots, returns) — `fetchURL(url: "https://…")` is fine, and ``fetchURL(url: `https://${host}`)`` works too
- `::` casts: explicit `String`/enum/`ID` coercion permitted (see above)
- list merges are pure — `String!` does not become `ID!` element-wise
- ongoing work — see `soundness.md` for the model being moved toward
