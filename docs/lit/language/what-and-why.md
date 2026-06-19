\use-plugin{dang}
\literate-fences

# What & Why {#what-and-why}

A wise old Yahoo! Answers comment once explained Go to a newcomer as "a
language for doing garbage collection."

A bit fatalistic, perhaps, but through that same lens I might describe Dang as
"a language whose highest form is to be deleted."

## What is Dang?

Dang is a language for **glue**: the code that connects the project you're
creating with the systems that keep it going. It's the language between "real"
languages. A creature of the backrooms.

Knowing its place in the universe, Dang strives to make the coder's experience
as brief and pleasant as possible.

To that end:

* Dang builds on [**GraphQL**](#graphql) to provide a familiar starting point
  across ecosystems.
* It has [**Hindley-Milner-ish**](#types) types, so your janky code fails
  _before_ it tries shipping to prod.
* It has [**immutable data** with **mutable syntax**](#mutation), balancing
  safety with ergonomics.
* It steals fun ideas like [block args](#blocks) from Ruby. (In fact, it steals
  a _lot_ from Ruby.)

{-
* **GraphQL-based** types and functions
* **Prototype-style** object system
* Ruby-style **block arguments** to emphasize chaining
* **Immutable** data behind mutable syntax
* Hindley-Milner-ish[*](#types) **type inference**
-}


## ...but _why_ is Dang?

The initial goal was a native language for [Dagger]. Dagger could be described
as a polyglot function engine that uses GraphQL as the common substrate between
languages.

Most other Dagger SDKs work via code generation. The Go SDK generates bindings
for the GraphQL schema exposed to your Dagger module. Dagger goes to great
lengths to make this process fast, but you can never be faster than _not having
to do the thing_.

Because Dang is native to GraphQL, there is no code generation -- it literally
imports the schema like code. Many Dagger modules are just gluing together
APIs, so they can be written in any language. Dang aims to be the best option
in that scenario.

Architecturally, Dang is decoupled from Dagger; it just speaks GraphQL, so you
can point it at any API endpoint you want.

[Dagger]: https://dagger.io/


## Just put the code in the bag...

For those impatient like me, we'll dive straight in to some code. This may seem
like a lot at once, but I encourage you mess around with it and press the
"Run" button to see what happens.

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

Here are some things that you can infer from above:

* Yep -- that's GraphQL, alright.
* But you implement the fields, too; not just the signatures!
* `null` is a thing, and the type system tracks it (`!` is non-null)
* Required slots become constructor arguments (`Rabbit("jeffrey")`)
* Named arguments can be passed positionally (`Rabbit("jeffrey")` is `Rabbit(name: "jeffrey")`)
* Objects don't mutate -- `insert` returns a modified copy.
* Ruby-style block arguments and method chaining (optional parentheses)


## Acknowledgements

* The many color themes to the left are from
