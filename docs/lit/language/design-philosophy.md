\use-plugin{dang}

# Design philosophy {#design-philosophy}

> Meta: the "why is it like this" page, in one arc: principles → what they rule out → what they produce. Keep it opinionated and short. The applied list is mental-model seeds — one bullet per "thing you won't have seen elsewhere," each linking to the chapter that owns it; don't repeat topic-page material here.

## core principles

These are the principles informing Dang's design, in roughly descending
priority order.

> Meta: the point of this page is quite literally to virtue signal. To keep it
> from just sounding like self-aggrandizement I'll include examples. Hopefully
> this is helpful to future contributors or for people learning the language to
> understand its decisions.

*  - favor existing concepts (block args, 
* **most delight** - block args, language-tagged code blocks
* **maximize few concepts** - 
* **be a leaf in the wind** - 

### minimize surprise, but don't shy away from delight

Tap into familiar concepts when possible, but seize opportunities to leverage
what it has in its own or chasing .

### expressiveness over performance

This is a glue language; it's unlikely to be the bottleneck. Dev performance is more important than runtime performance.

### be a leaf in the wind

Dang shouldn't take too much brain juice; that's already been spent on the product that it's used to build/test/ship.

### ergonomics over purity

Be wary of ideas that please the language designer more than the language
speaker.

"_Everything is a \_\_\_\__"  or "_a language with only \_ built-ins_" is
catnip for language designers - like trying to find the smallest winning deck
in Slay the Spire. It's a fun ideal to chase every time, but it shouldn't
override ergonomics.

**Example**: [Bass](https://bass-lang.org) is the opposite extreme: it went
full S-expressions, and even had something [more novel than
macros][bass-op] to reduce the number of built-ins even further.

Dang, in contrast, starts with a language already familiar across speakers of
all languages across the industry (GraphQL), and has built-ins like
[#stdlib-fn-assert].

[bass-op]: https://bass-lang.org/bassics#term-operative


### shift failures leftward

I should be able to have some confidence in my "production-shipping glue code" without having to ship to production.


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

> Meta: runtime model in one paragraph, still to write — pitch it as "values are immutable; methods on `type`s look mutating but return a forked copy." That single line saves a lot of confusion later in [#mutation].
