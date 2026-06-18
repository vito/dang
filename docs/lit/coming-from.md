\use-plugin{dang}

# Coming from another language {#coming-from}

> Meta: a Rosetta page — map what the reader already knows onto Dang, and flag
> the one or two things that will actually trip them up. Each section is "what
> carries over" then "what's different," kept short. Link to the chapter that
> owns each topic rather than re-teaching it.

Dang borrows on purpose: blocks from Ruby, null tracking and smart-casts from
Kotlin, expression-orientation from Rust, directory modules from Go, and its
whole type system from GraphQL. If you know one of those, most of Dang is "yes,
like that." This page maps your existing instincts onto Dang and points out the
few places they'll mislead you.

One thing to internalize no matter where you're coming from: **a bare type is
nullable, and `!` makes it non-null** — `String` may be null, `String!` cannot.
That's GraphQL's convention, and it's *inverted* from Kotlin/Swift/TypeScript,
where the bare type is non-null and a sigil marks nullability. See
[#nullability].

\table-of-contents

## Coming from Go {#from-go}

Dang shares Go's "boring on purpose" instincts: one canonical format (`dang fmt`,
like `gofmt`), and the conviction that all code should look the same.

**Feels like home**

- **A directory is a package/module.** Every `.dang` file in a directory shares
  one scope — declare a type in one file, use it in another, no import. Files
  load in unspecified order, so write order-independent code. ([#modules])
- **Forward references just work.** Declarations are hoisted; order within a
  file or directory doesn't matter.
- **No inheritance.** Behavior is shared through interfaces (`implements`), not a
  class hierarchy — much like Go's interfaces. ([#interfaces-unions])
- **Errors are values.** No exceptions-as-control-flow culture; you return
  `null` for expected absence and `raise`/`catch` only for real failures.
  ([#errors])

**What's different**

- **Capitalization names *types*, not visibility.** `User` vs `user`
  distinguishes a type from a value; it does *not* control export. Visibility is
  `let` (private) vs. a typed declaration (public). ([#fields])
- **Nullability is in the type system.** There's no zero-value-and-pray; `T` and
  `T!` are different types, with flow narrowing instead of `if x != nil`.
  ([#flow-typing])
- **Everything is an expression** — no statements. `if`, `case`, and `loop` all
  produce values, and the last expression of a body is its result (no `return`
  for the normal case). ([#control-flow])
- **No `for`/`while`.** Iterate with `.each`/`.map`; the bare `loop { ... }`
  builtin covers the rest. ([#blocks])
- **Values are immutable.** A method that looks mutating returns a forked copy —
  no pointers, no shared mutation. ([#mutation])

## Coming from Ruby {#from-ruby}

Dang's blocks *are* Ruby's blocks, down to the trailing-brace call syntax and
the `.map`/`.each`/`.filter` vocabulary. If you like writing Ruby, you'll like
writing Dang.

**Feels like home**

- **Blocks everywhere.** `[1, 2, 3].map { x => x * 2 }`, `list.each { ... }`,
  trailing block after the call. They're the iteration protocol and the
  lambda. ([#blocks])
- **Optional parens on calls.** A zero-arg method reads like a property —
  `user.name`, `counter.incr` — exactly like Ruby attribute calls.
  ([#functions])
- **Implicit return.** The last expression is the value; no `return` keyword for
  the normal result.
- **Everything's an expression**, and `loop { ... }` is right there.

**What's different**

- **Static types and null tracking.** The big add — the checker catches the
  `NoMethodError`-on-`nil` class of bug before you run. ([#nullability])
- **No truthiness.** `if`/`and`/`or` require a real `Boolean!`; there's no
  "everything but `nil`/`false` is truthy." ([#control-flow])
- **Immutable values.** Ruby objects mutate in place; Dang forks a copy instead
  — `counter.incr` leaves the original at its old value. ([#mutation])
- **Block params use `=>`, and `_` is Kotlin's `it`.** It's `{ x => x + 1 }`,
  not `{ |x| x + 1 }`; and a bare `_` is the single implicit argument, the same
  one everywhere in the block. ([#implicit-param])
- **No metaprogramming.** No monkey-patching, no `method_missing`, no macros —
  blocks cover the cases those usually would. Classes become prototype `type`s
  with no open inheritance. ([#objects])

## Coming from Kotlin {#from-kotlin}

Kotlin is the closest match for Dang's null handling and pattern matching — the
mental model transfers almost directly, with one sigil flip.

**Feels like home**

- **Null tracking with smart casts.** Check once and the type narrows:
  `if (x == null) return; x.use` leaves `x` non-null afterward — Kotlin's smart
  casts by another name. ([#flow-typing])
- **`when` → `case`.** Value patterns, type patterns that bind-and-narrow, and an
  operand-less form that's a condition chain. ([#control-flow])
- **`it` → `_`.** A param-less block referencing `_` gets one implicit argument.
  ([#implicit-param])
- **Trailing lambdas, named and default arguments**, and `if`/`when` as
  expressions — all present. ([#functions])
- **Data classes ≈ `type` + copy.** Immutable value objects whose "mutations"
  produce a modified copy, like `.copy(...)`. ([#mutation])

**What's different**

- **The nullability sigil is inverted.** Kotlin: `String` non-null, `String?`
  nullable. Dang: `String` *nullable*, `String!` non-null. Bare means "might be
  null" here. ([#nullability])
- **Immutability is the only mode.** No `var`/`val` split — every value is
  immutable and methods fork copies. ([#mutation])
- **Types come from a schema.** You don't write most type declarations; you
  `import` a GraphQL schema and they appear. ([#interop])
- **Zero-arg functions auto-call.** No `()` needed (and no distinction between a
  property and a zero-arg method); use `&fn` to get the function itself.
  ([#functions])
- **No classes or inheritance** — prototype `type`s and interfaces only.
  ([#objects], [#interfaces-unions])

## Coming from Rust {#from-rust}

Dang is expression-oriented in the same way Rust is, and pattern matching feels
familiar — without the ownership system, since immutability plus copying
replaces borrowing.

**Feels like home**

- **Last expression is the value.** No `return` for the normal result; `return`
  is early-exit only. ([#functions])
- **`match` → `case`.** First-match-wins, value and type patterns, exhaustive
  matching over a closed union needs no catch-all and stays non-null.
  ([#control-flow])
- **Immutable by default**, and `if`/`case`/`loop` are all expressions you can
  bind or return. ([#mutation], [#control-flow])
- **Sum types.** `union` is the closed set of variants; `enum` is the closed set
  of constants — and you take them apart with `case`. ([#interfaces-unions])

**What's different**

- **No ownership or borrowing.** Values are immutable and "mutation" clones; you
  never think about lifetimes, moves, or `&mut`. It's a scripting language —
  expressiveness over performance. ([#mutation])
- **Nullability replaces `Option<T>`.** `T` is the nullable form, `T!` the
  non-null one, with `??` for defaults and flow narrowing instead of
  `match`/`if let` on `Some`/`None`. ([#nullability], [#flow-typing])
- **Errors aren't `Result<T, E>`.** They `raise` and unwind to a `try`/`catch`;
  there's no `?` operator because uncaught errors propagate automatically.
  ([#errors])
- **Closures are blocks** — `{ x => ... }`, passed as trailing braces, not
  `|x| { ... }`. ([#blocks])
- **Types come from a GraphQL schema**, not `struct`/`enum`/`trait`
  declarations. ([#interop])

## Coming from Swift {#from-swift}

Swift programmers will recognize optionals, guard-style narrowing, trailing
closures, and named arguments — with the same sigil flip Kotlin folks hit.

**Feels like home**

- **Optionals and narrowing.** A bare `T` is the optional; `guard`/`if let`
  habits map onto flow narrowing (`if (x == null) { return }` then use `x`), and
  `??` is the same nil-coalescing you know. ([#flow-typing], [#operators])
- **Trailing closures and named arguments**, including defaults. ([#functions],
  [#blocks])
- **Value semantics.** Like Swift structs, values are copied rather than shared;
  a "mutating" method yields a new value. ([#mutation])
- **`enum` and `switch` → `enum`/`union` and `case`**, with binding patterns.
  ([#interfaces-unions], [#control-flow])

**What's different**

- **The optional sigil is inverted and postfix on the type.** Swift: `String`
  non-null, `String?` optional. Dang: `String` nullable, `String!` non-null. And
  postfix `!` on a *value* is the force-unwrap (raises if actually null).
  ([#nullability], [#operators])
- **No classes/struct split, no protocols-with-generics.** Just prototype
  `type`s and GraphQL interfaces. ([#objects], [#interfaces-unions])
- **Types come from a schema**, and querying GraphQL is the language's reason for
  being. ([#interop])
- **No `for`/`while`** — `.each`/`.map` and `loop`. ([#blocks])

## Also familiar {#also-familiar}

Shorter notes for a few more starting points:

- **GraphQL.** You already know Dang's type system — it *is* GraphQL's:
  nullability with `!`, objects, interfaces, unions, enums, scalars, and input
  types, all 1:1. The novelty is that the schema becomes a programming language,
  with multi-field selection (`user.{{ name, posts.{{ title }} }}`) compiling to
  a single query. ([#interop], [#types])
- **TypeScript.** Structural records (`{{ ... }}`) echo object types, and
  `strictNullChecks` is the right instinct — but here null-checking is always on
  and the sigil is flipped (`T` nullable, `T!` non-null). Union types and
  narrowing carry over; there's no `any`, no structural-vs-nominal escape
  hatches, and values are immutable. ([#nullability], [#flow-typing])
- **ML / Elm / Haskell.** Hindley–Milner inference means annotations are usually
  optional; immutability, expression-orientation (no statements), and
  `case`/pattern matching over unions will all feel native. The differences:
  effects and GraphQL calls aren't tracked in the types, errors use
  `raise`/`catch` rather than a result type, and objects are prototype-based
  rather than records-plus-typeclasses. ([#control-flow], [#errors])

Whichever you're coming from, the fast tour of the whole language is
[#twenty-minutes]; the per-topic chapters follow it.
