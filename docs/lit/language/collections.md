\use-plugin{dang}

# Collections {#collections}

> Meta: lists are the only collection today — say so. When maps/sets land, add subsections; until then, don't sketch a future. Big API surface; group by intent (transform / select / aggregate / slice).

- lists are the only collection type today (no maps/sets yet)

## Lists

### Construction

- literal: `[1, 2, 3]`
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

> Meta: many list operations are mirrored on strings (`split`, `contains`, etc.) and readers will look both places — see [#strings]. Block-taking list methods relate to [#blocks]; full signatures live in [#stdlib]; element/list nullability follows [#types].
