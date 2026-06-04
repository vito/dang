\use-plugin{dang}

# Literals {#literals}

> Meta: one short subsection per literal kind, with one example each. Worth showing the three string flavors side-by-side so the differences are obvious.

## Numbers

- `Int!` — decimal, signed 64-bit
- `Float!` — `3.14`, `1.5e10`, `2.5e-3` (a digit must precede and follow the `.`; exponent `e`/`E` with optional sign)
- no hex/octal/binary literals — decimal only

## Booleans

- `true`, `false`

## Null

- `null` literal
- only assignable to nullable type positions

## String literals

> See [#strings] for the full method reference.

### Double-quoted

- `"hello"`, single-line (no raw newline), with escapes: `\a \b \f \n \r \t \v \" \\`, `\ooo` (octal), `\xNN`, `\uNNNN`, `\UNNNNNNNN`; `\/` is accepted and yields `/`

### Triple-quoted

- `"""..."""`, multi-line, raw content (escape sequences are NOT processed), dedents by minimum indent
- a leading and trailing newline adjacent to the `"""` fences is stripped (so `"""\nhello\n"""` == `"hello"`)
- canonical form for docstrings (see [#syntax])

### Backtick templates

- `` `hello ${name}!` `` — single-line, `${...}` interpolation
- ```` ```...``` ```` — multi-line; same minimum-indent dedent as triple-quoted; fences grow (4+ backticks) to wrap shorter backtick blocks, and the close fence must match the open fence length
- only escape is `\${` → literal `${`; every other backslash is literal (`` `\d+` `` stays `\d+`). A lone `$` not followed by `{` is literal.
- interpolated expressions are auto-stringified like [#strings] `toString` (non-strings JSON-encode; `null` → `"null"`)
- optional language tag (parsed but does not affect the value): ` ```go ... ``` `

## Lists

- `[1, 2, 3]`, `["a", "b"]`, `[]` (empty)
- nested: `[[1, 2], [3]]`
- empty list needs a type hint to pin its element type: `[] :: [String!]!` (also `[] :: [String!]`, `[] :: [String]`). It can still be compared directly: `xs == []`.

## Objects (record literals)

- `{{ key: value, other: value }}`
- always non-null
- the same `{{ ... }}` syntax is also a record *type* annotation: `x :: {{foo: Int!, bar: Status!}}!`
- nestable: `{{user: {{id: 1, active: true}}, count: 100}}`
- serialized to JSON with keys sorted alphabetically, not declaration order (see [#json-yaml]): `{{count: 100, user: ...}}` → `{"count":100,"user":...}`

> Meta: explicitly call out the double-brace syntax — it's the unusual one and the first reaction is "is that a typo?"
