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
- verified: test_class_desired_behavior.dang / test_class_mutation.dang assert `Foo(42).incr.a == 43`; `let original = Foo(10); original.incr` leaves `original.a == 10`
- note `a += 1` here mutates the *field* with no `self.` prefix (bare field write still forks); `self.a += 1` is the equivalent explicit form (test_class_immutability.dang)

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

- two calls on the same receiver don't compound — each call forks from the original receiver, not from the previous call's result (test_copy_on_write.dang: 5x `withAlpine(branch).withPackages(packages)` all equal, not accumulated; test_method_loop_mutation.dang `s1`/`s2` independent)
- holds for recursion (test_cow_recursive.dang: `incrementN(2)` twice both give `count == 2`) and across module boundaries (test_cow_cross_module.dang, test_cow_external.dang, test_cow_indirect_module.dang)

## Within a method, mutations accumulate inside one fork

```dang
pub addAll(source: [String!]!): Builder! {
  source.each { item => self.items += [item] }
  self
}
```

- the loop builds up a single forked value, then returns it (test_method_loop_mutation.dang, test_constructor_loop_mutation.dang)
- the return type is the concrete type name (`Builder!`), not `Self!` — there is no `Self` type keyword in the language (grep: tests never use `Self!`; grammar only defines lowercase `self`)

## Nested field assignment

- `self.a.b.c = x` (or bare `data.a.b.c = x`) clones every link on the path from root to leaf, leaving the original tree untouched (test_nested_field_assignment.dang: `original.data.a.b.c` stays `42` while copies diverge)
- compound forms work too: `data.a.b.c += 10` (test_nested_field_assignment.dang `incrementNested`)
- supported but expensive — avoid deep nesting if you can

## Bare reassignment vs. field mutation

- name resolution at the write site decides the target:
  - if `x` is a local/arg in scope → `x = value` rebinds it, no fork (test_constructor_arg_mutation.dang: mutating arg `foo` does not touch `self.foo`)
  - if `x` is a field (not shadowed) → bare `x = value` / `x += 1` forks `self` and sets the field (test_class_desired_behavior.dang)
- `self.x = value` — always forks `self`, sets the field; required only to disambiguate when a same-named local/arg shadows the field (test_constructor_arg_scope.dang)
- inside a constructor with a field-shadowing arg, this distinction matters most

> Meta: a diagram (boxes-and-arrows) would help a lot here. Even ASCII would do. (still TBD)

## When not to think in CoW

- pure functions — no `self`, no forking
- top-level `pub`/`let` bindings — plain bindings, no `self` to fork (see [#blocks])
- the *values themselves* are immutable regardless
- see [#objects] for `type`/`self`/constructor mechanics that this page builds on
