# GraphQL Interop, Modules, dang.toml, Directives

## Schema-as-stdlib
When you `import` a schema, every type and root function in it becomes part of the language:
- every imported schema's types are first-class Dang types
- root `Query` and `Mutation` fields become callable functions
- enum and input types map directly; custom scalars become Dang scalars

## Calling a field
```dang
let users = users(limit: 10)
```
- Named args as in the schema; positional args in schema-declared order.
- A no-arg field calls automatically: `serverInfo.platform` (no parens).

## Multi-field selection (the headline feature)
```dang
user.{{ name, email, posts.{{ title, createdAt }} }}
```
- Desugars to a **single GraphQL query**. Nested selections to arbitrary depth.
- Arguments on nested fields: `user.{{ posts(first: 5).{{ title }} }}`; positional works too: `users.{{ posts(1).{{ ... }} }}`.
- Selection on a nullable receiver propagates null: if `user` is `null`, `user.{{ name }}` is `null` (not an error).
- The result is a **record** (`{{ ... }}`); access fields by name.
- **Aliases** rename a field in the result, GraphQL-style (alias before the colon): `user.{{ fullName: name, email }}`. A bare field is shorthand for aliasing to itself — `user.{{ name }}` ≡ `user.{{ name: name }}` — exactly as `{{ name }}` ≡ `{{ name: name }}` in a record literal.
- Aliases become real GraphQL aliases, so the **same field** can be selected more than once with different args: `user.{{ small: avatarUrl(size: 100), large: avatarUrl(size: 200) }}`.

## Inline fragments (unions/interfaces)
```dang
node(id: "x").{{
  ... on User {{ name, email }}
  ... on Post {{ title }}
}}
```
- Type-conditional selection on unions and interfaces; the result narrows in `case`.
- After selection you only have the selected fields: accessing an unselected field is a *compile* error (`field "lives" not found in Cat`), even after a `case` narrows the type.
- Type conditions resolve against the receiver's (GraphQL) schema, not a local type shadowing the name.
- The field set uses the same double braces as any selection (`... on User {{ name, email }}`).
- Can nest: `... on Post {{ title, author.{{name}} }}`, and `edges.{{ node.{{ ... on User {{ ... }} }} }}`.

### Lazy inline fragments (no field block)
- `... on User` yields a typed reference / type assertion without selecting fields; chainable: `node(id).{{... on User}}.name`.
- Works on a single value or a list (`pets.{{ ... on Cat, ... on Dog }}`, comma/newline-separated, elementwise on lists).
- Non-matching assertion returns `null` (`cat.{{... on Dog}} == null`).
- Non-null form `... on Cat!` asserts and unwraps; a mismatch is a *runtime* error: `inline fragment type assertion failed: expected one of Cat, got Dog`.

## Lists of objects
```dang
users.{{ name, email }}    # distributes over elements -> [ {{ name, email }} ]
users.{{ name }}[0].name   # index to force the query
```
- A selection on a list reaches **into each element** (mirrors GraphQL: a selection set on a list-typed field applies per element). `.{{ … }}` is element-wise; `.field` treats the list as the receiver and resolves a list method (`.length`, `.map`). So `xs.{{ f }}` is not `xs.f`.
- Nested selections distribute the same way: `users.{{ name, posts.{{ title }} }}`.
- Result records compare by **value**, so lists of projections compare directly (`users.{{ name }} == [{{ name: "Alice" }}]`). The result is an ordinary list — chain `.map`, index, etc.

## Mutations
- Root `Mutation` fields are functions like queries; the result is whatever the schema declares.
- Side effects happen when the call **executes**, which is when its value is **forced** (same laziness rules as queries).

## Laziness / forcing
GraphQL field access is **lazy**. A GraphQL value accumulates a query chain (`.field`, `.{{...}}`, args); no request is sent until the value is **forced** — materialized at an expected-type boundary (assertion, `print`, assignment to a typed field, indexing into a result, etc.). Forcing runs the built-up selection as a single request. This is what makes `user.{{ name, posts.{{ title }} }}` one round-trip.

## Equality
- `==`/`!=` on GraphQL objects compare by **reference identity** — no network call. A GraphQL object's identity is the query that produced it, so the same handle equals itself, but two independent constructions don't, even when they denote the same server object: `primaryUser == user(id: "1")` is `false`.
- To ask whether two objects are the *same server entity*, compare an identifying field explicitly: `a.id == b.id`. That forces the fetch where it's visible rather than hiding I/O inside `==`.
- A `.{{ }}` selection is different: it materializes an **anonymous record**, which compares by **value** (same fields + equal values ⇒ equal). Reference identity applies to the GraphQL object *handle*, not to a selected record. See the object-equality rules in `objects.md`.

## Errors from the server
- Non-null violations and GraphQL errors **raise** — catchable via `try`/`catch`.

## Input types
User-defined `type`s and imported GraphQL **input** types share the same `Type(args)` construction syntax:
```dang
Mutation.createUser(input: CreateUserInput(name: "Alice", email: "..."))
```

## Multiple endpoints
- One `[imports.Name]` per endpoint; each becomes its own qualified namespace.
- Types from different endpoints are *distinct* even if same-named (`Test.User` ≠ `Other.User`).
- A bare unqualified name provided by two imports is ambiguous — must qualify.

---

## Modules and imports

