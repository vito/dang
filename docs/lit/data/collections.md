\use-plugin{dang}

# Collections {#collections}

> Meta: two collections today — lists and string-keyed maps. Don't sketch a future (no sets yet). Big API surface; group by intent (construct / index / transform / select / aggregate / slice).

- lists (`[a]`) and maps (`Map[a]`) are the collection types today (no sets yet)
- `[a]` is shorthand for `List[a]`; the two spellings are interchangeable in type annotations

## Lists

### Construction

- literal: `[1, 2, 3]`
- type: `[Int!]` or, equivalently, `List[Int!]`
- empty (needs type hint): `[] :: [Int!]!` or via an annotation: `let xs: [Int!]! = []`
- concatenation: `[1, 2] + [3, 4]` (associative; works with empties)

### Indexing

- `xs[0]` — element access
- out-of-bounds yields `null` (so the result is `T`, not `T!`)
- chained: `matrix[0][1]`

### Length and emptiness

- `xs.length` — `Int!`
- `xs.isEmpty` — `Boolean!`

### Transform

- `xs.map { x => f(x) }` — block also receives index: `{ x, i => ... }` → `[b]!`
- `xs.filter { x => p(x) }` — `[a]!`
- `xs.reject { x => p(x) }` — inverse of filter
- `xs.reduce(init) { acc, x => g(acc, x) }` — `init` is the (positional/named `initial:`) seed; returns `b`
- `xs.uniq` — drops duplicates, keeps first-occurrence order; uses Dang equality (works on nested lists)

### Iterate

- `xs.each { x => ... }`
- `xs.each { x, i => ... }` — element and index
- returns the list (for chaining); used for side effects

### Predicates

- `xs.any { x => p(x) }`
- `xs.all { x => p(x) }`
- `xs.contains(value)`

### Slice

- `xs.takeFirst`, `xs.takeFirst(n)`
- `xs.takeLast`, `xs.takeLast(n)`
- `xs.dropFirst`, `xs.dropFirst(n)`
- `xs.dropLast`, `xs.dropLast(n)`
- `xs.takeWhile { x => p(x) }`
- `xs.dropWhile { x => p(x) }`

### Join

- `xs.join(", ")` — `String!`; non-string elements are stringified (numbers, bools, etc.)

## Maps

Maps are immutable, string-keyed collections with a homogeneous value type. Keys
are always `String!`; only the value type is parameterized.

### Construction

- literal: `["alice": "admin", "bob": "user"]`
- type: `Map[a]` — e.g. `Map[Int!]` is a map of `Int!` values (there is no `[a]`-style shorthand for maps)
- empty map: `[:]` (distinct from the empty list `[]`); needs a type hint, e.g. `let m: Map[Int!]! = [:]`
- keys may be any `String!` expression, not just literals: `[key: value]`

### Indexing

- `m["alice"]` — value access by key
- a missing key yields `null` (so the result is `T`, not `T!`)

### Length and emptiness

- `m.length` — `Int!`
- `m.isEmpty` — `Boolean!`

### Lookup

- `m.get("key")` — value or `null` (same as `m["key"]`)
- `m.has("key")` — `Boolean!`
- `m.keys` — `[String!]!`, in insertion order
- `m.values` — `[a]!`, in insertion order

### Derive (immutable — these return new maps)

- `m.with("key", value)` — a new map with the key set (replaces in place if present, keeping position)
- `m.without("key")` — a new map with the key removed (no-op if absent)
- `m.merge(other)` — a new map combining both; `other`'s values win on key conflicts

### Transform and iterate

- `m.map { key, value => f(key, value) }` — transforms values, preserves keys → `Map[b]!`
- `m.each { key, value => ... }` — iterates in insertion order, returns the original map (for chaining)

> Note: maps are equal when they hold the same entries, regardless of insertion
> order: `["a": 1, "b": 2] == ["b": 2, "a": 1]`.

## Nullable lists vs. lists of nullables

- `[T]` — list might be null
- `[T]!` — list is non-null; elements might be null
- `[T!]!` — both non-null

### Methods on nullable lists

- list methods work on `[T]` (nullable) receivers
- null propagates: if the list is `null`, the method returns `null` (e.g. `nullList.map { ... } == null`, `nullList.length == null`)

## Type inference for heterogeneous elements

- `[Cat, Dog]` where both implement `Animal` → `[Animal!]` (a common supertype/interface)
- when several common interfaces exist, the first common one found is picked
- mixing `null` widens elements to nullable: `[WithNull, WithValue]` → `[Nullable]`
- single-element or same-type lists need no supertype inference

> Note: `.length` and `.isEmpty` are list-only — strings do *not* have them (see [#strings]).

> Meta: many list operations are mirrored on strings (`split`, `contains`, etc.) and readers will look both places — see [#strings]. Block-taking list methods relate to [#blocks]; full signatures live in [#stdlib]; element/list nullability follows [#nullability].
