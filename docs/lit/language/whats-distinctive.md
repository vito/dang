\use-plugin{dang}

# What's distinctive {#whats-distinctive}

> Meta: the mental-model seed list — one bullet per "thing you won't have seen elsewhere," each linking to the chapter that owns it. Don't repeat topic-page material here, just seed it.

- types and root functions come from a **GraphQL schema**, not handwritten declarations ([#interop])
- **prototype-based** objects (`type Foo` declares both a type and its constructor) ([#objects])
- **multi-field selection**: `user.{{name, posts.{{title}}}}` becomes one query ([#objects])
- **null tracking** in the type system (`String` ≠ `String!`) ([#nullability], [#flow-typing])
- **optional parens** for zero-arg calls — fields and methods feel the same ([#fields])
- **directives** instead of comment pragmas ([#directives])
- **directory-level modules** — split files however ([#modules])
- **`assert { ... }` built in** — high-level testing without a framework ([#stdlib], [#errors])

> Meta: runtime model in one paragraph, still to write — pitch it as "values are immutable; methods on `type`s look mutating but return a forked copy." That single line saves a lot of confusion later in [#mutation].