### A single file is a module
- Top-level declarations are the module's surface; a typed declaration is exported, `let` keeps it private.
- Order doesn't matter — declarations are hoisted; forward references work.

### A directory is also a module
- All `.dang` files in a directory are **one module**; file boundaries are invisible to type resolution. Files load in unspecified order — code must be order-independent. A type defined in `types.dang` can be constructed/extended from `utils.dang` with no import.

### `dang.toml`
```toml
[imports.Dagger]
dagger = true

[imports.MyApi]
schema = "./schema.graphqls"
service = ["go", "run", "./server"]

[imports.Remote]
endpoint = "https://api.example.com/graphql"
authorization = "Bearer ${API_TOKEN}"
[imports.Remote.headers]
X-Custom = "value"
```
- One `[imports.<Name>]` per source; `<Name>` is the qualifier (`MyApi.User`). Must specify at least one of `dagger`, `schema`, `endpoint`, `service`.
- `schema` — path to a local `.graphqls` SDL file (relative to `dang.toml`); used for type-checking and the LSP.
- `endpoint` — GraphQL HTTP URL for runtime queries; if set without `schema`, the schema is introspected (and cached) from it.
- `service` — command starting a GraphQL server that prints its endpoint URL as the first stdout line; started lazily, killed on exit.
- `dagger = true` — connect to a Dagger Engine session; `service` defaults to `["dagger", "session"]`.
- `authorization` — value for the `Authorization` header. `[imports.<Name>.headers]` — extra HTTP headers.
- `authorization` / `endpoint` / `headers` support `${ENV_VAR}` expansion; `$(...)` command substitution is **not** supported. (The old `DANG_GRAPHQL_*` env config was dropped.)
- Discovery walks **up** from the working directory, stopping at a `.git` boundary. Commit `dang.toml` — don't include credentials inline; reference env vars.

### `import` declarations
```dang
import Dagger
import MyApi
```
- Exposes both *qualified* (`MyApi.User`) and *unqualified* (`User`) names.
- Covers types, root `Query`/`Mutation` functions, interfaces, unions, enums, scalars, and directives.
- Enum *values* become unqualified too: `ACTIVE` (== `Status.ACTIVE`) if no collision.
- Imported directives are usable unqualified: `@customDirective(...)`.

### Shadowing & ambiguity
- A local `type User { ... }` shadows the imported `User` (qualified `MyApi.User` still works); a local declaration also resolves an otherwise-ambiguous unqualified name (local wins).
- Without a local shadow, an unqualified name provided by two imports is an error: `ambiguous reference to "Status": provided by imports [Other Test]` — must qualify. Same for `Mutation` and directives.

### Ordering & cycles
- Forward references across files work.
- Direct circular variable initializers caught statically (`circular module variable initializer: a -> b -> a`); cycles behind an auto-called function/constructor default caught at runtime (`initialization cycle while evaluating variable "..."`).
- Circular *types* (interface-implementing-interface, mutually-referencing types) are fine.

### What a module exports
- Every public (typed) field, type, interface, union, enum, scalar, directive. Nothing `let`.

---

## Directives

A directive is a typed annotation attachable to a declaration or argument — a **real declaration** (type-checked args, locations, defaults), not a comment pragma. It's **structural, not semantic**: it attaches typed data; it never runs code at a call site and has no runtime effect on the annotated field. It's metadata read by tooling / the schema / a host (e.g. Dagger reads `@defaultPath`).

### Declaration
```dang
directive @deprecated(reason: String = "No longer supported") on FIELD_DEFINITION | OBJECT
directive @experimental on FIELD_DEFINITION | ARGUMENT_DEFINITION
directive @auth(role: String!) on FIELD_DEFINITION
directive @cache(ttl: Int! = 300, key: String) on FIELD_DEFINITION
```
- A directive with no args omits the `()`. Arg list is like a function's: required, optional, defaulted.
- Locations (`on ...`): `FIELD_DEFINITION`, `OBJECT`, `ARGUMENT_DEFINITION`, `INTERFACE`, `UNION`, `ENUM`, ... (mirror GraphQL); `|`-separated for multiple positions.

### Applying
```dang
type Person @deprecated(reason: "use NewPerson") {
  name: String! @deprecated
  email: String! @cache(ttl: 60)
}

@check
mixedField: String! @cache(ttl: 120) { "mixed" }   # prefix and suffix both collected
```
- Suffix form attaches to the field/type; prefix form sits on its own line before the declaration. Both forms apply to types, fields, and function/field arguments (`process(user: Person! @experimental)`). Multiple prefix directives go on separate lines.
- Args: named (`@cache(ttl: 60, key: "user")`) or positional shorthand (`@cache(60, key: "user")`); positionals before named (`positional arguments must come before named arguments`). Declaration defaults apply when omitted.

### Qualified access
- `@MyApi.experimental` disambiguates when an import shadows a name. Two imports providing the same unqualified directive → `ambiguous reference to directive @experimental`.
- Qualified access is **suffix-only** — the prefix form does not accept a `Module.` scope (`@MyApi.experimental name: ...` on its own line is a syntax error).

### Common built-ins
- `@defaultPath(path: ...)` — provides a default for a `Directory!` field (Dagger)
- `@ignorePatterns(patterns: [...])` — filtering metadata
- plus every directive imported from connected schemas
