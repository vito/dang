\use-plugin{dang}

# Design philosophy {#design-philosophy}

> Meta: the "why is it like this" page, in one arc: principles → what they rule out → what they produce. Keep it opinionated and short. The applied list is mental-model seeds — one bullet per "thing you won't have seen elsewhere," each linking to the chapter that owns it; don't repeat topic-page material here.

\table-of-contents


## Principles

These are the principles informing Dang's design, in roughly descending
priority order. Each one is grounded in examples.


### shift failures left {#safety}

> I should be confident in my "production-shipping glue code" without having to
> ship to production.

* Type-checking, including null checking.
* Immutability, but with mutable syntax for [#ergonomics].


### minimize surprise, maximize delight

> Tap into familiar concepts when possible, but seize opportunities to delight
> the user - in other words, don't fear novelty if it brings comfort.

* Dang should fit in your head, leaning on things you already know.
* Most Dang code is code that the author wishes they didn't have to write.
  Make it brief and sweet.
* Favor Go's "all Go code looks the same" over Ruby's "I can tell who wrote this."
* [#blocks] are fun, and work just like in Ruby.
* Backtick-strings support language tags for better editor highlighting.


### expressiveness over performance

> Development-time performance is more important than runtime performance.

* Immutability, even if it means a lot of copying ([#safety]).


### ergonomics over purity {#ergonomics}

> Be wary of ideas that please the language designer more than the language
> speaker.

* "_Everything is a \_\_\_\__"  or "_a language with only \_ built-ins_" is
draws language designers like a moth to a flame. Resist the light.
* [Bass] went full S-expressions, and even had [something more novel than
macros][bass-op] to reduce the number of built-ins even further than usual.
* Dang, in contrast, starts with what is already almost a universal language
(GraphQL), and has built-ins like [#stdlib-fn-assert].

[Bass]: https://bass-lang.org
[bass-op]: https://bass-lang.org/bassics#term-operative



## Characteristics

Dang's focus is to consume and produce GraphQL APIs, so it uses GraphQL as its
baseline type system.

With GraphQL as the starting point, we get the following:

* **Type-checking**, with explicit nullability
* **Object-oriented**, with functional flavoring
* **Named arguments** -- everywhere
* No caller-side distinction between a zero-arity function and a trivial data field (like Ruby)
* First-class documentation strings
* Directives, for rich metadata
* **Interfaces**, **enums**, **scalars**
* Input objects, with native syntax

The plan from there was to try to figure out a language runtime that makes all
of this make sense, guided by the principles above.

Here's where I ended up, guided by the principles above:

* Type inference
* ...

These principles are why Dang reads the way it does -- the things you'll notice
are different, each owned by its own chapter:

- Dang doesn't have a macro system. Instead, it has Ruby-style [#blocks].
- types and root functions come from a **GraphQL schema**, not handwritten declarations ([#interop])
- **prototype-based** objects (`type Foo` declares both a type and its constructor) ([#objects])
- **multi-field selection**: `user.{{name, posts.{{title}}}}` becomes one query ([#objects])
- **null tracking** in the type system (`String` ≠ `String!`) ([#nullability], [#flow-typing])
- **optional parens** for zero-arg calls — fields and methods feel the same ([#fields])
- **directives** instead of comment pragmas ([#directives])
- **directory-level modules** — split files however ([#modules])
- **`assert { ... }` built in** — high-level testing without a framework ([#stdlib], [#errors])

> Meta: runtime model in one paragraph, still to write — pitch it as "values are immutable; methods on `type`s look mutating but return a forked copy." That single line saves a lot of confusion later in [#mutation].
