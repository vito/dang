\use-plugin{dang}
\literate-fences

# What & Why {#what-and-why}

A wise old Yahoo! Answers comment once described Go as "a language designed for
garbage collection." A bit fatalistic, for sure, but through that same lens
I might describe Dang as "a language designed for occasions where you wish you
didn't have to write it."

Dang is for _glue code_: the code that connects your project to the systems
that build, test, deploy, or manage it. It's the language between "real"
languages. A creature of the backrooms.

Dang strives to make the coder's time spent with it short and sweet.

To that end, let's dive straight in to some code.

```dang
type Hat {
  animal: Animal = null

  insert(animal: Animal!): Hat! {
    self.animal = animal
    self
  }
}

interface Animal {
  coat: Coat!
}

enum Coat { SKIN FUR FEATHERS }

type Rabbit implements Animal {
  name: String!
  coat: Coat! { Coat.FUR }
}

type Dove implements Animal {
  name: String!
  coat: Coat! { Coat.FEATHERS }
}

let jeff = Rabbit("jeffrey")
let hat = Hat # actually Hat() - parens are optional
let withJeff = hat.insert(jeff)
assert("initial hat remains empty") { hat.animal == null }
assert("jeff is in the hat")        { withJeff.animal == jeff }
assert("jeff is furry")             { withJeff.animal.coat == Coat.FUR }

[Dove("molly"), withJeff.animal, Rabbit("richard")].map { _.coat }
```

Here's what you can infer from above:

* Yep -- that's GraphQL, alright.
* But you define the bodies, too; not just the signatures!
* `null` is a thing, but the type system tracks it (`!` is non-null)
* Objects don't mutate. Not even `self`.
* Forward references are fine.


## ...but why?

The initial goal was a native language for [Dagger](https://dagger.io/). Dagger
is a polyglot function engine with an underlying GraphQL API serving as the
common layer where functions written in different languages call one another.

Combining Dang with Dagger gives you a polyglot language with an ecosystem of
modules developed in any language that has a Dagger SDK. Dang is one such
Dagger SDK, so it's perfect for writing Dagger modules that simply glue
together APIs and don't need a heavy full-blown language runtime. As a result
of not needing a codegen phase, it has potential to be much, much faster than
the other SDKs.

Architecturally, Dang is decoupled from Dagger; it just speaks GraphQL, so you
can point it at any API endpoint you want.
