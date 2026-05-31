\use-plugin{dang}

# Collections {#collections}

> Meta: lists are the only collection today — say so. When maps/sets land, add subsections; until then, don't sketch a future. Big API surface; group by intent (transform / select / aggregate / slice).

## Lists

### Construction

- literal: `[1, 2, 3]`
- empty (needs type hint): `[] :: [Int!]!`
- concatenation: `[1, 2] + [3, 4]`

### Indexing

- `xs[0]` — element access
- out-of-bounds yields `null` (so the result is `T`, not `T!`)
- chained: `matrix[0][1]`

### Length and emptiness

- `xs.length` — `Int!`
- `xs.isEmpty` — `Boolean!`

### Transform

- `xs.map { x => f(x) }`
- `xs.filter { x => p(x) }`
- `xs.reject { x => p(x) }`
- `xs.reduce(init) { acc, x => g(acc, x) }`

### Iterate

- `xs.each { x => ... }`
- `xs.each { x, i => ... }` — element and index

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

- `xs.join(", ")` — `String!`

## Nullable lists vs. lists of nullables

- `[T]` — list might be null
- `[T]!` — list is non-null; elements might be null
- `[T!]!` — both non-null

## Type inference for heterogeneous elements

- `[Cat, Dog]` where both implement `Animal` → `[Animal!]`
- mixing `null` widens elements to nullable

> Meta: forward-reference [strings](./strings.md) — many list operations are mirrored on strings (`split`, `contains`, etc.) and readers will look both places.
