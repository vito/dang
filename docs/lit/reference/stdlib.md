\use-plugin{dang}

# Standard library reference {#stdlib}

> Meta: alphabetical reference, not a tutorial. Each entry: signature, one-line description, one tiny example. Group by module/receiver. Cross-link to the conceptual page that introduces the API.

> This page is generated from the builtin registry in `pkg/dang` (`stdlib.go`, `stdlib_random.go`, `stdlib_regexp.go`, `assert.go`) and from the Dang-source prelude (`pkg/dang/prelude/*.dang`), whose members document themselves with a docstring for the description and an `@example` directive holding their runnable example. Each entry's signature, one-line description, and example come straight from the builtin's definition, so the reference can't drift from the implementation — to change an entry, edit the builtin's `.Doc(...)` or `.Example(...)`. Each example is a live REPL: press **Run** to evaluate it (and then keep typing — state carries across entries, just like the CLI).

\table-of-contents

## Top-level functions

\stdlib-functions

> `print` and `assert` return `null` — there is no `Void` type; treat the result as `null`. The `JSON`/`YAML`/`TOML` scalars and their `encode`/`decode` are covered in depth on [#json-yaml].

## `String!` methods

> See [#strings]. `.length` counts characters (Unicode code points), not bytes.

> Regex methods take a `Regexp!` pattern. Backtick template strings auto-coerce to the `Regexp` scalar, so a pattern is usually written as `` `\d+` `` (Go `regexp/syntax`).

\stdlib-methods{String}

## `Path!` methods

> `Path` is a slash-separated path scalar, **normalized on construction** — `Path("a//b/../c")` *is* `Path("a/c")`, so there is no `clean`. Construct with the `Path(...)` function (any String), a string literal in a `Path!`-typed slot, or `:: Path!`. A `Path` value **degrades back to `String!`** at value-handoff boundaries (call args, typed slots, returns), so it can be passed anywhere a plain path string is expected. Path is defined in Dang itself — the interpreter's embedded prelude — as a `scalar` with a body of methods and a `new()` normalization hook.

\stdlib-methods{Path}

## `URL!` methods

> `URL(raw: String!)` constructs a URL reference, preserving its original text rather than normalizing it: `URL("https://foo:1234/bar") == "https://foo:1234/bar"`. String literals in `URL!`-typed slots and explicit `:: URL!` casts validate through the same hook. URL values degrade to `String!` at value-handoff boundaries, like `Path`.

> Absolute, relative, empty, and opaque references are accepted. This is not a check that a URL is absolute or safe to fetch. A schema's same-named scalar remains a separate type (use `MyAPI.URL`), while unqualified `URL` refers to the builtin unless shadowed by a local declaration.

### Reading components

Optional components use null rather than sentinel values: `.port` is an `Int`, and `.scheme`, `.host`, `.hostname`, and `.fragment` are nullable strings. No default port is inferred. The path is a non-null string, not a filesystem `Path`: repeated/trailing slashes matter, and `.escapedPath` preserves the distinction between an encoded slash and a separator.

`.query` is a **non-null `Map[[String!]!]!`**: missing queries yield an empty map, and repeated parameters retain every value in order. Form decoding treats `+` as a space, `%2B` as a plus, and bare `flag` as an empty value. Semicolons are ordinary data. Invalid escapes raise rather than dropping parameters. `.rawQuery` exposes uninterpreted query text; malformed query escapes do not prevent constructing a URL, but reading `.query` or editing its parameters raises.

Use `.rawQuery` to distinguish no query (`null`) from a trailing `?` (`""`). Likewise `.fragment` distinguishes no fragment (`null`) from a trailing `#` (`""`). `.isAbsolute` means a scheme is present, not that there is necessarily a host or an HTTP endpoint. Explicit ports must fit an `Int` when read, without imposing a network-port ceiling of 65535.

### Building URLs

Build from a base URL or start from `URL("")` and add components. Helpers return a new URL, leave the receiver unchanged, and accept **decoded data**, escaping delimiters for the appropriate component:

```dang
let endpoint = URL("")
  .withScheme("https")
  .withHostname("api.example.com")
  .withPort(8443)
  .appendPath(["users", "name/with/slashes"])
  .withQuery(["tag": ["a", "b"]])
  .withAddedQueryParam("tag", "c")
  .withFragment("details")
assert { endpoint.escapedPath == "/users/name%2Fwith%2Fslashes" }
assert { endpoint.query["tag"] == ["a", "b", "c"] }
```

`.withQueryParam` replaces a key's values; `.withAddedQueryParam` appends; `.withoutQueryParam` removes them. `.withQuery(null)` removes the query, while `.withQuery([:])` emits a trailing `?`. Empty value lists emit no pairs. `.withFragment(null)` removes the fragment; an empty string emits a trailing `#`. `.withHostname(null)` clears the entire authority, including credentials and port. Query edits may regroup keys and normalize escaping; use the original `.string` when exact spelling matters.

`.withPath` replaces a decoded path without cleaning it; with an authority, a nonempty path must start with `/`. `.appendPath` treats every supplied segment as data, escaping embedded slashes and literal `.`/`..` segments. By contrast, `.resolve("../other")` applies URL reference rules, including dot-segment navigation and replacement by absolute references. With a rootless relative base such as `docs/page`, relative resolution stays rootless and retains excess leading `..` segments, so the result can subsequently be resolved against an origin. Resolution is **not** a same-origin or containment check for untrusted input.

Invalid schemes/hostnames and negative ports raise. Path/authority edits on opaque URLs raise rather than discarding their payload. Adding a scheme or hostname to an existing nonempty relative path also raises: root the path first with `.withPath("/...")`, or rebuild from `URL("")` with scheme and hostname before appending segments. This prevents the path from silently becoming opaque or gaining a leading slash.

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
