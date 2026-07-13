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
- `{{ }}` **always evaluates its fields concurrently**, failing fast on the first error. A selection and a record literal are one construct, so the rule is uniform: over a GraphQL receiver it's the single batched query above, over a plain object it's parallel Dang evaluation, and over a list it runs the elements in parallel.
- **Aliases** rename a field in the result, GraphQL-style (alias before the colon): `user.{{ fullName: name, email }}`. A bare field is shorthand for aliasing to itself — `user.{{ name }}` ≡ `user.{{ name: name }}` — exactly as `{{ name }}` ≡ `{{ name: name }}` in a record literal.
- Aliases become real GraphQL aliases, so the **same field** can be selected more than once with different args: `user.{{ small: avatarUrl(size: 100), large: avatarUrl(size: 200) }}`.
- A record literal is `{{ }}` with no receiver, so it has the same parallelism: `{{ users: users.{{ name }}, posts: posts.{{ title }} }}` issues both queries at once.

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
- The type name may be module-qualified to match an imported type: `... on Dagger.Editor` (or `... on Dagger.Editor!`).
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
- The elements are projected **concurrently** (a non-GraphQL list fans out across its elements in parallel; a GraphQL list is still one batched query).
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
- Non-null violations and GraphQL errors **raise** — recoverable via `rescue` (e.g. `dir.file("VERSION").contents rescue null`).

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
- One `[imports.<Name>]` per source; `<Name>` is the qualifier (`MyApi.User`). Must specify at least one of `dagger`, `schema`, `endpoint`, `service` — or `path` (native Dang module, below).
- `schema` — path to a local `.graphqls` SDL file (relative to `dang.toml`); used for type-checking and the LSP.
- `endpoint` — GraphQL HTTP URL for runtime queries; if set without `schema`, the schema is introspected (and cached) from it.
- `service` — command starting a GraphQL server that prints its endpoint URL as the first stdout line; started lazily, killed on exit.
- `dagger = true` — connect to a Dagger Engine session; `service` defaults to `["dagger", "session"]`.
- `authorization` — value for the `Authorization` header. `[imports.<Name>.headers]` — extra HTTP headers.
- `authorization` / `endpoint` / `headers` support `${ENV_VAR}` expansion; `$(...)` command substitution is **not** supported. (The old `DANG_GRAPHQL_*` env config was dropped.)
- Discovery walks **up** from the working directory, stopping at a `.git` boundary. Commit `dang.toml` — don't include credentials inline; reference env vars.

### Native Dang module imports
- `path = "./dir"` imports **another Dang module** (a directory of `.dang` files, relative to `dang.toml`) instead of a GraphQL source; mutually exclusive with `dagger`/`schema`/`endpoint`/`service` (`'path' cannot be combined with ...`).
- The module's public (non-`let`) surface becomes the import's API; `let` stays private **across the boundary** (the [#fields] public/`let` rule, now enforced between modules).
- **Per-module identity:** each importing module gets its own instance — two modules importing the same directory hold distinct copies of its types, and same-named types from different instances don't unify (`the "Widget" value here is a different type from the "Widget" expected ... share behavior through an interface instead`). No MVS/version reconciliation.
- Transitive `path` imports resolve each module's own `dang.toml`; cycles are reported (`import cycle detected: a -> b -> a`).

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
- Suffix form attaches to the field/type; prefix form sits on its own line before the declaration. Both forms apply to types, scalars (`scalar Tag @experimental`), fields, and function/field arguments (`process(user: Person! @experimental)`). Multiple prefix directives go on separate lines.
- Args: named (`@cache(ttl: 60, key: "user")`) or positional shorthand (`@cache(60, key: "user")`); positionals before named (`positional arguments must come before named arguments`). Declaration defaults apply when omitted.

### Qualified access
- `@MyApi.experimental` disambiguates when an import shadows a name. Two imports providing the same unqualified directive → `ambiguous reference to directive @experimental`.
- Qualified access is **suffix-only** — the prefix form does not accept a `Module.` scope (`@MyApi.experimental name: ...` on its own line is a syntax error).

### Common built-ins
- `@defaultPath(path: ...)` — provides a default for a `Directory!` field (Dagger)
- `@example(code: String!)` — attaches a runnable example to a declaration, read by doc tooling (declared in the prelude, available everywhere). Idiomatically the code is a language-tagged fenced template so editors highlight it as Dang:
  ````dang
  """
  doubles a number
  """
  @example(```dang
  double(21)
  ```)
  double(n: Int!): Int! { n * 2 }
  ````
- `@ignorePatterns(patterns: [...])` — filtering metadata
- plus every directive imported from connected schemas
