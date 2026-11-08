\use-plugin{dang}
\literate-fences

# JSON, YAML, and TOML {#json-yaml}

> Meta: short page. The defining characteristic is type-driven parsing — `JSON.decode` produces values of the *expected* type. Open with that.

Each format is a codec namespace — `JSON`, `YAML`, `TOML` — with matching
`encode` / `decode` static methods. The names double as scalar types (`:: JSON`)
and are owned by Dang, so they work with no schema import. A schema's scalar of
the same name (e.g. Dagger's `JSON`) gets the codec's methods grafted on rather
than colliding, while unqualified `JSON` stays Dang's own (see
[#enums-scalars]).

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

### Strict decoding

Pass `strict: true` to catch typos and stale keys instead. A key that no field
of the expected type reads is an error, naming the object's path and the key.
Every such key in the whole value is reported at once, one per line:

```dang-failure
JSON.decode("""
{
  "nightly": {"name": "x", "count": 1, "extra": true},
  "weekly": {"name": "y", "cuont": 2, "count": 3}
}
""", strict: true) :: Map[Summary!]!
```

The check applies at every level of a type or record, under each field's
`@JSON.field(name:)` key; an `@JSON.ignore` field reads no key, so its name is
unknown too. Every field the type declares counts, including ones its
constructor doesn't take (a `let` field), so a value's own encoding always
decodes back. A `Map[T]`'s keys are data, so it takes any key:

```dang
let counts = JSON.decode("""{"zed": 1, "amy": 2}""", strict: true) :: Map[Int!]!
counts["amy"]
```

Strict decoding also rejects a key repeated within one JSON object or YAML
mapping, where a plain decode keeps the last one. TOML rejects repeated keys
either way:

```dang-failure
YAML.decode("""
nightly:
  name: x
  count: 1
  name: y
""", strict: true) :: Map[Summary!]!
```

The two errors locate a problem differently. A repeated key is found while
parsing, before any type is involved, so its path is the document's (`nightly`)
and it carries a line number. An unknown key is found while building the
expected type, so its path is the typed path used by every other decode error,
where a map entry reads `["nightly"]`.

YAML anchors, aliases, and `<<` merge keys are resolved before decoding. A
mapping's own keys win over merged ones, and earlier mappings in a `<<: [*a,
*b]` sequence win over later ones (even with `strict: true`, overriding a
merged key isn't a repeat):

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

## Data of unknown shape {#json-values}

`JSON` is also a type for data whose shape you don't know: decoding into
`JSON` takes any value and keeps it as its JSON text. A `Map[JSON!]!` reads
an object without saying anything about its values, so you can check its
keys:

```dang
let config = YAML.decode("""
modules:
  - name: lint
    source: ./lint
    pin: 3
version: 2
""") :: Map[JSON!]!

config.keys
```

A JSON value reads as its text wherever a `String` is expected. A string
value keeps its quotes, so the text is always JSON:

```dang
[toString(config["version"]!), `modules: ${config["modules"]!}`]
```

`JSON.decode` takes a JSON value too, so going a level deeper is decoding
again, at the type the data has there:

```dang
let modules = JSON.decode(config["modules"]!) :: [Map[JSON!]!]!

modules[0]!.keys.filter { key => !["name", "source"].contains(key) }
```

```dang
JSON.decode(modules[0]!["name"]!) :: String!
```

Encoding puts a JSON value back as the data it holds, not as a string, so
it round-trips:

```dang
JSON.encode({{ first: modules[0]! }})
```

A `String` turns into a JSON value only if it is JSON. It is compacted on
the way in, keeping its keys in the order written. JSON values compare as
data, so key order and spacing don't affect equality:

```dang
let written = """{ "name": "lint", "pin": 3 }"""

toString(written :: JSON!)
```

```dang
(written :: JSON!) == """{"pin": 3, "name": "lint"}"""
```

```dang-failure
"{name: lint}" :: JSON!
```

## Serialization

`JSON.encode(value)`, `YAML.encode(value)`, and `TOML.encode(value)` return
a `String!`. `JSON.encode` keeps fields in the order they are declared, as
Go does for struct fields: a record's as written (see [#literals]), a type's
as the type declares them. YAML and TOML sort them alphabetically:

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

`JSON.encode` writes compact JSON and escapes `<`, `>`, and `&` (as
`\u003c`, `\u003e`, `\u0026`) so the output is safe to embed in HTML, like
Go's `json.Marshal`. Pass `indent` to pretty-print with one copy of it per
nesting level, and `escapeHTML: false` to keep those characters as they are:

```dang
let link = {{ label: "Tom & Jerry", tags: ["<b>"] }}

[JSON.encode(link), JSON.encode(link, escapeHTML: false)]
```

```dang
print(JSON.encode(link, indent: "  ", escapeHTML: false))
```

More precisely, a record's fields follow how it was built: a literal's as
written (even when it is annotated with a record type listing them
differently), a selection's as selected, and a decoded record's as its type
lists them. A map's entries keep their insertion order. To sort the keys of
every object instead, maps included, pass `sortKeys: true`:

```dang
type Release {
  version: String!
  name: String!
  notes: String @JSON.field(omitNull: true)
}

let release = Release(version: "2.2", name: "dang")

[JSON.encode(release), JSON.encode(release, sortKeys: true)]
```

```dang
JSON.encode(["zed": 1, "amy": 2], sortKeys: true)
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
forms (except `JSON`, which takes any value; see [#json-values]). A missing
field takes its declared default, and a missing nullable field decodes to
`null`:

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
