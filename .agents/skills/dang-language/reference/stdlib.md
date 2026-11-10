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
- `toString(value: a) -> String!` — pass strings, enum values, and custom scalar values (Path, Regexp, URL, ...) through as their bare string; render everything else as the Dang literal it would be written as (`toString(["a", "b"])` → `["a", "b"]`, `toString({{a: 1}})` → `{{a: 1}}`, `toString(["k": 1])` → `["k": 1]`, Decoded data → the literal of its data, objects as map literals with sorted keys; floats always read as floats, `1.0`; an enum value inside a collection is `Color.RED`; a record shows its data fields, an object its public data fields (not methods or `let` fields; `{{}}` when all are `let`); a GraphQL object is the lazy query it is, `<Container: container.from(address: …)>` (a mutation's result `<Type: mutation.field(…)>`), sending nothing and showing argument names but never their values; select `.id` for the ID). String interpolation, `List.join` and `print` render values the same way. It is not an encoder: use `JSON.encode` for JSON.

`print` and `assert` return `null` — there is no `Void` type.

JSON/YAML/TOML conversions are **not** top-level functions — they live in the namespaces of the `JSON`/`YAML`/`TOML` scalars (`.encode` / `.decode`; see [JSON, YAML, and TOML](#json-yaml-and-toml) below). base64 lives on `String!` (`.toBase64` / `.fromBase64`).

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

Conversion: `toString(value)` (Dang-literal rendering of non-strings) or `value :: String!` (explicit cast where types align). A `::` cast never parses (`"42" :: Int!` is a type error); use `.toInt`.

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
- `.sum -> T` — adds up a list of `Int!` or `Float!` (`Int!` / `Float!` result); the elements are bounded by **`Numeric`** (as a user function's `[a: Numeric]`, see types.md), so any other list, including nullable elements (`[Int]!`), is a compile error where `.sum` is written, and a list whose element type isn't settled yet is checked wherever it is learned. Empty: the zero of its element type, `0` or `0.0` — an empty list holds the element type inference gave it, however it got it (an annotation, `xs ?? []`, an `if` branch, a `reduce([])` accumulator, an argument to a generic call), and a list a generic body builds holds what the call's arguments or block make its type variables. A variable the arguments don't decide — one only the call's expected result fixes (`none[a: Numeric](): [a!]! { [] }` read as `none().sum`), or one whose only arguments are null — isn't known while the body runs, so an empty list of it sums to `0` unless it then passes a typed boundary (`let xs: [Float!]! = none()`, a `[Float!]!` parameter). Replaces `.reduce(0) { a, b => a + b }`
- `.uniq -> [T]!` — drop duplicates, keep first-occurrence order; uses Dang equality (works on nested lists)
- `.sort(descending: Boolean = false) -> [T]!`, `.sortBy(descending: Boolean = false) { x => key } -> [T]!` — **stable** (equal keys keep input order, also when descending); `sortBy` computes each key once. Orderable: `Int!`/`Float!` numerically, `String!` by bytes (`"B" < "a"`), `Boolean!` (`false` first), enums in **declaration order**, custom scalars by string form (numeric scalars numerically), lists lexicographically (prefix first), `{{ }}` records field by field in written order (records written in different field orders raise when compared) — use a list for a same-typed compound key (`[x.major, x.minor]`), a record for mixed types (`{{role: x.role, name: x.name}}`). Anything else — objects, nullable keys (`?? default` them), an unbounded generic `a` — is a **compile error** where the call is written: sort's elements and sortBy's key are bounded by **`Ordered`** (the same bound a user function declares as `[a: Ordered]`, see types.md), so a receiver whose element type isn't settled yet (a `reduce([])` accumulator) or `&xs.sortBy` called later is checked wherever its type is learned
- `.reverse -> [T]!`
- `.each { x, i => ... } -> [T]!` — returns the original list (for chaining / side effects)
- `.any { x => Boolean! } -> Boolean!`, `.all { x => Boolean! } -> Boolean!`
- `.contains(element: T) -> Boolean!`
- `.indexOf(element: T) -> Int` — index of the first equal element (Dang equality), or **null** when absent (not `-1`)

### Slice
- `.takeFirst(count: Int = 1)`, `.takeLast(count: Int = 1)`, `.dropFirst(count: Int = 1)`, `.dropLast(count: Int = 1)`
- `.takeWhile { x => Boolean! }`, `.dropWhile { x => Boolean! }`

### Join
- `.join(separator: String!) -> String!` — works on any list; each element renders as `toString` renders it

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

Each format — `JSON`, `YAML`, `TOML` — is a builtin **scalar type** whose values are valid text in that format, and, as a value, the namespace of its static `encode` / `decode` methods (as a scalar's statics are, like `Regexp.escape`). Both are owned by Dang, so they work with **no `import`** (see [Builtin imports](#builtin-imports)). Untyped data is `Decoded` (below), whichever format decoded it. A schema's scalar of the same name (e.g. Dagger's `scalar JSON`) is a separate type: an ordinary schema scalar, reached qualified (`Dagger.JSON`); unqualified `JSON` stays the builtin, as a type and as a value.

### The `JSON!` / `YAML!` / `TOML!` scalars
- A value is text in its format, held as a string (`ScalarValue`), checked by the scalar's builtin `new()` hook wherever a string becomes one — a `::` cast, a literal at a typed boundary, decoding a string into it: `("{\"a\": 1}" :: JSON!)`, `let cfg: TOML! = "a = 1"`. Text that doesn't parse raises (`invalid JSON: unexpected EOF`, `invalid YAML: …`, `invalid TOML: …`, at its path inside a list or map); a string **literal** (quoted, or a template with no interpolations) is checked at compile time instead, like an enum literal (`("{" :: JSON!)`, `j == ""`, a `case` pattern `"[" =>` are compile errors); a computed string is validated when it converts, so `` j == `${s}` `` raises if `s` isn't JSON text. YAML must parse as YAML (a stream of any number of documents; `YAML.decode` reads a single one and raises `YAML.decode: the text holds more than one document, but only a single document decodes` for more), TOML as a document (a table). The text is kept as written (not reformatted).
- It degrades to `String!` like any custom scalar (`let s: String! = j`, `j :: String!`), so `JSON.decode(j)` takes one; `encode` always returns a `String!` (cast it, `JSON.encode(d) :: JSON!`, to get the scalar).
- An **explicit cast** converts between custom scalars, validating at the target: `j :: Dagger.JSON!`, `d :: JSON!` (a schema's JSON to the builtin), `blob :: JSON!`, through list/map wrappers too (`[Blob!]! :: [JSON!]!`), never dropping nullability. At a handoff (an argument, a return, a typed `let`) distinct scalars never mix: `cannot use Blob! as JSON!`.
- `JSON.encode(j)` writes a JSON value as the string it is (`"[1]"` → `"\"[1]\""`); decode it to embed its data.
- Values compare as scalars do: the same scalar and the same text (`"[1]"` ≠ `"[ 1 ]"`).

Parsing is **type-driven** — `JSON.decode` / `YAML.decode` / `TOML.decode` produce values of the *expected* type, which comes from a `::` cast, an annotation, or the parameter/return type at the call site:
```dang
let summary: Summary! = JSON.decode("""{"name": "test", "count": 42}""")
let status: Status! = JSON.decode("\"PASSED\"")
let s = JSON.decode(...) :: Status!
f(d: String!): Summary! { JSON.decode(d) }
let cfg: Settings! = TOML.decode("count = 1")   # TOML's top level is always a table
```
- Until it reaches an expected type, a decode result has the opaque type `Decoded`: it can be stored, passed, and returned, but `JSON.decode("1") + 1`, `JSON.decode("1") == 1`, `if (JSON.decode("true"))`, `.field` and `[i]` on it are type errors — cast first. Joins with typed values too: `(JSON.decode(s) :: Int!) rescue 0`.
- `Decoded` is a nameable Prelude type (a declaration named `Decoded` shadows it; an import never does), so data of unknown shape can be kept undecoded and walked a level at a time: `JSON.decode(s) :: Map[Decoded!]!` (or `[Decoded!]!`) materializes the outer object (or list) and leaves each value deferred; cast one later (`top["modules"]! :: [Map[Decoded!]!]!`, `v :: Int!`), or hand it to a typed parameter, which materializes it. A `Decoded!` field of a type keeps its data as it came (pass-through), and every encoder embeds Decoded data as the data it is, under its own keys (field directives belong to types; cast first to re-key). `Decoded!` holds null data too (a nullable cast reads it as `null`, `??` falls back past it, and `@FORMAT.field(omitNull:/omitEmpty:)` apply to a `Decoded!` field). Decoded data compares with Decoded data of the same shape (through lists, maps and records, or an empty literal of one), or with `null`, **as data**: key order, number spelling (`1.50` vs `1.5`, YAML's `-.5`) and the decoding format don't matter (`contains`/`uniq` too); comparing it with a typed value, or a union that only might be Decoded, stays an error.
- A later cast reads Decoded data the way its decode would have: with the **decoding format's** field names (`@YAML.field` for YAML data), and **strictly if the decode was strict** — `JSON.decode(s, strict: true) :: Map[Decoded!]!` makes every later cast of its values strict too.
- Works for primitives, lists, maps (`Map[T]`, from an object), records, custom types, enums.
- A type with no constructor is not a decode target: a namespace (`Random!`, an import's namespace such as `Lib!`) or a schema's object type (`Dagger.Container!`) is rejected at compile time wherever a decoded value must become one — a non-null target at the top, non-null list/map elements of a non-null list/map, or a required field: `cannot decode into Random: it has no constructor`, `cannot decode Holder.inner: its type Random has no constructor to decode into`. A nullable position (`:: Random`, `[Random]!`, a nullable or defaulted field) type-checks, and decodes when the data leaves it null or out; data there still fails at runtime (`constructor for Random not found`). Use `Decoded!` for data of any shape.
- Unknown/extra fields in the input are ignored, not errors. `decode` rejects trailing data after the first value.
- `decode(data, strict: true)` (all three formats; default `false`) rejects instead, like Go yaml.v3's `KnownFields(true)`: an input key that matches no field (or constructor parameter) of the target type/record → `<path>: field <key> not found` (`@FORMAT.field(name:)` renames count, so the declared name is unknown; an `@FORMAT.ignore` field reads no key, so its name is unknown too; `let` fields count, so encode output always decodes back; `Map[T]` accepts any key, and so does a `Decoded` value: nothing inside it is checked against a type until it is cast, though its repeated keys still are). Every unknown key in the whole value is reported in one error, one line each (an object's own keys sorted, then its fields' in declaration order; list elements by index, map entries by key); any other decode error still stops at once. It also rejects a key repeated within one JSON object or YAML mapping (non-strict keeps the last) → `<Format>.decode: invalid <Format>: <path>: line N: field <key> already set`; TOML always rejects repeats. YAML `<<` merges are not repeats. **The two paths differ on purpose:** duplicates are found while parsing, so their path is the document's (`web`, `a.b`) with a line number; unknown keys are found while materializing at the type, so their path is the typed one shared with other decode errors (a map entry reads `["web"]`). The flag stays with a `Decoded` value until it materializes.
- YAML anchors/aliases and `<<` merge keys are resolved; a mapping's own keys beat merged ones, and earlier entries of `<<: [*a, *b]` beat later ones.
- Empty input differs by format: `JSON.decode("")` errors (invalid JSON); `YAML.decode("")` is `null` (an empty YAML document), so it won't materialize into a non-null record; `TOML.decode("")` is an empty table, so it fills declared defaults.

### Coercion during parsing
- Enum values decode from their string names (`"PASSED"` → `Status.PASSED`).
- Custom scalars decode from their string forms.
- **Which scalars accept non-string JSON** depends on who owns them. A scalar from an imported GraphQL **schema** (without a Dang body) accepts any JSON value and holds it as JSON text — decoding `{"a": [1]}` into a field typed with a schema's scalar works. **Dang-owned** scalars — `JSON`, `YAML`, `TOML`, `Path`, `Regexp`, and any `scalar` you declare — materialize **only from a string**: decoding a number into `scalar Foo` raises `expected string for scalar Foo, got number` (so a `JSON!` field decodes from a string holding JSON text, not from nested data). Model such a value as a record/`Map` of concrete types, as `Decoded`, or keep the raw text in a `String!`.
- Record/object fields fall back to declared defaults when absent (or explicitly `null` for a non-null field with a default, except `Decoded!`, which holds null data as it is); nullable fields absent from input decode to `null`.

### Scalars named `JSON`
- A `scalar JSON` you declare is an ordinary scalar that shadows the builtin in its module — type and value, so `JSON.encode`/`JSON.decode` and the `@JSON.field`/`@JSON.ignore` directives are then not found there (the error says the declaration shadows the builtin).
- A schema's `scalar JSON` (e.g. `Dagger.JSON`) is a plain schema scalar: servers disagree on its wire form, so its values stay as the server sends them (a string is held bare). Unqualified, `JSON` always names the builtin, as a type and as a value, since an import's unqualified names never override an import's namespace: with Dagger imported, `fnArgs: JSON!` is the builtin scalar. Convert with a cast either way: `j :: Dagger.JSON!`, `d :: JSON!` (validated), or hand data over as text: `JSON.encode(d) :: Dagger.JSON!`. A cast carries text: a schema scalar's value the server sent as data becomes its JSON text in `JSON!` (or any Dang-owned scalar), and a schema scalar cast from text is sent to the server as a string, whatever wire form the server's scalar takes. A server that sends its scalar as raw JSON on the wire has a string value held bare (`"plain"`, not `"\"plain\""`), so casting that value to `JSON!` fails validation while a number or object works; for such a server, encode the value instead: `JSON.encode(x) :: JSON!`.

### Serialization
- `JSON.encode(value) -> String!`, `YAML.encode(value)`, `TOML.encode(value)`. `JSON.encode` and `YAML.encode` keep keys in **declaration order** (below); `TOML.encode` emits them in **alphabetical** order. `TOML.encode` requires a table (record) at the top level and drops null fields (TOML has no null); JSON/YAML keep them.
- `JSON.encode(value, indent: String! = "", escapeHTML: Boolean! = true, sortKeys: Boolean! = false)` — like Go's `json.Marshal` of structs: compact, `<`, `>`, `&` escaped as `\u003c`/`\u003e`/`\u0026`, a `type`'s fields in declaration order, a record's in the order it was built (a literal's as written — an annotation listing them differently doesn't reorder it — a selection's as selected, a decoded record's as its type lists them), a map's in insertion order, and `Decoded` data's keys sorted. `indent: "  "` pretty-prints one copy per nesting level (`json.MarshalIndent(v, "", indent)`; spaces/tabs only); `escapeHTML: false` writes `<>&` bare; `sortKeys: true` sorts the keys of **every** object, maps included. No trailing newline either way.
- `YAML.encode(value, sortKeys: Boolean! = false)` — same order as `JSON.encode` (declaration / as-built / insertion; `Decoded` data sorted), 4-space indent, `"<<"` keys quoted so they don't read back as merges; `sortKeys: true` sorts every mapping, as yaml.Marshal sorts keys (runs of digits compare as numbers, so `item2` before `item10`; `Decoded` data is sorted that way too), where `JSON.encode` sorts by bytes. `TOML.encode(value)` always sorts keys (and puts tables after plain keys, as TOML requires).
- `toString(value)` is not JSON: it renders Dang literals (see top-level builtins); encode with `JSON.encode`.

### Errors (all recoverable with `rescue`)
- invalid input → `<Format>.decode: invalid <Format>: ...` (e.g. `JSON.decode: invalid JSON: ...`, `YAML.decode: invalid YAML: ...`)
- missing required field → `<path>: missing required field`
- wrong type for field → raises
- invalid enum value → `<path>: invalid enum value "X" for <Enum>`
- with `strict: true`: unknown keys → one `<typed path>: field <key> not found` line per key, all in one error; repeated key → `<Format>.decode: invalid <Format>: <document path>: line N: field <key> already set`

## Builtin imports
`JSON`, `YAML`, `TOML`, `Random` and `UUID` are imports Dang provides every module automatically, below the module's own scope:
- **Qualified only**: `JSON.encode(x)`, `Random.int(min: 0, max: 6)`; there is no bare `encode`/`decode`/`int`.
- **Shadowable**: a declaration of the same name in the module shadows the builtin import there — `type JSON { … }`/`scalar JSON` as both type and value, `let Random = 3` as a value only (`Random!` as a type, `@JSON.field` directives, and a type's statics such as `JSON.encode` still reach the builtin).
- **Replaceable**: an import of the same name (`[imports.YAML]` + `import YAML`, or an auto-import named `YAML`) replaces the builtin import in the file importing it, as a type and as a value; other files still get the builtin.
- **Never overridden by an import**: an unqualified name of an explicit import (Dagger's `scalar JSON`, a module's `pub YAML`) never replaces one, as a type or as a value; reach the import's own qualified (`Dagger.JSON`, `Lib.YAML`).
- **Private to the module**: an import's namespace never re-exports them; `Test.YAML` and `Lib.YAML` are errors unless the schema or module itself defines a `YAML`.

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
