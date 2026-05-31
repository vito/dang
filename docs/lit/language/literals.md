\use-plugin{dang}

# Literals {#literals}

> Meta: one short subsection per literal kind, with one example each. Worth showing the three string flavors side-by-side so the differences are obvious.

## Numbers

- `Int!` — decimal, signed 64-bit
- `Float!` — `3.14`, `1.5e10`, `2.5e-3`
- no hex/octal/binary literals (TBD)

## Booleans

- `true`, `false`

## Null

- `null` literal
- only assignable to nullable type positions

## Strings

### Double-quoted

- `"hello"`, with standard escapes (`\n`, `\t`, `\"`, `\\`, `\xNN`, `\uNNNN`, `\U…`)

### Triple-quoted

- `"""..."""`, multi-line, preserves content, dedents by minimum indent
- canonical form for docstrings

### Backtick templates

- `` `hello #{name}!` `` — single-line, `#{...}` interpolation
- ``` ```...``` ``` — multi-line; fences grow (4+ backticks) to wrap shorter ones
- `##` escapes a literal `#` (TBD — flag if pre/post `${` → `#{` pivot)
- optional language tag for syntax highlighting: ` ```go ... ``` `

## Lists

- `[1, 2, 3]`, `["a", "b"]`, `[]` (empty)
- nested: `[[1, 2], [3]]`
- empty list needs a type hint: `[] :: [String!]!`

## Objects (record literals)

- `{{ key: value, other: value }}`
- always non-null
- field order matters for type but not for equality (TBD — verify)

> Meta: explicitly call out the double-brace syntax — it's the unusual one and the first reaction is "is that a typo?"
