\use-plugin{dang}

# Modules and imports {#modules}

> Meta: this page has three audiences — single-file users, directory-module users, and people wiring up `dang.toml`. Consider three subsections in that exact order so each can stop reading early.

## A single file is a module

- top-level declarations are the module's surface
- `pub` is exported, `let` is private
- order doesn't matter — declarations are hoisted

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

> Meta: show the directory layout from `tests/test_dir_module/` as the canonical example.

## `dang.toml`

```toml
[imports.Dagger]
dagger = true

[imports.MyApi]
schema = "./schema.graphqls"
service = ["go", "run", "./server"]
```

- one `[imports.<Name>]` per imported GraphQL source
- `schema = "..."` for a static schema file (introspected)
- `service = [...]` for a process to spawn and connect to
- `dagger = true` shorthand for the Dagger session protocol
- see [GraphQL configuration](../graphql-config.md) for env-var alternatives

## `import` declarations

```dang
import Dagger
import MyApi
```

- exposes both *qualified* (`MyApi.User`) and *unqualified* (`User`) names
- enum values become unqualified too: `ACTIVE` if no collision

## Shadowing

- a local `type User { ... }` shadows the imported `User` (qualified access still works)
- multiple imports with the same name require qualifying — ambiguous unqualified use is an error

## Ordering and cycles

- forward references across files work
- circular *initializer* chains are caught at type-check time
- circular *types* (interface-implementing-interface) are fine

## What a module exports

- every `pub` field, type, interface, union, enum, scalar, directive
- nothing `let`
