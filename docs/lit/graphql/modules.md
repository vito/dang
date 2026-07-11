\use-plugin{dang}

# Modules and imports {#modules}

> Meta: three audiences, in this order so each can stop reading early — single-file users (next section), directory-module users, then people wiring up `dang.toml`. `import`/shadowing/cycles follow.

## A single file is a module

- top-level declarations are the module's surface
- public (the default) is exported, `let` is private (see [#fields])
- order doesn't matter — declarations are hoisted; forward references work

## A directory is also a module

- all `.dang` files in a directory are one module
- file boundaries are invisible to type resolution
- files load in unspecified order — code must be order-independent

```
mymod/
  main.dang
  types.dang
  utils.dang
```

- a type defined in `types.dang` can be constructed/extended from `utils.dang` or `main.dang` with no import

## `dang.toml` {#dang.toml}

`dang.toml` is discovered by walking up from the working directory (stopping at
a `.git` boundary).

```toml
[imports.Dagger]
dagger = true

[imports.MyApi]
schema = "./schema.graphqls"
service = ["go", "run", "./server"]

[imports.Remote]
endpoint = "https://api.example.com/graphql"
authorization = "Bearer ${API_TOKEN}"
```

The `endpoint`, `authorization`, and `headers` values support `${VAR}`
environment expansion; `$(...)` command substitution is not supported.

- one `[imports.<Name>]` per imported source; `<Name>` is the qualifier (`MyApi.User`)
- must specify at least one of `dagger`, `schema`, `endpoint`, or `service` — or `path`, to import a native Dang module instead of a GraphQL source (see [#dang-imports])
- `schema = "..."` — path to a local `.graphqls` SDL file (relative to `dang.toml`); used for type-checking and the LSP
- `endpoint = "..."` — GraphQL HTTP URL for runtime queries; if set without `schema`, the schema is introspected (and cached) from it
- `service = [...]` — command that starts a GraphQL server, printing its endpoint URL as the first stdout line; started lazily, killed on exit
- `dagger = true` — connect to a Dagger Engine session; `service` defaults to `["dagger", "session"]`
- `authorization = "..."` — value for the `Authorization` header
- `[imports.<Name>.headers]` — extra HTTP headers (table of key = value)
- `authorization` / `endpoint` / `headers` values support `${ENV_VAR}` expansion (note: the old `DANG_GRAPHQL_*` env-var config was dropped)
- runtime queries are GraphQL interop; see [#interop]

## Importing another Dang module {#dang-imports}

An import can point at **another Dang module** on disk instead of a GraphQL
source. Its public surface becomes available under the qualifier, exactly like a
GraphQL import.

```toml
[imports.Helpers]
path = "./lib/helpers"
```

- `path = "..."` — a directory of `.dang` files (relative to `dang.toml`), compiled as a module
- mutually exclusive with the GraphQL source kinds: `'path' cannot be combined with 'dagger', 'schema', 'endpoint', or 'service'`
- the module's public (non-`let`) declarations are its API; `let` declarations stay private **across the import boundary** — the same public/`let` rule as [#fields], now enforced between modules
- `import Helpers` then exposes `Helpers.Greeting` (qualified) and `Greeting` (unqualified), like any import
- transitive `path` imports work (an imported module resolves its own `dang.toml` from its directory); import cycles are reported: `import cycle detected: a -> b -> a`

### Per-module identity

- each importing module gets its **own instance** of an imported module — nothing unifies across the boundary
- two modules importing the same directory therefore hold **distinct copies** of its types: a `Widget` from one is not the `Widget` from the other, even when structurally identical
- assigning across that boundary is rejected with a hint to share behavior through an interface: `the "Widget" value here is a different type from the "Widget" expected: they come from different modules and do not unify across the module boundary; share behavior through an interface instead`
- there is no version reconciliation (no MVS): each `dang.toml` gets exactly what it declares

## `import` declarations

```dang
import Dagger
import MyApi
```

- exposes both *qualified* (`MyApi.User`) and *unqualified* (`User`) names
- covers types, root `Query`/`Mutation` functions, interfaces, unions, enums, scalars, and directives (see [#interop])
- enum *values* become unqualified too: `ACTIVE` (== `Status.ACTIVE`) if no collision (see [#enums-scalars])
- imported directives are usable unqualified: `@customDirective(...)` (see [#directives])

## Shadowing

- a local `type User { ... }` shadows the imported `User` (qualified `MyApi.User` still works)
- a local declaration also *resolves* an otherwise-ambiguous unqualified name (local wins)
- without a local shadow, an unqualified name provided by two imports is an error: `ambiguous reference to "Status": provided by imports [Other Test]` — must qualify
- same for unqualified `Mutation` and directives (`ambiguous reference to directive @experimental`)

## Ordering and cycles

- forward references across files work
- direct circular *variable initializers* are caught statically: `circular module variable initializer: a -> b -> a`
- cycles hidden behind an auto-called function or constructor default are caught at runtime when the variable is forced: `initialization cycle while evaluating variable "..."`
- circular *types* (interface-implementing-interface, mutually-referencing types across files) are fine

## What a module exports

- every public (non-`let`) field, type, interface, union, enum, scalar, directive
- nothing `let`

> Meta: `dang.toml` discovery walks up from the file's directory to the nearest `.git` boundary; the `dang` CLI runs the resolved module (see [#cli]).
