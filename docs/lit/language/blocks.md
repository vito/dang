\use-plugin{dang}

# Blocks {#blocks}

> Meta: blocks are doing a lot of work in Dang — they're the iteration protocol, the `Ruby`-ish DSL hook, the lambda-equivalent, *and* the body of conditionals/loops. Worth a paragraph naming them explicitly as "the lambda of Dang."

## What a block is

- braces with optional parameter list: `{ x => x + 1 }`
- zero or more parameters separated by commas before `=>`
- body is an expression sequence; last expression is the result

## Block arguments to functions

- a function can declare a block parameter:

```dang
pub twice(&body: Int!): Int! {
  body + body
}
```

- callers pass a trailing brace block:

```dang
twice { 21 }                  # ⇒ 42
twice { let n = 21; n }       # multi-statement
list.map { x => x * 2 }       # built-in
```

- block parameter list can take multiple args:

```dang
list.each { item, index => ... }
```

## Optional parameters

- a block whose body ignores its parameters can omit the `param =>`:

```dang
shouldRun { true }    # block has signature (x: Boolean!): Boolean!
```

## Scoping

- a block is a lexical scope; `let` is local
- reassignment without `let` mutates the enclosing field
- hoisting: mutations inside `for` propagate to subsequent code in the same scope

## Control-flow handoff

> Meta: this is the cute Ruby-esque part — worth its own subsection with a small example showing `return` from inside a `map` block unwinding through the enclosing function.

- `return` inside a block unwinds through the enclosing **function**, not just the block
- `break value` / `continue value` work inside `.each`, `for`, and user-defined block-arg loops
- `break` value becomes the loop's result; `continue` value flows into `.map` result
- escaped blocks (stored and called later, outside the original control-flow scope) raise an error if they try to `break`/`return`

## When to use a block vs. a function reference

- block: inline code, common case
- `&fn`: store a callable, rebind it, pass it around as data

## Common methods that take blocks

- `.map`, `.filter`, `.reject`, `.reduce`, `.each`
- `.any`, `.all`, `.takeWhile`, `.dropWhile`
- conditionals/loops desugar through block bodies
