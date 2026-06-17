\use-plugin{dang}

# JSON, YAML, and TOML {#json-yaml}

> Meta: short page. The defining characteristic is type-driven parsing — `JSON.decode` produces values of the *expected* type. Open with that.

Each format is a codec namespace — `JSON`, `YAML`, `TOML` — with matching
`encode` / `decode` static methods. The names double as scalar types (`:: JSON`)
and are owned by Dang, so they work with no schema import; an in-scope scalar of
the same name (e.g. Dagger's `JSON`) merges with the codec rather than colliding
(see [#enums-scalars]).

## Parsing

```dang
let summary: Summary! = JSON.decode("""{"name": "test", "count": 42}""")
let status: Status! = JSON.decode("\"PASSED\"")
let cfg = YAML.decode("name: foo\ncount: 1")
let settings: Settings! = TOML.decode("count = 1")
```

- parsing is type-driven: the expected type guides decoding. The type comes from a `::` cast ([#nullability]), an annotation, or the parameter/return type at the call site (`JSON.decode(...) :: Status!`, `let x: Summary! = JSON.decode(...)`, `f(d: String!): Summary! { JSON.decode(d) }`)
- works for primitives, lists, records, custom types ([#objects]), enums ([#enums-scalars])
- unknown/extra fields in the input are ignored, not errors
- `JSON.decode` rejects trailing data after the first value
- TOML's top level is always a table, so `TOML.decode` materializes into a record/object type
- empty input differs by format: `JSON.decode("")` errors (not valid JSON); `YAML.decode("")` is `null` (an empty YAML document), so it won't materialize into a non-null record; `TOML.decode("")` is an empty table, so an empty TOML config still fills declared defaults

## Serialization

- `JSON.encode(value)` / `YAML.encode(value)` / `TOML.encode(value)` — `String!`; object/record keys are emitted in alphabetical order (see [#literals]). `TOML.encode` requires a table (record) at the top level, and drops null fields (TOML has no null) where JSON/YAML keep them.
- `toString(value)` — pass-through for strings, JSON-encode otherwise (see [#strings])

## Field directives

Fields serialize under their declared names by default. Per-format directives
(see [#directives]) override that, each scoped to one codec so a rename never
leaks into another format:

```dang
type Account {
  pub displayName: String! @JSON.field(name: "display_name") @YAML.field(name: "display-name")
  pub email: String        @JSON.field(name: "email_address", omitNull: true)
  pub secret: String!      @JSON.ignore = "hidden"
  pub balance: Int!
}
```

- `@JSON.field(name: "...")` — the key this field encodes to and decodes from
- `@JSON.field(omitNull: true)` — omit the field from output when its value is null (nullable fields only)
- `@JSON.field(omitEmpty: true)` — omit the field when its value is empty: null, `""`, `0`, `false`, or an empty list/map (this matches Go's `omitempty`; use `omitNull` instead if you want to keep `""`/`0`/`false`)
- `@JSON.ignore` — exclude the field from encoding and decoding entirely
- `@YAML.*` and `@TOML.*` are the same directives for their formats; a field can carry one set per format it customizes (`@JSON.field` and `@YAML.field` on the same field rename it differently per codec)

Both `encode` and `decode` honor them, so a renamed field round-trips. An
`@JSON.ignore` field is never read on decode, so give it a default (or make it
nullable) if it would otherwise be required. The name must be a string literal,
and two fields cannot map to the same key.

## Coercion during parsing

- enum values decode from their string names (`"PASSED"` / `PASSED` → `Status.PASSED`)
- custom scalars decode from their string forms
- record/object fields fall back to declared defaults when absent (`withDefault: String! = "default"`)
- nullable fields absent from input decode to `null`

## Common errors

- invalid input → raises (`JSON.decode: invalid JSON: ...`, `YAML.decode: invalid YAML: ...`, `TOML.decode: invalid TOML: ...`)
- missing required field → raises (`<path>: missing required field`)
- wrong type for field → raises
- invalid enum value → raises (`<path>: invalid enum value "X" for <Enum>`)

All of these are catchable with `try`/`catch` ([#errors]).

> Meta: A side-by-side "JSON in / Dang value out" table would be a nice teaching tool.
