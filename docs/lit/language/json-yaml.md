\use-plugin{dang}

# JSON and YAML {#json-yaml}

> Meta: short page. The defining characteristic is type-driven parsing — `fromJSON` produces values of the *expected* type. Open with that.

## Parsing

```dang
let summary: Summary! = fromJSON("""{"name": "test", "count": 42}""")
let status: Status! = fromJSON("\"PASSED\"")
let cfg = fromYAML("name: foo\ncount: 1")
```

- parsing is type-driven: the expected type guides decoding
- works for primitives, lists, records, custom types, enums

## Serialization

- `toJSON(value)` — `String!`
- `toString(value)` — pass-through for strings, JSON-encode otherwise

## Coercion during parsing

- enum values decode from string names
- custom scalars decode from string forms
- missing required fields raise (catchable)

## Common errors

- invalid JSON / YAML → raises
- missing required field → raises
- wrong type for field → raises

> Meta: cross-link [errors](./errors.md) for how to recover. A side-by-side "JSON in / Dang value out" table would be a nice teaching tool.
