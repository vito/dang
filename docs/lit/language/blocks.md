\use-plugin{dang}

# Blocks {#blocks}

> Meta: blocks are doing a lot of work in Dang — they're the iteration protocol, the `Ruby`-ish DSL hook, the lambda-equivalent, *and* the body of conditionals/loops. Worth a paragraph naming them explicitly as "the lambda of Dang."

## What a block is

- braces with optional parameter list: `{ x => x + 1 }`
- zero or more parameters separated by commas before `=>` (e.g. `{ item, index => ... }`)
- body is a form sequence separated by newlines or `,`; the last form is the result
- a bare `{ ... }` with no `=>` is also a block expression and evaluates to its last form: `pub single = { 42 }` ⇒ `42`

## Block arguments to functions

- a block parameter is declared with the `&` sigil (same operator as [#functions]'s `&fn` refs); its type is a function type:

```dang
# zero-arg block returning Int! (parens omitted)
pub twice(&body: Int!): Int! {
  body + body
}

# block taking args: &name(params): Ret
pub myFun(&block(x: Int!): String!): String! {
  block(42)
}
```

- the block param's arg types may also be a type variable: `pub do(&yield: b): b { yield * 2 }`
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

## Scoping

- a block is a lexical scope; `let` declares a fresh local
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

## Common methods that take blocks

- the common block-taking collection methods are `.map`, `.filter`, `.each`; see [#collections] for the full set
- conditionals (`if`/`else`) and loops (`for`) use block bodies; see [#control-flow]
