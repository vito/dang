\use-plugin{dang}

# Syntax and literals {#syntax}

> Meta: this page is the boring foundation. Focus on what would surprise someone coming from JS/Go/Python (semicolons-optional, newline-as-separator, `#` not `//`, no trailing commas in formatted output). Grammar rules live in [#grammar].

## File layout

- a file is a sequence of forms separated by newlines or commas
- whitespace is significant only as separators; indentation is conventional, not syntactic

## Comments

- `#` to end of line
- placed on their own line or trailing; both are idiomatic
- formatter keeps them attached to the following or preceding code

> Meta: `///` doc comments don't exist — docstrings are real triple-quoted strings attached to declarations (covered in [#fields] and [#functions]).

- a docstring is a triple-quoted string placed immediately before a declaration (module-level, field, function, or `let`)

## Identifiers

- lowercase: `foo_bar`, `userName` — values and methods
- capitalized: `User`, `String` — types
- single lowercase letters in type positions (`a`, `b`) — type variables
- `_` is a normal identifier character, not a special "ignore"/discard pattern

## Reserved words

- `pub`, `let`, `type`, `interface`, `enum`, `union`, `scalar`, `new`, `implements`
- `if`, `else`, `case`, `break`, `continue`, `return`
- `try`, `catch`, `raise`
- `pub` is optional and being retired — a declaration is public by default; see [#fields]
- `import`, `directive`, `on`
- `true`, `false`, `null`
- `and`, `or`
- `self`

## Separators and trailing commas

- newlines and commas are interchangeable inside lists, arg lists, object literals
- formatter strips trailing commas

## Literals {#literals}

> Meta: one short subsection per literal kind, with one example each. The three string flavors sit side-by-side so the differences are obvious.

### Numbers

- `Int!` — decimal, signed 64-bit
- `Float!` — `3.14`, `1.5e10`, `2.5e-3` (a digit must precede and follow the `.`; exponent `e`/`E` with optional sign)
- no hex/octal/binary literals — decimal only

### Booleans and null

- `true`, `false`
- `null` literal — only assignable to nullable type positions

### String literals

> See [#strings] for the full method reference.

The three flavors, side by side:

- **Double-quoted** — `"hello"`: single-line (no raw newline), with escapes: `\a \b \f \n \r \t \v \" \\`, `\ooo` (octal), `\xNN`, `\uNNNN`, `\UNNNNNNNN`; `\/` is accepted and yields `/`
- **Triple-quoted** — `"""..."""`: multi-line, raw content (escape sequences are NOT processed), dedents by minimum indent; a leading and trailing newline adjacent to the `"""` fences is stripped (so `"""\nhello\n"""` == `"hello"`); the canonical form for docstrings (see Comments above)
- **Backtick templates** — `` `hello ${name}!` ``: single-line, with `${...}` interpolation

Backtick templates in detail — backticks switch the lexer into template mode:

- inside backticks, `#` is NOT special — it's a literal character, not a comment
- the only escape recognized is `\${`, which emits a literal `${`; every other backslash is literal (`` `\d+` `` stays `\d+`). A lone `$` not followed by `{` is literal.
- interpolated expressions are auto-stringified like [#strings] `toString` (non-strings JSON-encode; `null` → `"null"`)
- ```` ```...``` ```` — multi-line; same minimum-indent dedent as triple-quoted; fences grow (4, 5+ backticks) to wrap shorter backtick blocks, and the close fence must match the open fence length
- optional language tag (parsed but does not affect the value): ` ```go ... ``` `

### Lists

- `[1, 2, 3]`, `["a", "b"]`, `[]` (empty)
- nested: `[[1, 2], [3]]`
- empty list needs a type hint to pin its element type: `[] :: [String!]!` (also `[] :: [String!]`, `[] :: [String]`). It can still be compared directly: `xs == []`.

### Objects (record literals)

- `{{ key: value, other: value }}`
- always non-null
- the same `{{ ... }}` syntax is also a record *type* annotation: `x :: {{foo: Int!, bar: Status!}}!`
- nestable: `{{user: {{id: 1, active: true}}, count: 100}}`
- serialized to JSON with keys sorted alphabetically, not declaration order (see [#json-yaml]): `{{count: 100, user: ...}}` → `{"count":100,"user":...}`

> Meta: explicitly call out the double-brace syntax — it's the unusual one and the first reaction is "is that a typo?"
