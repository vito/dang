\use-plugin{dang}

# Standard library reference {#stdlib}

> Meta: alphabetical reference, not a tutorial. Each entry: signature, one-line description, one tiny example. Group by module/receiver. Cross-link to the conceptual page that introduces the API.

## Top-level functions

- `assert { Boolean! } -> Void` — runtime check; raises on false
- `assert(message: String!) { Boolean! } -> Void` — with custom failure message
- `print(value: a) -> Void` — write to stdout
- `toString(value: a) -> String!` — pass through strings, JSON-encode others
- `toJSON(value: a) -> String!` — JSON-encode anything
- `fromJSON(text: String!) -> a` — type-driven JSON parse
- `fromYAML(text: String!) -> a` — type-driven YAML parse

## `String!` methods

- `.length`, `.isEmpty`
- `.toUpper`, `.toLower`
- `.contains(sub: String!)`, `.hasPrefix(p: String!)`, `.hasSuffix(p: String!)`
- `.trim(charset: String!)`, `.trimLeft(...)`, `.trimRight(...)`, `.trimSpace`
- `.trimPrefix(p: String!)`, `.trimSuffix(p: String!)`
- `.padLeft(width: Int!)`, `.padRight(width: Int!)`, `.center(width: Int!)`
- `.split(sep: String!, limit: Int = 0)`
- `.replace(old: String!, new: String!, count: Int = -1)`

## `[T]!` methods

- `.length`, `.isEmpty`
- `.contains(value: T)`
- `.map { x => ... }`, `.filter { x => ... }`, `.reject { x => ... }`
- `.reduce(init: U) { acc, x => ... }`
- `.each { x => ... }`, `.each { x, i => ... }`
- `.any { x => ... }`, `.all { x => ... }`
- `.takeFirst`, `.takeFirst(n: Int!)`
- `.takeLast`, `.takeLast(n: Int!)`
- `.dropFirst`, `.dropFirst(n: Int!)`
- `.dropLast`, `.dropLast(n: Int!)`
- `.takeWhile { x => ... }`, `.dropWhile { x => ... }`
- `.join(sep: String!)` (`[String!]!` only)

## `Random` module

- `Random.int(min: Int!, max: Int!) -> Int!`
- `Random.float -> Float!`
- `Random.string -> String!`

## `UUID` module

- `UUID.v4 -> String!`
- `UUID.v7 -> String!`

## `Error` interface

- `pub message: String!`

> Meta: when generics land properly (see `handoff.md`), update `[T]!` method signatures to show the actual type parameter rather than handwaving `a`/`U`.
