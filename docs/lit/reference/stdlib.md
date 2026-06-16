\use-plugin{dang}

# Standard library reference {#stdlib}

> Meta: alphabetical reference, not a tutorial. Each entry: signature, one-line description, one tiny example. Group by module/receiver. Cross-link to the conceptual page that introduces the API.

> This page is generated from the builtin registry in `pkg/dang` (`stdlib.go`, `stdlib_random.go`, `stdlib_regexp.go`, `assert.go`). Each entry's signature, one-line description, and example come straight from the builtin's definition, so the reference can't drift from the implementation — to change an entry, edit the builtin's `.Doc(...)` or `.Example(...)`. Each example is a live REPL: press **Run** to evaluate it (and then keep typing — state carries across entries, just like the CLI).

## Top-level functions

\stdlib-functions

> `print` and `assert` return `null` — there is no `Void` type; treat the result as `null`. The `JSON`/`YAML`/`TOML` codec namespaces (`encode`/`decode`) are covered in depth on [#json-yaml].

## `String!` methods

> See [#strings]. Note: `.length`/`.isEmpty` are **list-only** — there is no String length/isEmpty builtin.

> Regex methods take a `Regexp!` pattern. Backtick template strings auto-coerce to the `Regexp` scalar, so a pattern is usually written as `` `\d+` `` (Go `regexp/syntax`).

\stdlib-methods{String}

### `Match` object

> Returned by `.match` (and as elements of `.matchAll`); a missing match is `null`. See [#strings].

\stdlib-methods{Match}

## `[T]!` methods

> See [#collections]. List methods are registered on the `List` module, so signatures show the element type as the type variable `a` (and block result types as `b`). Block params are named `item`/`index` — and `acc` for `.reduce`.

\stdlib-methods{List}

## `Map[a]!` methods

> See [#collections]. Map methods are registered on the `Map` module. Keys are always `String!`; the value type shows as the type variable `a` (and block result types as `b`). Block params are named `key`/`value`.

\stdlib-methods{Map}

## `JSON` module

> See [#json-yaml]. `JSON`/`YAML`/`TOML` are codec namespaces: `encode` serializes any value to a string, `decode` materializes a string against the expected type (`:: T`). The names double as scalar types (`:: JSON`) owned by Dang, and merge with a same-named schema scalar such as Dagger's `JSON`.

\stdlib-statics{JSON}

## `YAML` module

\stdlib-statics{YAML}

## `TOML` module

> `TOML.encode` requires a table (record) at the top level.

\stdlib-statics{TOML}

## `Random` module

\stdlib-statics{Random}

## `UUID` module

\stdlib-statics{UUID}

## Error types

> See [#errors]. These are prelude types rather than builtins, so they aren't part of the generated lists above.

- `Error` — interface with `message: String!`
- `BasicError` — concrete type behind `raise "msg"`; implements `Error`, has `message: String!`
- `AssertionError` — raised by a failed `assert` (carries the offending expression and sub-values)

> Meta: when generics land properly, update `[T]!` method signatures to show the actual type parameter rather than handwaving `T`/`U`.
