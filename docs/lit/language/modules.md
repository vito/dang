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

- one `[imports.<Name>]` per imported GraphQL source; `<Name>` is the qualifier (`MyApi.User`)
- must specify at least one of `dagger`, `schema`, `endpoint`, or `service`
- `schema = "..."` — path to a local `.graphqls` SDL file (relative to `dang.toml`); used for type-checking and the LSP
- `endpoint = "..."` — GraphQL HTTP URL for runtime queries; if set without `schema`, the schema is introspected (and cached) from it
- `service = [...]` — command that starts a GraphQL server, printing its endpoint URL as the first stdout line; started lazily, killed on exit
- `dagger = true` — connect to a Dagger Engine session; `service` defaults to `["dagger", "session"]`
- `authorization = "..."` — value for the `Authorization` header
- `[imports.<Name>.headers]` — extra HTTP headers (table of key = value)
- `authorization` / `endpoint` / `headers` values support `${ENV_VAR}` expansion (note: the old `DANG_GRAPHQL_*` env-var config was dropped)
- runtime queries are GraphQL interop; see [#graphql]

## `import` declarations

```dang
import Dagger
import MyApi
```

- exposes both *qualified* (`MyApi.User`) and *unqualified* (`User`) names
- covers types, root `Query`/`Mutation` functions, interfaces, unions, enums, scalars, and directives (see [#graphql])
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
