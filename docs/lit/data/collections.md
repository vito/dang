\use-plugin{dang}
\literate-fences

# Collections {#collections}

> Meta: two collections today — lists and string-keyed maps. Don't sketch a future (no sets yet). Big API surface; group by intent (construct / index / transform / select / aggregate / slice).

Dang has two collection types: **lists** (`[a]`) and string-keyed **maps**
(`Map[a]`). Both are immutable — every operation that "changes" a collection
returns a new one — and both carry a single element type, so a `[Int!]` holds
only `Int!`s and a `Map[String!]` only `String!` values.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## Lists

A list is comma-separated values in square brackets:

```dang
["apple", "banana", "cherry"]
```

Its type is `[String!]!` — a non-null list of non-null strings. `[a]` is
shorthand for `List[a]`, and the two spellings are interchangeable in
annotations. We'll reuse a couple of lists below, so bind them now (a block
ending in a declaration prints nothing — it's just setup):

```dang
let fruits = ["apple", "banana", "cherry"]
let nums = [1, 2, 3, 4]
```

`+` concatenates two lists; it's associative and works with empties:

```dang
[1, 2] + nums
```

An empty literal can't infer its element type on its own, so it needs an
annotation (or a `:: [T]!` hint — see [#nullability]):

```dang
let none: [Int!]! = []
none.isEmpty
```

### Indexing

`xs[i]` reads the element at a zero-based index. An out-of-bounds index
yields `null`, so the result type is `T`, not `T!`:

```dang
fruits[0]
```

```dang
fruits[99]
```

The index must be an `Int!` — anything else is a compile error:

```dang-failure
fruits["first"]
```

Indexing chains, so a list of lists reads positionally:

```dang
[[1, 2], [3, 4]][1][0]
```

### Length and emptiness

```dang
[nums.length, fruits.length]
```

```dang
[nums.isEmpty, none.isEmpty]
```

> `.length` and `.isEmpty` are list methods only — strings do **not** have
> them; use the string predicates instead (see [#strings]).

### Transforming

`.map` applies a block to every element, returning a new list (see
[#blocks] for the block forms — `_` is the implicit parameter):

```dang
nums.map { _ * 2 }
```

The block can take the index as a second parameter:

```dang
fruits.map { fruit, i => `${i}: ${fruit}` }
```

`.filter` keeps the elements a predicate accepts; `.reject` is its inverse:

```dang
[nums.filter { _ % 2 == 0 }, nums.reject { _ % 2 == 0 }]
```

`.reduce` folds the list down to a single value, threading an accumulator from
a seed (positional, or named `initial:`) through each element:

```dang
nums.reduce(0) { acc, x => acc + x }
```

`.uniq` drops duplicates, keeping first-occurrence order. It uses Dang
equality, so it works on nested lists too:

```dang
[1, 1, 2, 3, 3, 1].uniq
```

### Iterating

`.each` runs a block for its side effects and returns the original list, so
calls keep chaining. Like `.map`, it can take the index:

```dang
fruits.each { fruit, i => print(`${i} = ${fruit}`) }
```

### Asking questions

`.any` and `.all` test a predicate across the list; `.contains` tests for a
specific value:

```dang
[nums.any { _ > 3 }, nums.all { _ > 0 }, fruits.contains("banana")]
```

### Slicing

`.takeFirst` / `.takeLast` keep elements from an end, `.dropFirst` /
`.dropLast` discard them. Each takes an optional count (default 1):

```dang
[nums.takeFirst(2), nums.dropLast]
```

`.takeWhile` / `.dropWhile` cut at the first element that fails a predicate:

```dang
[1, 2, 3, 10, 1].takeWhile { _ < 5 }
```

### Joining

`.join` concatenates the elements into a `String!` with a separator;
non-string elements are stringified:

```dang
nums.join(" + ")
```

## Maps

Maps are immutable, string-keyed collections with a homogeneous value type.
Keys are always `String!`; only the value type is parameterized. A literal
pairs keys with values:

```dang
let roles = ["alice": "admin", "bob": "user"]
roles
```

The value type is inferred (`Map[String!]` here), written `Map[a]` — there is
no `[a]`-style shorthand for maps. Keys may be any `String!` expression, not
just literals. The empty map is `[:]`, distinct from the empty list `[]`, and
like an empty list it needs a type hint:

```dang
let counts: Map[Int!]! = [:]
counts.isEmpty
```

### Indexing and lookup

`m["key"]` reads a value; a missing key yields `null`, so the result is `T`,
not `T!`. `.get` is the method form of the same lookup:

```dang
[roles["alice"], roles.get("carol")]
```

`.has` tests for a key:

```dang
roles.has("dave")
```

`.keys` and `.values` return lists in insertion order, and `.length` counts
the entries:

```dang
[roles.keys, roles.values]
```

### Deriving new maps

Maps are immutable, so these return a *new* map and leave the original
untouched. `.with` sets a key (replacing in place if present, keeping its
position), `.without` removes one, and `.merge` combines two — the argument's
values winning on conflicts:

```dang
roles.with("carol", "admin")
```

```dang
roles.merge(["bob": "owner", "dave": "guest"])
```

A map's value type is fixed, so a `.with` of the wrong type is a compile
error:

```dang-failure
["a": 1].with("b", "two")
```

### Transforming and iterating

`.map` transforms the values and preserves the keys, and its block receives
both; `.each` iterates in insertion order and returns the original map:

```dang
roles.map { name, role => role.toUpper }
```

Two maps are equal when they hold the same entries, regardless of insertion
order:

```dang
["a": 1, "b": 2] == ["b": 2, "a": 1]
```

## Nullable elements and nullable collections

The `!` sigil (see [#nullability]) sits in two independent places on a list
type — the list itself, and its elements:

| written | meaning |
|---|---|
| `[T]` | nullable list of nullable T |
| `[T]!` | non-null list of nullable T |
| `[T!]` | nullable list of non-null T |
| `[T!]!` | non-null list of non-null T |

A list whose elements may be null is the common case for parsed or
fetched data. Indexing such a list gives a nullable element, which `??` (see
[#operators]) or a `.map` can recover:

```dang
let scores = [10, null, 30]
scores.map { _ ?? 0 }
```

When the *list itself* is null, methods short-circuit and return `null`
rather than raising — `.length`, `.map`, and the rest all yield `null`, the
same way null propagates through field access:

```dang
let missing = null :: [Int!]
missing.map { _ + 1 }
```

## Heterogeneous elements

A list literal whose elements differ in type infers their nearest common
type. `Cat` and `Dog` here both implement `Animal` (see
[#interfaces-unions]), so the list is an `[Animal!]` — and only the shared
`Animal` surface is available on its elements:

```dang
interface Animal { name: String! }
type Cat implements Animal { name: String! }
type Dog implements Animal { name: String! }

[Cat(name: "Whiskers"), Dog(name: "Rex")].map { _.name }
```

Mixing `null` in widens the elements to nullable; elements with no common
type at all are rejected:

```dang-failure
[1, "two"]
```

> Meta: many list operations are mirrored on strings (`split`, `contains`, etc.) and readers will look both places — see [#strings]. Block-taking list methods relate to [#blocks]; full signatures live in [#stdlib]; element/list nullability follows [#nullability].
