\use-plugin{dang}

# JSON and YAML {#json-yaml}

> Meta: short page. The defining characteristic is type-driven parsing — `fromJSON` produces values of the *expected* type. Open with that.

## Parsing

```dang
let summary: Summary! = fromJSON("""{"name": "test", "count": 42}""")
let status: Status! = fromJSON("\"PASSED\"")
let cfg = fromYAML("name: foo\ncount: 1")
```

- parsing is type-driven: the expected type guides decoding. The type comes from a `::` cast ([#nullability]), an annotation, or the parameter/return type at the call site (`fromJSON(...) :: Status!`, `let x: Summary! = fromJSON(...)`, `f(d: String!): Summary! { fromJSON(d) }`)
- works for primitives, lists, records, custom types ([#objects]), enums ([#enums-scalars])
- unknown/extra fields in the input are ignored, not errors
- `fromJSON` rejects trailing data after the first value

## Serialization

- `toJSON(value)` — `String!`; object/record keys are emitted in alphabetical order (see [#literals])
- `toString(value)` — pass-through for strings, JSON-encode otherwise (see [#strings])

## Coercion during parsing

- enum values decode from their string names (`"PASSED"` / `PASSED` → `Status.PASSED`)
- custom scalars decode from their string forms
- record/object fields fall back to declared defaults when absent (`withDefault: String! = "default"`)
- nullable fields absent from input decode to `null`

## Common errors

- invalid JSON / YAML → raises (`invalid JSON: ...` / `invalid YAML: ...`)
- missing required field → raises (`<path>: missing required field`)
- wrong type for field → raises
- invalid enum value → raises (`<path>: invalid enum value "X" for <Enum>`)

All of these are catchable with `try`/`catch` ([#errors]).

> Meta: A side-by-side "JSON in / Dang value out" table would be a nice teaching tool.
