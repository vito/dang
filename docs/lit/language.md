\use-plugin{dang}
\split-sections

# The language {#language}

> Meta: this page is both the part opener and the mental-model overview — plant
> the seeds (immutability + CoW, prototype objects, schema-driven types, null
> tracking) so the chapters have hooks to hang things on, but don't repeat
> their material. The body must stay heading-free: this is a \split-sections
> page, so headings would split off into their own pages — use bold lead-ins.

The everyday core: syntax, operators, functions, blocks, control flow, and
errors. If you've written any modern scripting language, most of this part is
"yes, like that" — the differences are flagged where they bite.

**Design philosophy** — familiarity over theory; ergonomics over syntactic
purity; expressiveness over performance; safety over surprises. "A leaf in the
wind": low cognitive overhead, so brainpower stays on the glued-together
product.

**What's distinctive:**

- types and root functions come from a **GraphQL schema**, not handwritten declarations ([#interop])
- **prototype-based** objects (`type Foo` declares both a type and its constructor) ([#objects])
- **multi-field selection**: `user.{{name, posts.{{title}}}}` becomes one query ([#objects])
- **null tracking** in the type system (`String` ≠ `String!`) ([#nullability], [#flow-typing])
- **optional parens** for zero-arg calls — fields and methods feel the same ([#fields])
- **directives** instead of comment pragmas ([#directives])
- **directory-level modules** — split files however ([#modules])

**How a Dang program is shaped:**

- a file is a sequence of declarations and forms (the start rule is `Dang`; see [#grammar])
- declarations are hoisted and order-independent within a file/directory
- names are public by default; `let` keeps a name private/local (`pub` is an accepted, legacy marker that `dang fmt` removes — see [#fields])
- `assert { ... }` is built in — high-level testing without a framework ([#stdlib], [#errors])

> Meta: runtime model in one paragraph, still to write — pitch it as "values are immutable; methods on `type`s look mutating but return a forked copy." That single line saves a lot of confusion later in [#mutation].

**What's *not* in the language** — no inheritance (only `implements` for
interfaces — see [#interfaces-unions]); no metaprogramming / macros.

\table-of-contents

\include-section{./language/syntax.md}
\include-section{./language/operators.md}
\include-section{./language/fields-functions.md}
\include-section{./language/blocks.md}
\include-section{./language/control-flow.md}
\include-section{./language/errors.md}
