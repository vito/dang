# Standard Library: Strings, Collections, JSON/YAML, Builtins

The built-in surface of Dang itself (everything here is available without any
`import`). Most functionality lives as **methods** on values — `"hi".toUpper`,
`users.length`, `"a,b".split(",")` — rather than global functions.

## Top-level functions
- `assert { Boolean! } -> Null` — runs the block; raises an `AssertionError` if not truthy. Block, not parens: `assert { x == 1 }`. The failure message includes the source expression and sub-values.
- `assert(message: String! = null) { Boolean! } -> Null` — optional named `message`.
- `loop { ... } -> r` — calls the block repeatedly forever; exit via `break` (the loop yields the break value, non-null if the break value is), `return`, or `raise`. Equivalent to `for { ... }` but as a plain builtin; see control-flow.md.
- `print(value: a) -> Null` — write a value to stdout (newline-terminated).
- `toString(value: a) -> String!` — pass strings through, JSON-encode everything else.
- `toJSON(value: a) -> String!` — JSON-encode anything.
- `fromJSON(data: String!) -> a` — parse JSON into a value materialized by the expected type.
- `fromYAML(data: String!) -> a` — parse YAML into a value materialized by the expected type.

`print` and `assert` return `null` — there is no `Void` type.

## `String!` methods

Strings have **no** `.length` / `.isEmpty` — those are list-only. Use the predicates below.

- `.toUpper -> String!`, `.toLower -> String!`
- `.contains(substring: String!) -> Boolean!`
- `.hasPrefix(prefix: String!) -> Boolean!`, `.hasSuffix(suffix: String!) -> Boolean!`
- `.trim(cutset: String!)`, `.trimLeft(cutset)`, `.trimRight(cutset)`, `.trimSpace`
- `.trimPrefix(prefix)`, `.trimSuffix(suffix)`
- `.padLeft(width: Int!)`, `.padRight(width)`, `.center(width)` — space-padded; no-op if already ≥ width
- `.split(separator: String!, limit: Int = 0) -> [String!]!` — empty separator splits into characters; `limit` caps parts (last keeps remainder)
- `.replace(old: String!, new: String!, count: Int = -1) -> String!` — `count = -1` replaces all; empty `old` inserts between characters

Conversion: `toString(value)` (JSON-encodes non-strings) or `value :: String!` (explicit cast where types align).

### `String!` regex methods
Backtick templates auto-coerce to the `Regexp` scalar, so a pattern is usually `` `\d+` ``. Go `regexp/syntax` (RE2); named groups use `(?P<name>...)`.

- `.containsMatch(pattern: Regexp!) -> Boolean!`
- `.match(pattern: Regexp!) -> Match` — first match, or null
- `.matchAll(pattern: Regexp!) -> [Match!]!`
- `.replaceMatches(pattern: Regexp!, with: String!, count: Int = -1) -> String!` — `$0`/`$1`/`$name`/`${name}` backref expansion
- `.rewriteMatches(pattern: Regexp!, count: Int = -1) { match => String! } -> String!`
- `.splitMatches(pattern: Regexp!, limit: Int = 0) -> [String!]!`

```dang
"call 555-1212".containsMatch(`\d+`)
"a1 b22".matchAll(`\d+`)
"555-1212".replaceMatches(`(?P<area>\d{3})-(?P<num>\d{4})`, with: "$area.$num")   # "555.1212"
"hello world".rewriteMatches(`\w+`) { m => m.string.toUpper }                     # "HELLO WORLD"
```

### `Match` object
- `.string -> String!` — whole matched substring
- `.start -> Int!`, `.end -> Int!` — byte offsets
- `.captures -> [String!]!` — positional groups (`captures[0]` is `$1`); unmatched optional groups surface as `""`
- `.named -> Map[String]!` — named groups by name (`m.named["area"]`); a key reads as null if that group didn't match, and is absent for an unknown name

## `[T]!` methods (lists)

