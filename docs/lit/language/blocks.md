\use-plugin{dang}

# Blocks {#blocks}

> Meta: blocks are doing a lot of work in Dang — they're the iteration protocol, the `Ruby`-ish DSL hook, the lambda-equivalent, *and* the body of conditionals/loops. Worth a paragraph naming them explicitly as "the lambda of Dang."

## What a block is

- braces with optional parameter list: `{ x => x + 1 }`
- zero or more parameters separated by commas before `=>` (e.g. `{ item, index => ... }`)
- body is a form sequence separated by newlines or `,`; the last form is the result
- a bare `{ ... }` with no `=>` is also a block expression and evaluates to its last form: `let single = { 42 }` ⇒ `42`

## Block arguments to functions

- a block parameter is declared with the `&` sigil (same operator as [#functions]'s `&fn` refs); its type is a function type:

```dang
# zero-arg block returning Int! (parens omitted)
twice(&body: Int!): Int! {
  body + body
}

# block taking args: &name(params): Ret
myFun(&block(x: Int!): String!): String! {
  block(42)
}
```

- the block param's arg types may also be a type variable: `id(&yield: b): b { yield }`. A type variable is opaque — the body can only pass the value through, not operate on it, so `yield * 2` would be a type error (see [#types])
- a function/constructor may have at most one block parameter
- regular args and a block param can mix; the block param comes last
- callers pass a trailing brace block:

```dang
twice { 21 }                  # ⇒ 42, body takes no args
twice { let n = 21, n }       # multi-statement (separate with newline or `,`)
list.map { x => x * 2 }       # built-in
withArg("Number: ") { x => toJSON(x) }   # args then block
```

- block parameter list can take multiple args:

```dang
list.each { item, index => ... }
```

## Optional parameters

- a block whose body ignores its parameters can omit the `param =>` entirely:

```dang
[1, 2, 3].map { "whee" }        # param ignored ⇒ ["whee", "whee", "whee"]
numbers.filter { true }         # param ignored ⇒ all
numbers.filter { false }        # ⇒ []
```

## Implicit `_` parameter {#implicit-param}

> Meta: Kotlin's `it`, not Scala's positional `_`. The one rule people trip on: every `_` in a param-less block is the *same* one argument.

- a block with **no explicit params** that references `_` gets a single implicit parameter named `_`
- every `_` in that block refers to that same one argument — `{ _ * 2 }` and `{ _ + _ }` both bind exactly one value (NOT Scala-style positional `_1`, `_2`)
- works everywhere blocks are used:

```dang
xs.map { _ * 2 }                # ⇒ each doubled
xs.filter { _ > 0 }             # ⇒ positives
foo.{ bar(_) }                  # dot-block; see [#dot-block]
```

- a block with **explicit params** does not capture `_` — `{ x => x * 2 }` has no implicit parameter
- nesting: `_` binds to the **nearest enclosing param-less block**; a nested param-less block shadows the outer one
- a bare `_` outside any block is an undefined-reference error

## Scoping

- a block is a lexical scope; `let` declares a fresh local. `let` is *the* way
  to declare a local — a bare `name = value` is a reassignment of an existing
  field, not a new declaration (see [#fields])
- inside a block, prefer `let` for locals; bare (public) declarations are for a
  type's or module's exported surface, where "public" actually means something
- a local `let` shadows an outer field of the same name; mutating the local leaves the outer untouched
- reassignment without a shadowing `let` mutates the enclosing field — across nested blocks too
- `+=` works on the outer field from inside a block
- hoisting: a mutation made inside a `for` loop is visible to code after the loop in the same scope

## Control-flow handoff

> Meta: the cute Ruby-esque part. `return` inside a `.map`/`.each` block unwinds the *enclosing function*, not just the block.

- `return` inside a block unwinds through the enclosing **function**, not just the block
- `break value` / `continue value` work inside `.each`, `.map`, `for`, and user-defined block-arg calls
- `break value` becomes the loop/call's result; bare `break` yields `null`
- `continue value` flows into `.map`'s result for that element; bare `continue` yields `null` there (e.g. `[null]`); in `.each`/`for` it just skips to the next iteration
- `break`/`continue` target the *innermost* loop/block call
- an **ordinary nested function** declared inside a block does NOT inherit the block's break/continue target — `break`/`continue` there errors `... outside of loop or block-taking call`
- `break`/`continue`/`return` with no enclosing loop/function error at typecheck: `break outside of loop or block-taking call`, `continue outside of loop or block arg invocation`, `return outside of function`
- escaped blocks (stored via `&block`, then called after the receiving call/function has already returned) error at runtime: `break from expired block call` / `return from expired function`

## When to use a block vs. a function reference

- block: inline code, common case
- `&fn`: store a callable, rebind it, pass it around as data (see [#functions])

## Dot-block application (piping) {#dot-block}

> Meta: this is Dang's piping primitive — there is no `|>` operator. Lead with the equivalence, then the interleaving, then the null behaviour as a consequence of "application, not navigation."

- `receiver.{ block }` calls the block with the receiver as its single argument — Dang's piping mechanism
- `foo.{ bar(_) }` ≡ `bar(foo)`, and `foo.{ x => bar(x) }` ≡ `bar(foo)` (the implicit `_` from [#implicit-param] is the idiomatic form)
- it sits at `.`'s precedence — a sibling of `.{{ }}` selection and method calls — so it **interleaves with real method calls in a single chain**:

```dang
c.{ mountCache(_, path, cache) }
 .withEnvVariable("X", "y")
 .{ prepare(_) }
```

- the block must take **0 or 1 parameters**; 2+ is an error: `dot-block takes a single value`
- a 0-param block ignores the receiver and just returns its body

### Null: dot-block is application, not navigation

> Meta: the null behaviour follows from what dot-block *is*. Frame it that way rather than as a gotcha vs. selection.

- because `foo.{ bar(_) }` ≡ `bar(foo)`, a null receiver is simply *passed in*: the block runs with `_` bound to null, exactly as `bar(null)` would. Dot-block applies a block — it does not navigate into the receiver, so it has nothing to short-circuit
- this is what lets a block *handle* null: `x.{ _ ?? 0 }`, `x.{ if (_ == null) { … } else { … } }`. Want null-safety instead? Express it in the block (`x.{ _?.field }`)
- contrast `.{{ }}` selection ([#graphql]), which *is* navigation and therefore **short-circuits**: `user.{{name}}` is `null` when `user` is null, and its result type is nullable. Same `.`-brace surface, but selection reads fields while dot-block calls a block

## Common methods that take blocks

- the common block-taking collection methods are `.map`, `.filter`, `.each`; see [#collections] for the full set
- conditionals (`if`/`else`) and loops (`for`) use block bodies; see [#control-flow]
