\use-plugin{dang}
\literate-fences

# Syntax and literals {#syntax}

> Meta: this page is the boring foundation. Focus on what would surprise someone coming from JS/Go/Python (semicolons-optional, newline-as-separator, `#` not `//`, no trailing commas in formatted output). Grammar rules live in [#grammar].

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## File layout

- a file is a sequence of forms separated by newlines or `;` (the start rule is `Dang`; see [#grammar])
- whitespace is significant only as separators; indentation is conventional, not syntactic
- declarations are hoisted and order-independent within a file/directory — forward references just work (see [#fields-functions])
- names are public by default; `let` keeps a name private/local (see [#fields])

```dang
let width = 3; let height = 4
area: Int! { width * height }

area
```

## Comments {#comments}

`#` starts a comment that runs to the end of the line; `//` is not a
comment. Comments can sit on their own line or trail code, and the formatter
keeps them next to the code they're attached to:

```dang
# the number of retries before giving up
let retries = 3   # trailing comments are fine too

retries
```

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
- `raise`, `rescue` (`try` and `catch` are no longer reserved — see [#errors] for migrating legacy `try`/`catch`)
- `pub` is optional and being retired — a declaration is public by default; see [#fields]
- `import`, `directive`, `on`
- `true`, `false`, `null`
- `and`, `or`
- `self`
- where only a name can go — a field after `.` or in a `{{ }}` selection, a named argument, an object key — `rescue` is a name like any other: `f(rescue: 1)`, `x.{{rescue}}`, `{{rescue: 1}}`

## Separators and trailing commas

Forms in blocks and at the top level are separated by newlines or `;`.
Inside lists, argument lists, and record literals, newlines and commas are
interchangeable. The formatter strips trailing commas.

```dang
[
  "one"
  "two",
  "three",
]
```

## Literals {#literals}

> Meta: one short subsection per literal kind, with one example each. The three string flavors sit side-by-side so the differences are obvious.

### Numbers

`Int!` literals are decimal, signed 64-bit. A `Float!` literal has a
fraction (a digit on both sides of the `.`), an exponent (`e` or `E`, with an
optional sign), or both:

```dang
[3.14, 1e5, 2.5e-3]
```

There are no hex, octal, or binary literals:

```dang-failure
0x1F
```

### Booleans and null

`true` and `false` are `Boolean!`. `null` fits only a nullable type (see
[#nullability]):

```dang
let ready = true
let nickname: String = null

[ready, nickname == null]
```

### String literals

> See [#strings] for the full method reference.

The delimiter decides how a string's contents are read, and tripling it
makes the string multi-line:

| Delimiter | Single-line | Multi-line, dedented, optional language tag |
|---|---|---|
| `"` | escapes and `${...}` | `"""`: escapes and `${...}` |
| `'` | verbatim | `'''`: verbatim |
| `` ` `` | raw, with `${...}` | ```` ``` ````: raw, with `${...}` |

The rule of thumb: quotes are for text and backticks are for code. Double
quotes interpolate; single quotes don't.

**Double-quoted** strings process escapes:
`\a \b \f \n \r \t \v \" \\ \/ \$`, `\ooo` (octal), `\xNN`, `\uNNNN`,
`\UNNNNNNNN`. Any other backslash is a syntax error. `\/` is just `/`, as in
JSON:

```dang
["tab\there, \x41, \u00e9", "https:\/\/danglang.org"]
```

They interpolate `${...}`. Only `${` starts an interpolation, never a bare
`$name`, and `\$` is a literal dollar sign, so `\${` is a literal `${`:

```dang
let name = "Ada"

"hello, ${name}! that's $5, not \${name}"
```

Interpolated values are turned into text: strings as-is, `null` as
`"null"`, and other values as they print:

```dang
"count: ${3}, missing: ${nickname}, list: ${[1, 2]}"
```

**Single-quoted** strings are verbatim: no escapes and no interpolation, so a
backslash or a `${` is just text. A `'` would end one, so text holding a quote
goes in `'''...'''`, which can hold quotes even on one line (`'''it's'''`), or
in `"..."`. They're the natural fit for shell-ish text that must keep its
`${VAR}`, like `.withEnvVariable("PATH", '${GOBIN}:${PATH}', expand: true)`:

```dang
'${HOME}\bin'
```

**Triple-quoted** strings, `"""..."""` and `'''...'''`, span lines. They
follow the same rules as their single-line forms, and are dedented by their
minimum indent. The newline after the opening quotes and the one before the
closing quotes are dropped (so `"""\nhello\n"""` == `"hello"`):

```dang
"""
  first
    indented: ${name}
  last
"""
```

A `"""` string is dedented before its escapes are decoded, so `\t` at the
start of a line is text, not indentation, and an escape keeps trailing
whitespace that would otherwise be easy to lose:

```dang
"""
  \tone tab
  two spaces:\x20\x20
"""
```

A `'''` string keeps everything as written, which suits shell scripts:

```dang
'''sh
  echo "${HOME}" | tr -d '\n'
'''
```

The opening line of a multi-line string holds at most a language tag, like
`sh` above or `"""sql`. It doesn't change the value; editors use it to
highlight the contents. Any other text there is an error, unless the string
closes on the same line:

```dang-failure
"""Summary.
more
"""
```

```dang
"""inline ${name}"""
```

Docstrings (see Comments above) are triple-quoted too, but they're always
verbatim, with no escapes, interpolation or language tag, and their text may
start on the opening line.

**Backtick templates** are for code. Backticks switch the lexer into template
mode:

- inside backticks, `#` is NOT special — it's a literal character, not a comment
- the only escape recognized is `\${`, which emits a literal `${`; every other backslash is literal (`` `\d+` `` stays `\d+`). A lone `$` not followed by `{` is literal.

```dang
`# ${name} owes $5 \${not interpolated} \d+`
```

- ```` ```...``` ```` — multi-line; same minimum-indent dedent as triple-quoted strings; fences grow (4, 5+ backticks) to wrap shorter backtick blocks, and the close fence must match the open fence length
- optional language tag on the opening line: ` ```go ... ``` `

````dang
```go
  fmt.Println("hi, ${name}")
```
````

> Before Dang 2.5 (a `dang.toml` pinning `dang = "2.4"` or older), `"..."`
> takes `${` literally and `"""..."""` is raw: no escapes, no interpolation and
> no language tag. `dang migrate` moves such code to 2.5 without changing any
> string's value (see [#v2.5]).

### Lists

```dang
[[1, 2], [3]]
```

An empty list takes its element type from context: a typed slot or argument,
the other side of `??` or `+`, the other branch of an `if`, or a later use of
the binding. A hint (`[] :: [String!]!`) is only needed when one untyped
binding has to serve two element types. It can be compared directly:

```dang
let tags = []
tags += ["a"]

[tags == [], [1] == []]
```

### Objects (record literals) {#record-literals}

A record literal is `{{ key: value, other: value }}`, and is always
non-null. A bare name is **shorthand** for `name: name`, taking the value
from a variable in scope. This mirrors object selection, where `recv.{{
name }}` is shorthand for `recv.{{ name: name }}` (see [#interop]):

```dang
let age = 36
let person = {{ name, age }}

person.name
```

- the same `{{ ... }}` syntax is also a record *type* annotation: `x :: {{foo: Int!, bar: Status!}}!`
- nestable: `{{user: {{id: 1, active: true}}, count: 100}}`
- compared by **value** — two records with the same fields and equal values are equal, `{{a: 1}} == {{a: 1}}` (see [#operators]); this is the structural cousin of a named `type`, which compares by type identity
- serialized to JSON with its fields in the order written; `JSON.encode(value, sortKeys: true)` sorts them (see [#json-yaml])

```dang
let point: {{x: Int!, y: Int!}}! = {{y: 2, x: 1}}

[point == {{x: 1, y: 2}}, JSON.encode(point) == """{"y":2,"x":1}"""]
```

Fields may reference each other in any order:

```dang
{{ total: a + b, a: 1, b: 2 }}.total
```

A field's own name refers to the enclosing scope, not the field being
defined, so this `age` reads the variable from above:

```dang
{{ age: age + 1 }}.age
```

A cyclic reference is a compile error:

```dang-failure
{{ x: y, y: x }}
```

- `{{ }}` is **always parallel** — independent fields are evaluated concurrently (a field that references a sibling waits for it) and evaluation fails fast on the first error. One rule for one construct: chained `recv.{{ … }}` selection behaves identically and fans out across a list's elements, so a record of GraphQL selections issues them at once (see [#interop])
- because fields run concurrently, a field can't write anything declared outside it, directly or through a closure; fields communicate by producing values, e.g. `{{ a: c.incr.value, b: c.incr.incr.value }}`, where each method call returns its own updated copy (see [#concurrent-fields])

> Meta: explicitly call out the double-brace syntax — it's the unusual one and the first reaction is "is that a typo?"