Lists are the **only collection type today** (no maps/sets). Block params shown as `x`/`i`.

### Construction / access
- literal `[1, 2, 3]`; empty needs a type hint `[] :: [Int!]!` or annotation `let xs: [Int!]! = []`
- concatenation `[1, 2] + [3, 4]`
- `xs[0]` — element access; **out-of-bounds yields `null`** (result is `T`, not `T!`); chained `matrix[0][1]`
- `.length -> Int!`, `.isEmpty -> Boolean!`

### Transform / select / aggregate
- `.map { x, i => ... } -> [U]!`
- `.filter { x => Boolean! } -> [T]!`, `.reject { x => Boolean! } -> [T]!`
- `.reduce(initial: U) { acc, x => ... } -> U` — `initial` positional or named `initial:`
- `.uniq -> [T]!` — drop duplicates, keep first-occurrence order; uses Dang equality (works on nested lists)
- `.each { x, i => ... } -> [T]!` — returns the original list (for chaining / side effects)
- `.any { x => Boolean! } -> Boolean!`, `.all { x => Boolean! } -> Boolean!`
- `.contains(element: T) -> Boolean!`

### Slice
- `.takeFirst(count: Int = 1)`, `.takeLast(count: Int = 1)`, `.dropFirst(count: Int = 1)`, `.dropLast(count: Int = 1)`
- `.takeWhile { x => Boolean! }`, `.dropWhile { x => Boolean! }`

### Join
- `.join(separator: String!) -> String!` — works on any list; non-string elements are JSON-encoded

### Nullable lists vs. lists of nullables
- `[T]` — list might be null; `[T]!` — non-null list, elements might be null; `[T!]!` — both non-null.
- List methods work on `[T]` (nullable) receivers; null propagates: `nullList.map { ... } == null`, `nullList.length == null`.

### Heterogeneous element inference
- `[Cat, Dog]` where both implement `Animal` → `[Animal!]` (common supertype/interface; first common one found).
- Mixing `null` widens elements to nullable.

## JSON and YAML

Parsing is **type-driven** — `fromJSON`/`fromYAML` produce values of the *expected* type, which comes from a `::` cast, an annotation, or the parameter/return type at the call site:
```dang
let summary: Summary! = fromJSON("""{"name": "test", "count": 42}""")
let status: Status! = fromJSON("\"PASSED\"")
let s = fromJSON(...) :: Status!
f(d: String!): Summary! { fromJSON(d) }
```
- Works for primitives, lists, records, custom types, enums.
- Unknown/extra fields in the input are ignored, not errors. `fromJSON` rejects trailing data after the first value.

### Coercion during parsing
- Enum values decode from their string names (`"PASSED"` → `Status.PASSED`).
- Custom scalars decode from their string forms.
- Record/object fields fall back to declared defaults when absent; nullable fields absent from input decode to `null`.

### Serialization
- `toJSON(value) -> String!` — object/record keys emitted in **alphabetical** order.
- `toString(value)` — pass-through for strings, JSON-encode otherwise.

### Errors (all catchable with `try`/`catch`)
- invalid JSON/YAML → `invalid JSON: ...` / `invalid YAML: ...`
- missing required field → `<path>: missing required field`
- wrong type for field → raises
- invalid enum value → `<path>: invalid enum value "X" for <Enum>`

## `Random` module
- `Random.int(min: Int!, max: Int!) -> Int!` — `min` inclusive, `max` exclusive (errors if `min >= max`)
- `Random.float -> Float!` — `[0.0, 1.0)`
- `Random.string -> String!` — cryptographically random base32, ≥128 bits entropy

## `UUID` module
- `UUID.v4 -> String!` — random UUID v4
- `UUID.v7 -> String!` — time-ordered UUID v7

## Error types
- `Error` — interface with `message: String!`
- `BasicError` — concrete type behind `raise "msg"`; implements `Error`
- `AssertionError` — raised by a failed `assert` (carries the offending expression and sub-values)
