# 🌱 sprout

> a little language that grows when you feed it

Sprout is a language for scripting GraphQL, with types and functions loaded from
the API endpoint.


## sample

Here's `.dagger/main.spr` at the time of this writing:

```sp
type Sprout {
  pub source: Directory! @defaultPath(
    path: "/",
    ignore: [
      "Session.vim"
      "/sprout"
      "/zed-sprout/grammars/",
      "/.env"
    ]
  )

  pub build: File! {
    go(source).binary("./cmd/sprout")
  }

  pub test: Container! {
    go(source).base.
      withDirectory("/src", source).
      withWorkdir("/src").
      withExec(["go", "test", "-v", "./..."], experimentalPrivilegedNesting: true)
  }
}
```


## why?

The initial goal was a "native" language for [Dagger]. Dagger is a polyglot
function engine with an underlying GraphQL API serving as the common layer where
functions written in different languages call one another.

Combining Sprout with Dagger gives you a polyglot language with an ecosystem of
modules developed in any language that has a [Dagger SDK]. Sprout is one such
Dagger SDK, so it's perfect for writing Dagger modules that simply glue together
APIs and don't need a heavy full-blown language runtime. As a result of not
needing a codegen phase, it has potential to be much, much faster than the other
SDKs.

Architecturally, Sprout is decoupled from Dagger; it just speaks GraphQL, so you
can point it at any API endpoint you want.

[Dagger]: https://dagger.io
[Dagger SDK]: https://docs.dagger.io/api/sdk/


## design philosophy

* **familiarity** over theory
  - I've had my fun with language design/impl, time to make one people might actually use. :)
* **ergonomics** over syntactic purity
  - Embrace keywords and first-class syntax for common patterns. Don't obsess over homoiconicity and macros.
* **expressiveness** over performance
  - This is a glue language; it's unlikely to be the bottleneck. Dev performance is more important than runtime performance.
* **safety** over ... uh ... danger
  - I should be able to have some confidence in my "production-shipping glue code" without having to ship to production.
* **be a leaf in the wind**
  - Sprout shouldn't take too much brain juice; that's already been spent on the product that it's used to build/test/ship.


### cute bits

* **multi-field selection**
  - `user.{name, posts.{title, createdAt}}}` fetches everything in one query
* **null tracking**
  - `String` does not satisfy `String!`, but `String!` satisfies `String`
* **optional parentheses** for functions without required args
  - `container() == container`
* **named arguments** with **positional shorthand**
  - `container.from(address: "foo") == container.from("foo")`
* **directives**
  - structural type-checked metadata (`@defaultPath(...)`) instead of comment pragmas
* **prototype-based objects**
  - `type Foo(bar: String!) { ... }` declares a new `Foo` type and `Foo("xyz")` constructor
* **directory-level loading**
  - Similar to Go; split your code up at your liesure.


## how the meat was made

This language needs to be maintainable in very limited time as a side project.
To that end:

* There's a single [Pigeon] grammar from which a [Tree-sitter] grammar is
  generated, so I don't have to maintain both. Feel free to steal this for your
  own esolang!
* The language has a built-in `assert { ... }` syntax so that I can test it at
  a very high level (a Big Pile of Sprout Scripts).
* Large swathes of the codebase have been implemented with AI. The language
  design is still my fault, but I've let AI do a lot of the typing.

  Personally, having already created [Bass] recently I didn't feel the
  motivation to start all over again. It didn't seem like I'd learn much along
  the way, so it wasn't worth spending whatever mileage my fingers have left.
  This was a great opportunity to learn about AI and reach my project goals at
  the same time, so I took that direction instead.

  This project sat unfinished for 2 years, and in a day of AI crunching I was
  finally able to bring it to life. If you have ethical concerns with this or
  think that makes the project "lame," I respect that. If you can, just pretend
  this project stayed in that unfinished state. Maybe check out [Bass] instead,
  which may be more interesting to language nerds anyway. :)

  If it's any consolation, I will not be using AI for the parts that interface
  with humans. This README is 100% farm-raised, and any website or logo will be,
  too.

[Bass]: https://github.com/vito/bass
[Pigeon]: https://github.com/mna/pigeon
[Tree-sitter]: https://tree-sitter.github.io/tree-sitter/


## thanks

Special thanks to [@chewxy] for writing the [hm] package - having never
implemented a typed language before, I initially leaned on this heavily.
Eventually I had AI re-write a local version so I can better integrate it into
Sprout's local dialect, but I learned a lot from the original package, and its
existence was the spark that led to Sprout's creation.

[@chewxy]: https://github.com/chewxy
[hm]: https://github.com/chewxy/hm
