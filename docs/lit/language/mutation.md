\use-plugin{dang}

# Mutation and copy-on-write {#mutation}

> Meta: this is the page that prevents a *lot* of confusion. The thesis: methods look mutating but return a forked copy. Show the canonical `Foo(42).incr.a == 43` example up front.

## Values are immutable

- a value, once constructed, never changes
- "mutation" inside a method creates a forked copy of the receiver

## The classic example

```dang
type Foo {
  pub a: Int!
  pub incr: Foo! {
    a += 1
    self
  }
}

Foo(42).incr.a == 43
```

- each `.incr` allocates a fresh `Foo` with `a + 1`
- the original `Foo(42)` is untouched

## What `self.field = value` actually does

- forks the current receiver
- substitutes the new field value
- subsequent `self` references in the same method see the forked version
- the forked instance is what the method returns (typically `self`)

## Fork-per-call semantics

```dang
let c1 = Counter(0)
let c2 = c1.incr     # c2.value == 1
let c3 = c1.incr     # c3.value == 1, c1.value still 0
```

- two calls on the same receiver don't compound

## Within a method, mutations accumulate inside one fork

```dang
pub addAll(items: [String!]!): Self! {
  items.each { item => self.items += [item] }
  self
}
```

- the loop builds up a single forked value, then returns it

## Nested field assignment

- `self.a.b.c = x` clones every link on the path from root to leaf
- supported but expensive — avoid deep nesting if you can

## Bare reassignment vs. field mutation

- `x = value` — rebinds the local/arg `x`
- `self.x = value` — forks `self`, sets the field
- inside a constructor arg-shadowed scope, this distinction matters

> Meta: a diagram (boxes-and-arrows) would help a lot here. Even ASCII would do.

## When not to think in CoW

- pure functions (`fn(x): T { ... }`) — no `self`, no forking
- top-level `pub`/`let` fields — plain bindings, no `self` to fork
- the *values themselves* are immutable regardless
