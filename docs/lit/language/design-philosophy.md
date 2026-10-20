\use-plugin{dang}

# Design philosophy {#design-philosophy}

> Meta: the "why is it like this" page, in one arc: principles → what they rule out → what they produce. Keep it opinionated and short. The applied list is mental-model seeds — one bullet per "thing you won't have seen elsewhere," each linking to the chapter that owns it; don't repeat topic-page material here.

- familiarity over theory
- ergonomics over syntactic purity
- expressiveness over performance
- safety over surprises
- "a leaf in the wind" — low cognitive overhead so brainpower stays on the glued-together product

## Deliberately missing

- no inheritance (only `implements` for interfaces — see [#interfaces-unions])
- no metaprogramming / macros

## The philosophy, applied

These principles are why Dang reads the way it does — the things you'll notice
are different, each owned by its own chapter:

- types and root functions come from a **GraphQL schema**, not handwritten declarations ([#interop])
- **prototype-based** objects (`type Foo` declares both a type and its constructor) ([#objects])
- **multi-field selection**: `user.{{name, posts.{{title}}}}` becomes one query ([#objects])
- **null tracking** in the type system (`String` ≠ `String!`) ([#nullability], [#flow-typing])
- **optional parens** for zero-arg calls — fields and methods feel the same ([#fields])
- **directives** instead of comment pragmas ([#directives])
- **directory-level modules** — split files however ([#modules])
- **`assert { ... }` built in** — high-level testing without a framework ([#stdlib], [#errors])

## Immutable, copy-on-write, checked

Values never change. Methods on `type`s look like they mutate, but each call
works on its own copy of `self` and returns it, so whatever you hold stays as
it was. Names are lexical: a block or method ref sees the variables and `self`
where it was written, never the caller's. And the compiler rejects every way a
write could leak out of the scope that owns it: functions can't write globals,
nothing can write statics, closures that write their scope can't be stored,
`{{ }}` fields can't write outside themselves, and defaults can't see a
half-built object. See [#mutation].
