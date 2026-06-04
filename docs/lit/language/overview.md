\use-plugin{dang}

# Language overview {#overview}

> Meta: this is the "mental model" page. Don't repeat material from the topic pages — just plant the seeds (immutability + CoW, prototype objects, schema-driven types, null tracking) so the rest of the guide has hooks to hang things on.

## Design philosophy

- familiarity over theory
- ergonomics over syntactic purity
- expressiveness over performance
- safety over surprises
- "a leaf in the wind" — low cognitive overhead so brainpower stays on the glued-together product

## What's distinctive

- types and root functions come from a **GraphQL schema**, not handwritten declarations ([#graphql])
- **prototype-based** objects (`type Foo` declares both a type and its constructor) ([#objects])
- **multi-field selection**: `user.{name, posts.{title}}` becomes one query ([#objects])
- **null tracking** in the type system (`String` ≠ `String!`) ([#types], [#flow-typing])
- **optional parens** for zero-arg calls — fields and methods feel the same ([#fields])
- **directives** instead of comment pragmas ([#directives])
- **directory-level modules** — split files however ([#modules])

## How a Dang program is shaped

- a file is a sequence of declarations and forms (the start rule is `Dang`; see [#grammar])
- declarations are hoisted and order-independent within a file/directory
- `pub` exposes a name; `let` keeps it private (both are visibility keywords on field declarations)
- `assert { ... }` is built in — high-level testing without a framework ([#stdlib], [#errors])

## Runtime model in one paragraph

> Meta: pitch this as "values are immutable; methods on `type`s look mutating but return a forked copy." That single line saves a lot of confusion later in [#mutation].

## What's *not* in the language

- no inheritance (only `implements` for interfaces — see [#interfaces-unions])
- errors aren't control flow — `raise`/`try`/`catch` exist, but `return` covers early exit and null-tracking covers expected absence, so exceptions stay reserved for genuine failures ([#errors])
- no metaprogramming / macros
- no implicit scalar coercion outside `::` type ascription (the `TypeHint` form; also drives `fromJSON`/`fromYAML` materialization — see [#json-yaml])
