\use-plugin{dang}
\literate-fences

# JSON, YAML, and TOML {#json-yaml}

> Meta: short page. The defining characteristic is type-driven parsing — `JSON.decode` produces values of the *expected* type. Open with that.

Each format is a codec namespace — `JSON`, `YAML`, `TOML` — with matching
`encode` / `decode` static methods. The names double as scalar types (`:: JSON`)
and are owned by Dang, so they work with no schema import; an in-scope scalar of
the same name (e.g. Dagger's `JSON`) merges with the codec rather than colliding
(see [#enums-scalars]).

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## Parsing

Decoding is type-driven: the type you expect is the type you get. Here the
annotation on `summary` tells `JSON.decode` to build a `Summary`:

```dang
type Summary {
  name: String!
  count: Int!
}

let summary: Summary! = JSON.decode("""{"name": "nightly", "count": 42}""")
summary.count
```

The type can come from a `::` cast (see [#nullability]), an annotation, or a
parameter or return type:

```dang
let total = JSON.decode("[1, 2, 3]") :: [Int!]!

parseSummary(text: String!): Summary! { JSON.decode(text) }

[total.length, parseSummary("""{"name": "weekly", "count": 7}""").count]
```

Until it has a type, a decode result has the opaque type `Decoded`. You can
store it, pass it on, or return it, but nothing else:

```dang-failure
JSON.decode("1") + 1
```

```dang-failure
JSON.decode("1") == 1
```

Give it a type first:

```dang
(JSON.decode("1") :: Int!) + 1
```

That includes joining it with typed values. `JSON.decode(text) rescue 0` is
a `Decoded | Int!`, which doesn't fit where an `Int!` is needed:

```dang-failure
let parsed = JSON.decode("oops") rescue 0
parsed + 1
```

Cast inside the `rescue` instead:

```dang
let parsed = (JSON.decode("oops") :: Int!) rescue 0
parsed + 1
```

Decoding works for primitives, lists, maps (`Map[T]`, from an object),
records, your own types ([#objects]), and enums ([#enums-scalars]). Decoded
maps have sorted keys:

```dang
JSON.decode("""{"zed": 1, "amy": 2}""") :: Map[Int!]!
```

Fields the type doesn't declare are ignored:

```dang
let lenient: Summary! = JSON.decode("""{"name": "x", "count": 1, "extra": true}""")
lenient.name
```

YAML anchors, aliases, and `<<` merge keys are resolved before decoding. A
mapping's own keys win over merged ones, and earlier mappings in a `<<: [*a,
*b]` sequence win over later ones:

```dang
type Server {
  host: String!
  port: Int!
}

let servers = YAML.decode("""
defaults: &defaults
  host: localhost
  port: 80
web:
  <<: *defaults
  port: 8080
""") :: Map[Server!]!

servers["web"]!.port
```

`JSON.decode` rejects anything after the first value:

```dang-failure
JSON.decode("1 2") :: Int!
```

TOML's top level is always a table, so `TOML.decode` decodes into a record,
an object, or a `Map[T]`.

Empty input differs by format. `JSON.decode("")` raises, since it isn't
valid JSON. `YAML.decode("")` is `null` (an empty YAML document), so it
won't decode into a non-null record. `TOML.decode("")` is an empty table, so
an empty TOML config still fills in declared defaults:

```dang
type Options {
  retries: Int! = 3
}

(TOML.decode("") :: Options!).retries
```

```dang-failure
YAML.decode("") :: Options!
```

## Serialization

`JSON.encode(value)`, `YAML.encode(value)`, and `TOML.encode(value)` return
a `String!`. Object and record keys come out in alphabetical order (see
[#literals]):

```dang
JSON.encode({{ name: "nightly", count: 42, tags: ["ci"] }})
```

```dang
YAML.encode(summary)
```

`TOML.encode` needs a table (record) at the top level, and drops null
fields, since TOML has no null. JSON and YAML keep them:

```dang
let draft = {{ title: "notes", author: null :: String }}

[JSON.encode(draft), TOML.encode(draft)]
```

```dang-failure
TOML.encode(42)
```

`toString(value)` passes strings through and JSON-encodes everything else
(see [#strings]):

```dang
[toString("plain"), toString([1, 2])]
```

## Field directives

Fields serialize under their declared names by default. Per-format directives
(see [#directives]) override that, each scoped to one codec so a rename never
leaks into another format:

```dang
type Account {
  displayName: String! @JSON.field(name: "display_name") @YAML.field(name: "display-name")
  email: String        @JSON.field(name: "email_address", omitNull: true)
  secret: String!      @JSON.ignore = "hidden"
  balance: Int!
}

let account = Account(displayName: "Ada", balance: 10)

JSON.encode(account)
```

YAML only sees its own rename, so `email` and `secret` are still there:

```dang
YAML.encode(account)
```

- `@JSON.field(name: "...")` — the key this field encodes to and decodes from
- `@JSON.field(omitNull: true)` — omit the field from output when its value is null (nullable fields only)
- `@JSON.field(omitEmpty: true)` — omit the field when its value is empty: null, `""`, `0`, `false`, or an empty list/map (like Go's `omitempty`, applied to the built-in string/number/bool/list/map kinds; use `omitNull` instead if you want to keep `""`/`0`/`false`)
- `@JSON.ignore` — exclude the field from encoding and decoding entirely
- `@YAML.*` and `@TOML.*` are the same directives for their formats; a field can carry one set per format it customizes (`@JSON.field` and `@YAML.field` on the same field rename it differently per codec). The same directive cannot be applied twice for one format.

Both `encode` and `decode` honor them, so a renamed field round-trips:

```dang
let copy = JSON.decode(JSON.encode(account)) :: Account!
copy.displayName
```

An `@JSON.ignore` field is never read on decode, so give it a default (or
make it nullable) if it would otherwise be required. The same goes for
`omitEmpty` on a required field: an empty value is dropped on encode, so
without a default the field cannot be decoded back. The name must be a
string literal, and two fields cannot map to the same key.

## Coercion during parsing

Enum values decode from their names, and custom scalars from their string
forms. A missing field takes its declared default, and a missing nullable
field decodes to `null`:

```dang
enum Outcome { PASSED FAILED }

type Build {
  outcome: Outcome!
  branch: String! = "main"
  notes: String
}

let build = JSON.decode("""{"outcome": "PASSED"}""") :: Build!

[build.branch, build.notes]
```

```dang
build.outcome == Outcome.PASSED
```

## Common errors

Bad input raises an error naming the format, or the path to the field that
went wrong:

```dang-failure
JSON.decode("{") :: Summary!
```

```dang-failure
JSON.decode("""{"name": "x"}""") :: Summary!
```

```dang-failure
JSON.decode("""{"name": "x", "count": "many"}""") :: Summary!
```

```dang-failure
JSON.decode("""{"outcome": "BROKEN"}""") :: Build!
```

All of these are recoverable with `rescue` ([#errors]):

```dang
(JSON.decode("{") :: Summary!) rescue { e: Error => e.message }
```

> Meta: A side-by-side "JSON in / Dang value out" table would be a nice teaching tool.
