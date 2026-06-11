\use-plugin{dang}

# Control flow {#control-flow}

> Meta: keep `if`, `case`, and `loop` close together — they're all expression-form. The "no statements vs. expressions" point is worth stating once at the top.

## Everything is an expression

- `if`, `case`, `loop`, `try` all return values
- the value is the last expression in the chosen branch
- so they're assignable, returnable, usable as args (see [#blocks])

## `if` / `else`

```dang
status: String! = if (active) { "on" } else { "off" }
```

- condition must be `Boolean!` — no truthiness; a non-`Boolean` condition is a compile error (`condition must be Boolean, got Int!`)
- `else if` chains: `if (a) {..} else if (b) {..} else {..}`
- no-else form returns `null` when the condition is false — result type is nullable (e.g. `if (false) { "value" }` is `null`); `else if` chains with no final `else` are likewise nullable
- branches must merge to a common type; if they diverge they widen to a union (see [#flow-typing])
- flow-sensitive narrowings from the condition apply per-branch (then=truthy, else=falsy) — see [#flow-typing]
- a no-else `if` whose then-branch `return`s does NOT make an enclosing fn non-null (the branch may be skipped → result stays nullable)

## `case`

```dang
case (value) {
  1 => "one"
  2 => "two"
  else => "?"
}
```

- clauses tried top-to-bottom; first match wins (incl. a stray duplicate or an `else` placed before later clauses)
- clause bodies must merge to a common type (`Case.Infer: clause N type mismatch` otherwise)
- no compile-time exhaustiveness check: if nothing matches and there's no `else`, it's a runtime error `no case clause matched the value: <v>`

### Value patterns

- literal scalars: ints `1`, floats `3.14`, strings `"foo"`, booleans `true`/`false`, `null`, enum values
- literal strings/values auto-coerce to the operand's scalar/enum type (e.g. operand `URL!`/an enum, clause `"https://..."`/`"ACTIVE"`) — same coercion as field/arg/return boundaries (see [#enums-scalars])
- coercion only applies to syntactic literals; a non-literal value whose type differs from the operand is a mismatch (`clause N value type mismatch`)

### Type patterns

```dang
case (animal) {
  c: Cat => c.purr
  d: Dog => d.bark
}
```

- form is `binding: Type => expr`; the binding is the operand narrowed to `Type` inside that clause
- operand must be a union or interface (see [#interfaces-unions]); on a plain object it's an error (`type pattern requires a union or interface operand`)
- the named type must be a member of the union / an implementer of the interface, else `type X is not a member of union Y`
- an interface itself works as a pattern — matches any implementer (a typed catch-all); place specific types before it
- also used in `catch` over `Error` subtypes (see [#errors])

### Optional operand

- `case { x > 0 => "+", x < 0 => "-", else => "0" }` — omitting the operand desugars to `case (true)`; clauses are `Boolean!` conditions

## `loop`

```dang
let answer = loop { break 42 }   # block-taking builtin; runs until break
```

- `loop { ... }` is Dang's only loop — a stdlib builtin (see [#stdlib]), not a keyword: it calls its block repeatedly forever
- there is no `for` or `while`; iterate collections with `xs.each { x => ... }` (see [#collections]), and write conditional loops with a mid-body guard: `loop { if (!cond) { break } ... }`
- exit via `break` / `return` / `raise`; `continue` advances to the next iteration
- an expression: yields the `break` value (`null` for a bare `break`); the result is non-null when every `break` carries a non-null value, so `loop { break 42 }` is usable directly as `Int!`

## `break` and `continue`

- valid only inside a loop or a block-taking call; otherwise compile error (`break outside of loop or block-taking call` / `continue outside of loop or block arg invocation`)
- `break` exits; `break value` makes the loop / block-call yield `value`; bare `break` yields `null`
- `continue` skips to the next iteration; in `.map`, `continue value` inserts `value` into the result (bare `continue` inserts `null`); in `.each` / `loop` it just advances
- target the nearest enclosing loop or block-call only; an ordinary nested function does NOT inherit the enclosing block's break/continue target (compile error if it tries)

## `return`

- exits the enclosing function / method / constructor early; outside one is a compile error (`return outside of function`)
- value type must satisfy the function's declared return type
- unwinds through enclosing blocks and loops (e.g. `return` from inside `.each` exits the whole fn; see [#blocks])
- a `return` in a skippable branch (e.g. a no-else `if`) does not make the fn non-null
- `return` is NOT an error and is NOT catchable by `try`/`catch` (see [#errors])

## `try` / `catch` / `raise`

- see [#errors]
