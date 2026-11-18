\use-plugin{dang}

# Standard library reference {#stdlib}

> Meta: alphabetical reference, not a tutorial. Each entry: signature, one-line description, one tiny example. Group by module/receiver. Cross-link to the conceptual page that introduces the API.

> This page is generated from the builtin registry in `pkg/dang` (`stdlib.go`, `stdlib_random.go`, `stdlib_regexp.go`, `assert.go`) and from the Dang-source prelude (`pkg/dang/prelude/*.dang`), whose members document themselves with a docstring for the description and an `@example` directive holding their runnable example. Each entry's signature, one-line description, and example come straight from the builtin's definition, so the reference can't drift from the implementation — to change an entry, edit the builtin's `.Doc(...)` or `.Example(...)`. Each example is a live REPL: press **Run** to evaluate it (and then keep typing — state carries across entries, just like the CLI).

\table-of-contents

## Top-level functions

\stdlib-functions

> `print` and `assert` return `null` — there is no `Void` type; treat the result as `null`. The `JSON`/`YAML`/`TOML` scalars and their `encode`/`decode` are covered in depth on [#json-yaml].

## `String!` methods

> See [#strings]. `.length` counts characters (Unicode code points), not bytes. `.bytes` returns raw bytes as integers from 0 to 255 (UTF-8 for valid text); `String.fromBytes(bytes: [Int!]!): String!` reverses it. Both preserve invalid UTF-8 exactly, like `.fromBase64`; `String.fromBytes` raises for integers outside 0–255. For example, `"é".bytes == [195, 169]` and `String.fromBytes([195, 169]) == "é"`.

> Regex methods take a `Regexp!` pattern. Backtick template strings auto-coerce to the `Regexp` scalar, so a pattern is usually written as `` `\d+` `` (Go `regexp/syntax`).

\stdlib-methods{String}

\stdlib-statics{String}

## `Path!` methods

> `Path` is a slash-separated path scalar, **normalized on construction** — `Path("a//b/../c")` *is* `Path("a/c")`, so there is no `clean`. Construct with the `Path(...)` function (any String), a string literal in a `Path!`-typed slot, or `:: Path!`. A `Path` value **degrades back to `String!`** at value-handoff boundaries (call args, typed slots, returns), so it can be passed anywhere a plain path string is expected. Path is defined in Dang itself — the interpreter's embedded prelude — as a `scalar` with a body of methods and a `new()` normalization hook.

\stdlib-methods{Path}

## `URL!` methods

`URL(raw: String!)` accepts absolute URLs and relative references, preserving
the original text. `.path` and `.fragment` expose decoded text; `.escapedPath`
and `.rawQuery` retain escaping. `.query` groups repeated parameters into lists
and returns an empty map when there is no query.

URL paths are strings, not filesystem paths: repeated and trailing slashes can
matter. An existing `Path` works with `.withPath`, and its `.parts` work with
`.appendPath`. Use `Path(url.path)` when you explicitly want path normalization.

### Building URLs

\literate-fences

Start from a base URL or build one from components. Helpers return a new URL
and escape **decoded data** for you. `.appendPath` adds segments as data;
`.resolve` follows references such as `../other` instead.

```dang
let endpoint = URL("")
  .withScheme("https")
  .withHostname("api.example.com")
  .withPort(8443)
  .withPath(Path("/api"))
  .appendPath(["users", "name/with/slashes"])
  .withQuery(["tag": ["a", "b"]])
  .withAddedQueryParam("tag", "c")
  .withFragment("details")
endpoint.string
```

### Methods

\stdlib-methods{URL}

## `[T]!` methods

> See [#collections]. List methods are registered on the `List` module, so signatures show the element type as the type variable `a` (and block result types as `b`). Block params are named `item`/`index` — and `acc` for `.reduce`.

\stdlib-methods{List}

## `Map[a]!` methods

> See [#collections]. Map methods are registered on the `Map` module. Keys are always `String!`; the value type shows as the type variable `a` (and block result types as `b`). Block params are named `key`/`value`.

\stdlib-methods{Map}

## `JSON`, `YAML` and `TOML`

> See [#json-yaml]. `JSON`/`YAML`/`TOML` are Dang's scalars of text in their format, validated on construction (`"[1]" :: JSON!`); as values they hold their statics: `encode` serializes any value to a string, `decode` materializes a string against the expected type (`:: T`). A same-named schema scalar such as Dagger's `JSON` is a separate type (`Dagger.JSON`), while unqualified `JSON` stays the builtin. Data of any shape decodes into `Decoded` (see [#decoded-values]).

### `JSON`

\stdlib-statics{JSON}

### `YAML`

> A `YAML!` value may hold several documents; `YAML.decode` reads a single one.

\stdlib-statics{YAML}

### `TOML`

> `TOML.encode` requires a table (record) at the top level.

\stdlib-statics{TOML}

## `Random` module

\stdlib-statics{Random}

## `UUID` module

\stdlib-statics{UUID}

## Error types

> See [#errors]. These are prelude types rather than builtins, so they aren't part of the generated lists above.

- `Error` — interface with `message: String!`
- `BasicError` — concrete type behind `raise "msg"`, and nothing else; implements `Error`
- `AssertionError` — a failed `assert` block (`message` carries the offending expression and sub-values)
- `RuntimeError` — interpreter faults: division by zero, failed non-null assertions/casts, invalid enum values
- `GraphQLError` — a GraphQL response reporting errors; adds `path: [String!]!` (failing field in the request) and `extensions: String!` (extensions object as JSON text, `"{}"` when absent)

> Meta: when generics land properly, update `[T]!` method signatures to show the actual type parameter rather than handwaving `T`/`U`.
