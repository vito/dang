\use-plugin{dang}

# Standard library reference {#stdlib}

> Meta: alphabetical reference, not a tutorial. Each entry: signature, one-line description, one tiny example. Group by module/receiver. Cross-link to the conceptual page that introduces the API.

> Source of truth: `pkg/dang/stdlib.go` (top-level + `String!`/`[T]!` methods), `pkg/dang/stdlib_random.go` (`Random`, `UUID`), `pkg/dang/stdlib_regexp.go` (`Regexp`/`Match`), `pkg/dang/assert.go` (`assert`), `pkg/dang/env.go` (prelude type installs: `Error`, `BasicError`).

## Top-level functions

- `assert { Boolean! } -> Null` — runs the block; raises an `AssertionError` if it isn't truthy. Block, not parens: `assert { x == 1 }`. See [#errors].
- `assert(message: String! = null) { Boolean! } -> Null` — `message` is an optional named arg, defaults to `null`. The failure message includes the source expression and its sub-values.
- `print(value: a) -> Null` — write a value to stdout (newline-terminated)
- `toString(value: a) -> String!` — pass through strings unchanged, JSON-encode everything else
- `toJSON(value: a) -> String!` — JSON-encode anything. See [#json-yaml].
- `fromJSON(data: String!) -> a` — parse JSON into a deferred value materialized by the expected type. See [#json-yaml].
- `fromYAML(data: String!) -> a` — parse YAML into a deferred value materialized by the expected type. See [#json-yaml].

> verify: `print`/`assert` return a fresh type variable (`NullValue`), surfaced as `null`. `Void` is not a real type; treat the return as null.

## `String!` methods

> See [#strings]. Note: `.length`/`.isEmpty` are **list-only** — there is no String length/isEmpty builtin.

- `.toUpper -> String!`, `.toLower -> String!`
- `.contains(substring: String!) -> Boolean!`
- `.hasPrefix(prefix: String!) -> Boolean!`, `.hasSuffix(suffix: String!) -> Boolean!`
- `.trim(cutset: String!)`, `.trimLeft(cutset: String!)`, `.trimRight(cutset: String!)`, `.trimSpace`
- `.trimPrefix(prefix: String!)`, `.trimSuffix(suffix: String!)`
- `.padLeft(width: Int!)`, `.padRight(width: Int!)`, `.center(width: Int!)` (space-padded; no-op if already ≥ width)
- `.split(separator: String!, limit: Int = 0) -> [String!]!` — empty separator splits into characters
- `.replace(old: String!, new: String!, count: Int = -1) -> String!` — `count = -1` replaces all

### `String!` regex methods

> Backtick template strings auto-coerce to the `Regexp` scalar, so a pattern is usually written as `` `\d+` ``. Go `regexp/syntax`.

- `.containsMatch(pattern: Regexp!) -> Boolean!`
- `.match(pattern: Regexp!) -> Match` — first match, or null
- `.matchAll(pattern: Regexp!) -> [Match!]!`
- `.replaceMatches(pattern: Regexp!, with: String!, count: Int = -1) -> String!` — `$0`/`$1`/`${name}` backref expansion
- `.rewriteMatches(pattern: Regexp!, count: Int = -1) { match => String! } -> String!`
- `.splitMatches(pattern: Regexp!, limit: Int = 0) -> [String!]!`

### `Match` object

- `.string -> String!` — whole matched substring
- `.start -> Int!`, `.end -> Int!` — byte offsets
- `.captures -> [String!]!` — positional groups (`captures[0]` is `$1`)
- `.capture(name: String!) -> String` — named group; null if absent/unmatched

## `[T]!` methods

> See [#collections]. Block params are `item` (and `index` where a second param is accepted); shown below as `x`/`i`.

- `.length -> Int!`, `.isEmpty -> Boolean!`
- `.contains(element: T) -> Boolean!`
- `.uniq -> [T]!` — drop duplicates, keep first occurrence order
- `.map { x, i => ... } -> [U]!`
- `.filter { x => Boolean! } -> [T]!`, `.reject { x => Boolean! } -> [T]!`
- `.reduce(initial: U) { acc, x => ... } -> U`
- `.each { x, i => ... } -> [T]!` — returns the original list
- `.any { x => Boolean! } -> Boolean!`, `.all { x => Boolean! } -> Boolean!`
- `.takeFirst(count: Int = 1) -> [T]!`, `.takeLast(count: Int = 1) -> [T]!`
- `.dropFirst(count: Int = 1) -> [T]!`, `.dropLast(count: Int = 1) -> [T]!`
- `.takeWhile { x => Boolean! } -> [T]!`, `.dropWhile { x => Boolean! } -> [T]!`
- `.join(separator: String!) -> String!` — works on any list; non-string elements are JSON-encoded

> verify: take/drop First/Last are a single method with `count: Int = 1` (no separate no-arg vs `(n)` overloads).

## `Random` module

- `Random.int(min: Int!, max: Int!) -> Int!` — `min` inclusive, `max` exclusive (errors if `min >= max`)
- `Random.float -> Float!` — `[0.0, 1.0)`
- `Random.string -> String!` — cryptographically random base32, ≥128 bits entropy

## `UUID` module

- `UUID.v4 -> String!` — random UUID v4
- `UUID.v7 -> String!` — time-ordered UUID v7

## Error types

> See [#errors].

- `Error` — interface with `pub message: String!`
- `BasicError` — concrete type behind `raise "msg"`; implements `Error`, has `pub message: String!`
- `AssertionError` — raised by a failed `assert` (carries the offending expression and sub-values)

> Meta: when generics land properly, update `[T]!` method signatures to show the actual type parameter rather than handwaving `T`/`U`.
