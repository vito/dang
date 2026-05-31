\use-plugin{dang}

# Control flow {#control-flow}

> Meta: keep `if`, `case`, and `for` close together — they're all expression-form. The "no statements vs. expressions" point is worth stating once at the top.

## Everything is an expression

- `if`, `case`, `for`, `try` all return values
- the value is the last expression in the chosen branch

## `if` / `else`

```dang
pub status = if (active) { "on" } else { "off" }
```

- condition must be `Boolean!` — no truthiness
- `else if` chains
- no-else form returns `null` when the condition is false (result type is nullable)
- branches must be assignable to a common type (union-widened where possible)

## `case`

```dang
case (value) {
  1 => "one"
  2 => "two"
  else => "?"
}
```

### Value patterns

- literal scalars: `1`, `"foo"`, enum values
- literal strings auto-coerce to scalar/enum types matching the operand

### Type patterns

```dang
case (animal) {
  c: Cat => c.purr
  d: Dog => d.bark
}
```

- works on unions, interfaces, and `Error` subtypes inside `catch`
- exhaustiveness: a `case` over a union with no `else` and missing members is an error

### Optional operand

- `case { x > 0 => "+", x < 0 => "-", else => "0" }` — implicit `case (true)`

## `for`

```dang
for (i < 10) { i += 1 }
for { ... break ... }       # infinite
```

- condition expression: loops while truthy
- no `for (x in xs)` — use `xs.each { x => ... }`

## `break` and `continue`

- `break` exits the loop; `break value` makes the loop yield `value`
- `continue` skips to the next iteration; `continue value` yields into `.map` results
- target the nearest enclosing loop only

## `return`

- exits the enclosing function early
- value type must satisfy the function's declared return type
- inside a block, unwinds the enclosing function (see [blocks](./blocks.md))

## `try` / `catch` / `raise`

- see [errors](./errors.md)
