\use-plugin{dang}
\literate-fences

# Modules and imports {#modules}

> Meta: three audiences, in this order so each can stop reading early — single-file users (next section), directory-module users, then people wiring up `dang.toml`. `import`/shadowing/cycles follow.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## A single file is a module

- top-level declarations are the module's surface
- public (the default) is exported, `let` is private (see [#fields])
- order doesn't matter — declarations are hoisted; forward references work

```dang
greeting: String! { `${salutation}, ${audience}!` }   # exported
let salutation = "Hello"                              # private to the module
let audience = "world"

greeting
```

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
dang = "2.4"

[imports.Dagger]
dagger = "v0.19.0"

[imports.MyApi]
schema = "./schema.graphqls"
service = ["go", "run", "./server"]

[imports.Remote]
endpoint = "https://api.example.com/graphql"
authorization = "Bearer ${API_TOKEN}"
```

The `endpoint`, `authorization`, and `headers` values support `${VAR}`
environment expansion; `$(...)` command substitution is not supported.

- `dang = "X.Y"` — the Dang version the project's source is written for (its release's major.minor). A leading `v` and a patch are accepted (`"v2.1.4"` means `"2.1"`: patch releases don't change the language). Newer Dang releases keep parsing older versions' syntax, so a project keeps working until you move it forward with `dang migrate` (see [#cli]). A version newer than the running Dang is an error asking you to upgrade. Left out, the project follows the running Dang's version, like source no `dang.toml` governs, and moves with each release; declare it (`dang init` writes it for you) to keep a version.
- one `[imports.<Name>]` per imported source; `<Name>` is the qualifier (`MyApi.User`)
- must specify at least one of `dagger`, `schema`, `endpoint`, or `service` — or `path`, to import a native Dang module instead of a GraphQL source (see [#dang-imports])
- `schema = "..."` — path to a local `.graphqls` SDL file (relative to `dang.toml`); used for type-checking and the LSP
- `endpoint = "..."` — GraphQL HTTP URL for runtime queries; if set without `schema`, the schema is introspected (and cached) from it
- `service = [...]` — command that starts a GraphQL server, printing its endpoint URL as the first stdout line; started lazily, killed on exit
- `dagger = "X.Y.Z"` — connect to a Dagger Engine session, written against that Dagger version's API: like a Dagger module's `engineVersion` (copy it verbatim; a prerelease such as `v1.0.0-beta.21` is kept and gets that prerelease's API; build metadata such as `+abc` is dropped), written like `dang = "..."` (`v` and patch optional). `service` defaults to `["dagger", "session"]`; Dang appends `--version vX.Y.Z` (e.g. `--version v1.0.0-beta.21`) to it, so newer engines keep serving that API, and introspects a Dagger module's schema at it. An engine of an older release than the version (e.g. a `v1.0.0` engine for `v1.0.1`) is an error asking you to upgrade Dagger; as in Dagger, a later prerelease of the engine's own release, or that release itself (e.g. `v1.0.0` on a `v1.0.0-beta.22` engine), is not, and gets the engine's newest API; a version older than the engine still supports (`v0.19.0` as of Dagger 0.19) fails to start the session. Can't be combined with `endpoint`. In a Dagger module, keep it equal to the module's `engineVersion`: Dagger runs the module's functions at that version. `dagger = true` (the older form) connects without a version, so the API follows the `dagger` CLI and a module's schema its `engineVersion`; it's the form to use with `endpoint`
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
- the namespace holds only the module's own declarations: Dang's builtins and prelude, which the module's code uses as usual, aren't reachable through it (`Helpers.toString` or `Helpers.Path` is an error)
- transitive `path` imports work (an imported module resolves its own `dang.toml` from its directory); import cycles are reported: `import cycle detected: a -> b -> a`

### Per-module identity

- each importing module gets its **own instance** of an imported module — nothing unifies across the boundary
- two modules importing the same directory therefore hold **distinct copies** of its types: a `Widget` from one is not the `Widget` from the other, even when structurally identical
- assigning across that boundary is rejected with a hint to share behavior through an interface: `the "Widget" value here is a different type from the "Widget" expected: they come from different modules and do not unify across the module boundary; share behavior through an interface instead`
- there is no version reconciliation (no MVS): each `dang.toml` gets exactly what it declares

## `import` declarations

An `import` names one of the `[imports.<Name>]` entries. These docs come with
one called `Demo`, a small GraphQL schema served in-process (see
[#interop]); each request it receives is printed as `→ query …`:

```dang
import Demo
```

An import exposes both *qualified* (`Demo.User`) and *unqualified* (`User`)
names. That covers types, root `Query`/`Mutation` functions, interfaces,
unions, enums, scalars, and directives (see [#interop]):

```dang
[Demo.primaryUser.name, primaryUser.name]
```

Enum *values* become unqualified too, when no other import has the same
name (see [#enums-scalars]):

```dang
[status == ACTIVE, Demo.Status.ACTIVE == Status.ACTIVE]
```

- imported directives are usable unqualified: `@customDirective(...)` (see [#directives])
- the import's namespace holds only what the schema defines, plus GraphQL's built-in scalars (`Demo.String`, `Demo.Int`, …); Dang's own builtins and types aren't in it, so `Demo.print` or `Demo.Path` is an error unless the schema defines that name

## Builtin imports

Every module also gets Dang's builtin namespaces — `JSON`, `YAML`, `TOML`,
`Random` and `UUID` — as imports it doesn't write (see [#json-yaml] and
[#stdlib]). They are reached qualified only:

```dang
{{encoded: JSON.encode([1, 2]), uuidLength: UUID.v4.length}}
```

- there is no bare `encode`, `decode` or `int`: always `JSON.decode(…)`, `Random.int(…)`
- a declaration of the same name in the module shadows one: a `type JSON { … }` or `scalar JSON` both as a type and as a value, a `let Random = …` as a value only (`Random!` as a type, and `@JSON.field` directives, still reach the builtin)
- an import of the same name replaces one: with `[imports.YAML]` in `dang.toml`, a file that writes `import YAML` (or gets it as an auto-import) sees that import as `YAML`, and other files still see the builtin
- an unqualified name from an explicit import never overrides one: a schema's `scalar JSON` (Dagger has one) is reached as `Dagger.JSON`, and `JSON` stays the codec namespace
- like every import of a module, they aren't part of what it exports: `Demo.YAML`, or `Helpers.YAML` from a native module, is an error unless that schema or module defines a `YAML`

## Shadowing

A local declaration shadows an imported name of the same spelling, and the
qualified name still reaches the import:

```dang
type User {
  name: String!
  origin: String! = "local"
}

let remote: Demo.User! = Demo.primaryUser

[User("Ada").origin, remote.name]
```

The two `User`s are different types, so a Demo user doesn't fit the local
one. Types match by name, not shape: even a local `User` with exactly
Demo's fields would be rejected.

```dang-failure
let mine: User! = primaryUser
```

- a local declaration also *resolves* an otherwise-ambiguous unqualified name (local wins)
- without a local shadow, an unqualified name provided by two imports is an error: `ambiguous reference to "Status": provided by imports [Other Test]` — must qualify
- same for unqualified `Mutation` and directives (`ambiguous reference to directive @experimental`)

## Ordering and cycles

- forward references across files work
- circular *types* (interface-implementing-interface, mutually-referencing types across files) are fine

Direct circular *variable initializers* are caught statically:

```dang-failure
left: Int! = right
right: Int! = left
```

Cycles hidden behind an auto-called function or constructor default are
caught at runtime, when the variable is first read. Run as a script, this
reports `initialization cycle while evaluating variable "seed"`:

```dang-static
seed: Int! = grow
grow: Int! { seed * 2 }
```

## What a module exports

- every public (non-`let`) field, type, interface, union, enum, scalar, directive
- nothing `let`

> Meta: `dang.toml` discovery walks up from the file's directory to the nearest `.git` boundary; the `dang` CLI runs the resolved module (see [#cli]).
