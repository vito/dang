\use-plugin{dang}
\literate-fences

# Blocks {#blocks}

> Meta: blocks are doing a lot of work in Dang — they're the iteration protocol, the `Ruby`-ish DSL hook, the lambda-equivalent, *and* the body of conditionals/loops. Worth a paragraph naming them explicitly as "the lambda of Dang."

Blocks are the lambda of Dang. There is no separate closure literal or arrow
function: a brace block is how code gets passed around, and the same form is
the iteration protocol, the hook for Ruby-ish DSLs, and the body of every
conditional and loop.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser.

## What a block is

A block is braces around a sequence of forms, separated by newlines or `,`;
the last form is the block's result. A bare `{ ... }` with no parameters is
itself an expression:

```dang
{ let width = 4, let height = 3, width * height }
```

Parameters, when a block takes them, are comma-separated names before a `=>`:

```dang
[1, 2, 3].map { x => x + 1 }
```

## Block arguments to functions

A function declares a block parameter with the `&` sigil (the same operator
as [#functions]'s `&fn` refs); its type is a function type. The caller passes
the block as trailing braces after the call:

```dang
twice(&body: Int!): Int! {
  body + body
}

twice { 21 }
```

The block parameter can itself take arguments — declared `&name(params): Ret`
— and the function body calls it like any function:

```dang
apply(&block(x: Int!): String!): String! {
  block(42)
}

apply { x => `got ${x}` }
```

The block param's arg types may also be a type variable. A type variable is
opaque — the body can only pass the value through, not operate on it, so
`yield * 2` here would be a type error (see [#nullability]):

```dang
id(&yield: b): b { yield }

id { "anything" }
```

Regular args and a block param can mix; the block param comes last, and a
function or constructor may have at most one:

```dang
greet(name: String!, &style(s: String!): String!): String! {
  style(`Hello, ${name}`)
}

greet("Dang") { s => s.toUpper }
```

A block's parameter list can take multiple args when the call supplies them —
`.each` passes the element and its index:

```dang
let fruits = ["apple", "banana", "cherry"]

fruits.each { item, index => print(`${index}: ${item}`) }
```

(`.each` returns the original list — that's the `=>` line above — so calls
chain even when the block is pure side effect.)

## Optional parameters

A block whose body ignores its parameters can omit the `param =>` entirely:

```dang
[1, 2, 3].map { "whee" }
```

That works anywhere a block does — a constant predicate, say:

```dang
fruits.filter { true }     # param ignored ⇒ keeps everything
fruits.filter { false }
```

## Implicit `_` parameter {#implicit-param}

> Meta: Kotlin's `it`, not Scala's positional `_`. The one rule people trip on: every `_` in a param-less block is the *same* one argument.

A block with **no explicit params** that references `_` gets a single
implicit parameter named `_`:

```dang
[1, 2, 3].map { _ * 2 }
```

Every `_` in that block refers to that same one argument — Kotlin's `it`, NOT
Scala-style positional `_1`, `_2`. So `{ _ + _ }` doubles each element rather
than pairing two arguments:

```dang
[1, 2, 3].map { _ + _ }
```

`_` binds to the **nearest enclosing param-less block**, and a nested
param-less block shadows the outer one — here the outer `_` is each sublist,
the inner `_` each number:

```dang
[[1, 2], [3]].map { _.map { _ * 10 } }
```

A block with **explicit params** does not capture `_` — `{ x => x * 2 }` has
no implicit parameter — and a bare `_` outside any block is an
undefined-reference error.

## Scoping

A block is a lexical scope, and `let` is *the* way to declare a local. A
local `let` shadows an outer field of the same name; mutating the local
leaves the outer untouched:

```dang
let name = "outer"
let result = { let name = "inner", name }

[result, name]
```

Without a shadowing `let`, a bare `name = value` is a *reassignment* of the
existing field, not a new declaration (see [#fields]) — and that reach
extends through nested blocks, with mutations still visible after the block
returns. `+=` works on the outer field the same way:

```dang
let total = 0
[1, 2, 3].each { x => total += x }

total
```

The same hoisting applies to `loop` bodies:

```dang
let tries = 0
loop {
  tries += 1
  if (tries == 3) { break }
}

tries
```

Inside a block, prefer `let` for locals; bare (public) declarations are for a
type's or module's exported surface, where "public" actually means something.

## Control-flow handoff

> Meta: the cute Ruby-esque part. `return` inside a `.map`/`.each` block unwinds the *enclosing function*, not just the block. The full `break`/`continue` spec lives in [#control-flow]; keep only the block-specific wrinkles here.

`return` inside a block unwinds through the enclosing **function**, not just
the block — which is what makes early exit from an iteration read naturally:

```dang
firstMatch(words: [String!]!, prefix: String!): String {
  words.each { w => if (w.hasPrefix(prefix)) { return w } }
  null
}

firstMatch(fruits, "b")
```

`break value` works inside `.each`, `.map`, `loop`, and user-defined
block-arg calls — a block-taking call is a valid target, and `break` makes it
yield the value:

```dang
[1, 2, 3, 4].each { x => if (x > 2) { break x } }
```

`continue value` supplies one iteration's result — in `.map` it inserts the
value; in `.each` it just advances:

```dang
[1, 2, 3].map { x => if (x == 2) { continue 0 }, x }
```

The value/result rules are specified in [#control-flow]. Two block-specific
wrinkles:

- an **ordinary nested function** declared inside a block does NOT inherit
  the block's break/continue target — `break`/`continue` there errors
  `... outside of loop or block-taking call`
- escaped blocks (stored via `&block`, then called after the receiving
  call/function has already returned) error at runtime:
  `break from expired block call` / `return from expired function`

## When to use a block vs. a function reference

- block: inline code, common case
- `&fn`: store a callable, rebind it, pass it around as data (see [#functions])

## Dot-block application (piping) {#dot-block}

> Meta: this is Dang's piping primitive — there is no `|>` operator. Lead with the equivalence, then the interleaving, then the null behaviour as a consequence of "application, not navigation."

`receiver.{ block }` calls the block with the receiver as its single argument
— Dang's piping mechanism; there is no `|>` operator. `foo.{ bar(_) }` ≡
`bar(foo)`, and `foo.{ x => bar(x) }` ≡ `bar(foo)` (the implicit `_` from
[#implicit-param] is the idiomatic form):

```dang
inc(x: Int!): Int! { x + 1 }

41.{ inc(_) }
```

It sits at `.`'s precedence — a sibling of `.{{ }}` selection and method
calls — so it **interleaves with real method calls in a single chain**:

```dang
emphasize(s: String!): String! { `**${s}**` }

"hello world"
  .toUpper
  .{ emphasize(_) }
  .replace(" ", ", ")
```

The block must take **0 or 1 parameters**; 2+ is an error: `dot-block takes a
single value`. A 0-param block ignores the receiver and just returns its
body:

```dang
5.{ "ignored the receiver" }
```

### Null: dot-block is application, not navigation

> Meta: the null behaviour follows from what dot-block *is*. Frame it that way rather than as a gotcha vs. selection.

Because `foo.{ bar(_) }` ≡ `bar(foo)`, a null receiver is simply *passed in*:
the block runs with `_` bound to null, exactly as `bar(null)` would.
Dot-block applies a block — it does not navigate into the receiver, so it has
nothing to short-circuit. This is what lets a block *handle* null:

```dang
let missing = null :: String

missing.{ _ ?? "fallback" }
```

Contrast `.{{ }}` selection ([#interop]), which *is* navigation and therefore
**short-circuits**: the selection below never reads `name`, and its result
type is nullable. Same `.`-brace surface, but selection reads fields while
dot-block calls a block:

```dang
type User { name: String! }
let nobody = null :: User

nobody.{{name}}
```

## Common methods that take blocks

- the common block-taking collection methods are `.map`, `.filter`, `.each`; see [#collections] for the full set
- `if`/`else` branches are plain expressions — a braced branch is just a block grouping several expressions into one, not part of the `if` syntax; `loop` takes a block; see [#control-flow]
