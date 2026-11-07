# Standard Library: Strings, Collections, JSON/YAML, Builtins

The built-in surface of Dang itself (everything here is available without any
`import`). Most functionality lives as **methods** on values — `"hi".toUpper`,
`users.length`, `"a,b".split(",")` — rather than global functions.

## Top-level functions
- `assert { Boolean! } -> Null` — runs the block; raises an `AssertionError` if not truthy. Block, not parens: `assert { x == 1 }`. The failure message includes the source expression and sub-values: method receivers and arguments, comparison operands, and each operand of `and`/`or`/`!`, with the operands that decided the result broken down further (operands skipped by short-circuiting show as `(not evaluated)`). Those values come from re-evaluating the sub-expressions after the failure (an operand and then its receiver may each run again), so keep side effects out of assert blocks; anything that uses a name bound earlier in the block, or fails when re-evaluated, shows as `(unavailable)`.
- `assert(message: String! = null) { Boolean! } -> Null` — optional named `message`.
- `loop { ... } -> r` — Dang's only loop: calls the block repeatedly forever; exit via `break` (the loop yields the break value, non-null if the break value is), `return`, or `raise`. See control-flow.md.
- `Path(path: String!) -> Path!` — constructs a normalized Path from any String expression (see the [`Path!` scalar](#path-scalar) below).
- `Regexp(pattern: String!) -> Regexp!` — compiles a pattern from any String expression; raises `invalid regexp "...": ...` when it doesn't compile (see `String!` regex methods below).
- `print(value: a) -> Null` — write a value to stdout (newline-terminated).
- `toString(value: a) -> String!` — pass strings, enum values, and custom scalar values (Path, Regexp, URL, ...) through as their bare string; JSON-encode everything else.

`print` and `assert` return `null` — there is no `Void` type.

JSON/YAML/TOML conversions are **not** top-level functions — they live in the `JSON`/`YAML`/`TOML` codec namespaces (`.encode` / `.decode`; see [JSON, YAML, and TOML](#json-yaml-and-toml) below). base64 lives on `String!` (`.toBase64` / `.fromBase64`).

## `String!` methods

- `.length -> Int!` — number of characters (Unicode code points, not bytes): `"héllo".length == 5`; `.isEmpty -> Boolean!`
- `.toUpper -> String!`, `.toLower -> String!`
- `.toBase64 -> String!` — standard padded base64 of the string's bytes; `.fromBase64 -> String!` — decode standard padded base64 (raises on invalid input)
- `.contains(substring: String!) -> Boolean!`
- `.hasPrefix(prefix: String!) -> Boolean!`, `.hasSuffix(suffix: String!) -> Boolean!`
- `.trim(cutset: String!)`, `.trimLeft(cutset)`, `.trimRight(cutset)`, `.trimSpace`
- `.trimPrefix(prefix)`, `.trimSuffix(suffix)`
- `.padLeft(width: Int!)`, `.padRight(width)`, `.center(width)` — space-padded to `width` characters (Unicode code points, like `.length`); no-op if already ≥ width; `center` puts an odd extra space on the right
- `.split(separator: String!, limit: Int = 0) -> [String!]!` — empty separator splits into characters; `limit` caps parts (last keeps remainder)
- `.replace(old: String!, new: String!, count: Int = -1) -> String!` — `count = -1` replaces all; empty `old` inserts between characters
- `.truncate(length: Int!) -> String!` — at most `length` characters (code points, like `.length`); unchanged if already shorter; `length <= 0` yields `""`: `"hello world".truncate(5) == "hello"`
- `.toInt(base: Int = 10) -> Int!` — parse an integer in base 2–36 (letters either case), optional leading `+`/`-`; raises on anything else (no whitespace, `0x` prefix, or `_`) and on overflow: `"ff".toInt(base: 16) == 255`, `("x".toInt rescue 0)`. Prefer it to a hand-written digit table

Conversion: `toString(value)` (JSON-encodes non-strings) or `value :: String!` (explicit cast where types align). A `::` cast never parses (`"42" :: Int!` is a type error); use `.toInt`.

### `String!` regex methods
Backtick templates auto-coerce to the `Regexp` scalar, so a pattern is usually `` `\d+` ``. Go `regexp/syntax` (RE2); named groups use `(?P<name>...)`.

The coercion is the literal rule (types.md): it happens where a template meets a `Regexp!` slot. A template **bound without a type** is a `String!`, so `` let re = `\d+`; s.containsMatch(re) `` is `cannot use String! as Regexp!` — annotate the binding (`` let re: Regexp! = `\d+` ``) or build it with `Regexp(...)`. `Regexp.escape(s: String!) -> String!` quotes metacharacters so arbitrary text matches literally: `Regexp(Regexp.escape(userInput))`.

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

## `Path!` scalar and methods {#path-scalar}

`Path` is a slash-separated path **scalar, normalized on construction**: every
way a Path comes to exist runs `path.Clean`-style normalization, so an
un-clean Path cannot exist (there is no `.clean`) and equality is semantic —
`Path("a//b/./c") == Path("a/b/c")`, and `Path("a//b") == "a/b"` (a string
literal converts to the Path, normalized). Path is
defined in Dang itself, using the scalar-body syntax — see types.md.

Ways in: the `Path(s)` constructor (any String expression); a string
*literal*/template in a `Path!`-typed slot (`let p: Path! = "src"`,
`build(src: Path! = "src")`, case-clause values); an explicit `s :: Path!`
cast; `JSON`/`YAML`/`TOML.decode` into a `Path!`-typed field. Way out: a Path
**degrades to `String!` automatically at value-handoff boundaries** (see
types.md, Coercion rules), plus `.string` for an explicit exit; interpolation
and `toString` yield the bare path string.

- `.name -> String!` — last element (`Path("/").name` is `"/"`, `Path(".").name` is `"."`)
- `.stem -> String!` — `name` minus extension
- `.extension -> String` — after the last dot in `name`, dot excluded; **null** when none; a leading dot alone (`.bashrc`) is not an extension
- `.parent -> Path!` — parent of `/` is `/`; of a bare name is `.`
- `.parts -> [String!]!` — segments, root excluded; `.` has no segments
- `.ascend { dir => ... } -> [Path!]!` — the path, then each ancestor up to the root (`Path("/usr/local")` → `[/usr/local, /usr, /]`); a relative path stops at its first segment (`a/b` → `[a/b, a]`), never `.`. Calls the block on each and returns the list; the block is **required** (`{ d => null }` just to get the list)
- `.descend { dir => ... } -> [Path!]!` — the mirror of `ascend`, root first (`[/, /usr, /usr/local]`); `Path(".")` yields `[.]`
- `.isAbsolute -> Boolean!`, `.isRoot -> Boolean!` (exactly `/`)
- `.join(other: Path!) -> Path!` — lexical concat + normalize; chain for multiple; literals coerce (`p.join("bin")`)
- `.relativeTo(base: Path!) -> Path` — **null** when not expressible (mixed abs/rel, or base escaping via `..`)
- `.contains(other: Path!) -> Boolean!` — inclusive lexical containment; mixed abs/rel is never containment
- `.matches(pattern: String!) -> Boolean!` — shell glob; raises on invalid pattern
- `.string -> String!` — the normalized string

A String *variable* does not auto-coerce into a `Path!` slot (the literal-only
rule applies to every scalar) — wrap it: `Path(dir)`. There is no `+` on
Paths; use `.join` or interpolation.

## `[T]!` methods (lists)

Dang's collection types are lists and string-keyed maps (see [`Map[T]!` methods](#map-methods) below); there are no sets. Block params shown as `x`/`i`.

### Construction / access
- literal `[1, 2, 3]`; trailing comma allowed (`[1, 2, 3,]`)
- empty `[]` takes its element type from context — no hint needed in a typed slot or argument (`E(args: [])`, `let xs: [Int!]! = []`, a field default), beside a typed operand (`xs ?? []`, `(m["k"] ?? []) + [1]`), as an `if`/`case` branch (`if (c) { ["a"] } else { [] }`), as a `reduce` initial (`.reduce([]) { ... }`), or as an untyped local the first later use pins (`let out = []; out += ["a"]`). An untyped `let` is **monomorphic**, so one empty binding can't serve two element types: `let e = []; e + [1]; e + ["a"]` → `cannot use [String!]! as [Int!]!` — annotate it (`let e: [Int!]! = []`) or use separate literals. `[] :: [Int!]!` still works but is rarely needed
- concatenation `[1, 2] + [3, 4]`
- `xs[0]` — element access; **out-of-bounds yields `null`** (result is `T`, not `T!`); chained `matrix[0][1]`
- `.length -> Int!`, `.isEmpty -> Boolean!`

### Transform / select / aggregate
- `.map { x, i => ... } -> [U]!`
- `.flatMap { x, i => [U]! } -> [U]!` — map, then concatenate the lists; replaces `.reduce([]) { all, x => all + x.items }`
- `.filter { x => Boolean! } -> [T]!`, `.reject { x => Boolean! } -> [T]!`
- `.find { x => Boolean! } -> T` — first element the block accepts, or **null** when none does (stops at the first match); prefer it over `.filter { ... }[0]`
- `.reduce(initial: U) { acc, x => ... } -> U` — `initial` positional or named `initial:`
- `.uniq -> [T]!` — drop duplicates, keep first-occurrence order; uses Dang equality (works on nested lists)
- `.each { x, i => ... } -> [T]!` — returns the original list (for chaining / side effects)
- `.any { x => Boolean! } -> Boolean!`, `.all { x => Boolean! } -> Boolean!`
- `.contains(element: T) -> Boolean!`
- `.indexOf(element: T) -> Int` — index of the first equal element (Dang equality), or **null** when absent (not `-1`)

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

## `Map[T]!` methods {#map-methods}

Maps are immutable, keyed by `String!` (only the value type is a parameter: `Map[Int!]!`), and keep **insertion order**. Block params are `key, value`.

### Construction / access
- literal `["a": 1, "b": 2]`; keys are any `String!` expression (`[name: true]`); a repeated key keeps its first position and last value
- empty `[:]` infers its value type from context exactly like `[]` (`E(ds: [:])`, `m ?? [:]`, `.reduce([:]) { acc, k, v => acc.with(k, v) }`); annotate only when one untyped binding must serve two value types
- `m["a"]` / `.get(key: String!) -> T` (nullable) — **missing key yields `null`**
- `.has(key: String!) -> Boolean!`
- `.length -> Int!`, `.isEmpty -> Boolean!`
- `.keys -> [String!]!`, `.values -> [T]!` — insertion order

### Update (each returns a new map)
- `.with(key: String!, value: T) -> Map[T]!`, `.without(key: String!) -> Map[T]!`
- `.merge(other: Map[T]!) -> Map[T]!` — `other` wins on conflicts; there is no `+` on maps

### Iterate
- `.map { key, value => ... } -> Map[U]!` — transforms values, keeps keys
- `.each { key, value => ... } -> Map[T]!` — returns the original map
- `.reduce(initial: U) { acc, key, value => ... } -> U`

Equality ignores order (`["a": 1, "b": 2] == ["b": 2, "a": 1]`). `JSON.encode` emits a map as an object in insertion order (`sortKeys: true` sorts it). `JSON`/`YAML`/`TOML.decode` into a `Map[T]` type reads an object, materializing each value at `T`; decoded maps come back with **sorted** keys (document order is not kept).

## JSON, YAML, and TOML

Each format is a codec namespace — `JSON`, `YAML`, `TOML` — with static `encode` / `decode` methods. The names double as scalar types (`:: JSON`) and are owned by Dang, so they work with **no `import`**; a schema's scalar of the same name (e.g. Dagger's `scalar JSON`) gets the codec's `encode`/`decode` grafted on (`Dagger.JSON.encode` works), while unqualified `JSON` stays the builtin.

Parsing is **type-driven** — `JSON.decode` / `YAML.decode` / `TOML.decode` produce values of the *expected* type, which comes from a `::` cast, an annotation, or the parameter/return type at the call site:
```dang
let summary: Summary! = JSON.decode("""{"name": "test", "count": 42}""")
let status: Status! = JSON.decode("\"PASSED\"")
let s = JSON.decode(...) :: Status!
f(d: String!): Summary! { JSON.decode(d) }
let cfg: Settings! = TOML.decode("count = 1")   # TOML's top level is always a table
```
- Until it reaches an expected type, a decode result has the opaque type `Decoded`: it can be stored, passed, and returned, but `JSON.decode("1") + 1`, `JSON.decode("1") == 1`, `if (JSON.decode("true"))`, and `.field` on it are type errors — cast first. Joins with typed values too: `(JSON.decode(s) :: Int!) rescue 0`.
- Works for primitives, lists, maps (`Map[T]`, from an object), records, custom types, enums.
- Unknown/extra fields in the input are ignored, not errors. `decode` rejects trailing data after the first value.
- `decode(data, strict: true)` (all three formats; default `false`) rejects instead, like Go yaml.v3's `KnownFields(true)`: an input key that matches no field (or constructor parameter) of the target type/record → `<path>: field <key> not found` (`@FORMAT.field(name:)` renames count, so the declared name is unknown; an `@FORMAT.ignore` field reads no key, so its name is unknown too; `let` fields count, so encode output always decodes back; `Map[T]` accepts any key). Every unknown key in the whole value is reported in one error, one line each (an object's own keys sorted, then its fields' in declaration order; list elements by index, map entries by key); any other decode error still stops at once. It also rejects a key repeated within one JSON object or YAML mapping (non-strict keeps the last) → `<Format>.decode: invalid <Format>: <path>: line N: field <key> already set`; TOML always rejects repeats. YAML `<<` merges are not repeats. **The two paths differ on purpose:** duplicates are found while parsing, so their path is the document's (`web`, `a.b`) with a line number; unknown keys are found while materializing at the type, so their path is the typed one shared with other decode errors (a map entry reads `["web"]`). The flag stays with a `Decoded` value until it materializes.
- YAML anchors/aliases and `<<` merge keys are resolved; a mapping's own keys beat merged ones, and earlier entries of `<<: [*a, *b]` beat later ones.
- Empty input differs by format: `JSON.decode("")` errors (invalid JSON); `YAML.decode("")` is `null` (an empty YAML document), so it won't materialize into a non-null record; `TOML.decode("")` is an empty table, so it fills declared defaults.

### Coercion during parsing
- Enum values decode from their string names (`"PASSED"` → `Status.PASSED`).
- Custom scalars decode from their string forms — except `JSON` (below).
- **Which scalars accept non-string JSON** depends on who owns them. The builtin `JSON` (and a `scalar JSON` you declare) accepts any value — see below. A scalar from an imported GraphQL **schema** (without a Dang body) accepts any JSON value and holds it as JSON text — decoding `{"a": [1]}` into a field typed with a schema's scalar works. Other **Dang-owned** scalars — `YAML`/`TOML`, `Path`, `Regexp`, and any other `scalar` you declare — materialize **only from a string**: decoding a number into `scalar Foo` raises `expected string for scalar Foo, got number`. Model such a value as a record/`Map` of concrete types, as `JSON`, or keep the raw text in a `String!`.
- Record/object fields fall back to declared defaults when absent; nullable fields absent from input decode to `null`.

### `JSON` values: untyped JSON
`JSON` is also Dang's "any" type: decoding (JSON, YAML or TOML) into `JSON` accepts a value of **any** shape and holds it as its compact **JSON text** — so data of unknown shape can be kept, inspected a level at a time, and encoded back:
```dang
let top = YAML.decode(raw) :: Map[JSON!]!            # any values; check top.keys
let mods = JSON.decode(top["modules"]!) :: [Map[JSON!]!]!   # walk one level deeper
let name = JSON.decode(mods[0]!["name"]!) :: String!
```
- A decoded JSON value **is JSON text**: `toString(j)` and `${j}` give that text, and a string is held quoted — `"x"` is the text `"x"` (with the quotes), never `x`. Decoding a JSON string into `JSON` used to give the bare string; decode into `String!` for that. JSON and YAML numbers keep their exact digits (TOML's floats go through float64: `2.0` is held as `2`).
- `JSON.decode(j)` takes a JSON value as well as a `String!` (JSON values degrade to `String`), re-materializing it at the expected type.
- `JSON.encode` / `YAML.encode` / `TOML.encode` embed a JSON value as the data it holds, not as a string: `JSON.encode(JSON.decode(s) :: JSON!)` is `s` compacted, with object keys sorted (and, as for any string `JSON.encode` writes, `<`, `>` and `&` escaped as `\u003c` etc.). Text that is not JSON raises.
- A `String` cast to `JSON` (`s :: JSON!`, `let j: JSON! = "..."`) is taken as JSON text as-is, unvalidated (encoding raises if it isn't JSON). Equality compares the text, so a cast `"{\"a\": 1}"` is not `==` the decoded (compacted) `{"a":1}`. JSON `null` stays Dang `null`: use `Map[JSON]!` where values may be null.
- `toString` of a list, map or record holding JSON values renders them as data too, like `JSON.encode` (but under canonical field names).
- This is the builtin `JSON` (and a `scalar JSON` you declare yourself). A schema's `scalar JSON` (e.g. `Dagger.JSON`) is a plain schema scalar: servers disagree on its wire form, so its values stay as the server sends them (a string is held bare). Unqualified, `JSON` always names the builtin, since imports never shadow builtins; hand a value to the schema's JSON as text: `(j :: String!) :: Dagger.JSON!`.

### Serialization
- `JSON.encode(value) -> String!`, `YAML.encode(value)`, `TOML.encode(value)`. `JSON.encode` keeps keys in **declaration order** (below); `YAML.encode`/`TOML.encode` emit them in **alphabetical** order. `TOML.encode` requires a table (record) at the top level and drops null fields (TOML has no null); JSON/YAML keep them.
- `JSON.encode(value, indent: String! = "", escapeHTML: Boolean! = true, sortKeys: Boolean! = false)` — like Go's `json.Marshal` of structs: compact, `<`, `>`, `&` escaped as `\u003c`/`\u003e`/`\u0026`, a `type`'s fields in declaration order, a record's in the order it was built (a literal's as written — an annotation listing them differently doesn't reorder it — a selection's as selected, a decoded record's as its type lists them), a map's in insertion order. `indent: "  "` pretty-prints one copy per nesting level (`json.MarshalIndent(v, "", indent)`; spaces/tabs only); `escapeHTML: false` writes `<>&` bare; `sortKeys: true` sorts the keys of **every** object, maps included. No trailing newline either way.
- `toString(value)` — pass-through for strings, JSON-encode otherwise. Like `List.join`, it JSON-encodes in the same order as `JSON.encode`'s default, but under each field's own name (`@JSON.field`/`@JSON.ignore` don't apply).

### Errors (all recoverable with `rescue`)
- invalid input → `<Format>.decode: invalid <Format>: ...` (e.g. `JSON.decode: invalid JSON: ...`, `YAML.decode: invalid YAML: ...`)
- missing required field → `<path>: missing required field`
- wrong type for field → raises
- invalid enum value → `<path>: invalid enum value "X" for <Enum>`
- with `strict: true`: unknown keys → one `<typed path>: field <key> not found` line per key, all in one error; repeated key → `<Format>.decode: invalid <Format>: <document path>: line N: field <key> already set`

## `Random` module
- `Random.int(min: Int!, max: Int!) -> Int!` — `min` inclusive, `max` exclusive (errors if `min >= max`)
- `Random.float -> Float!` — `[0.0, 1.0)`
- `Random.string -> String!` — cryptographically random base32, ≥128 bits entropy

## `UUID` module
- `UUID.v4 -> String!` — random UUID v4
- `UUID.v7 -> String!` — time-ordered UUID v7

## Error types
- `Error` — interface with `message: String!`
- `BasicError` — concrete type behind `raise "msg"`, and nothing else; implements `Error`
- `AssertionError` — a failed `assert` block (`message` carries the offending expression and sub-values)
- `RuntimeError` — interpreter faults: division by zero, failed non-null assertions/casts, invalid enum values
- `GraphQLError` — a GraphQL response reporting errors; adds `path: [String!]!` (failing field) and `extensions: String!` (extensions object as JSON text, `"{}"` when absent)
