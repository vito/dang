# Control Flow and Errors

## Everything is an expression
`if`, `case`, `loop`, `try` all return values (the last expression in the chosen branch), so they're assignable, returnable, and usable as args.

## `if` / `else`
```dang
let status = if (active) "on" else "off"
```
- Branches are plain expressions; braces aren't part of the `if` syntax. `if (x) { a } else { b }` is just `if`/`else` applied to two blocks — use a block when a branch needs multiple expressions.
- Condition must be `Boolean!` — **no truthiness**. A non-`Boolean` condition is a compile error (`condition must be Boolean, got Int!`).
- `else if` chains are just `else` whose branch is another `if`: `if (a) {..} else if (b) {..} else {..}`.
- No-else form returns `null` when the condition is false — result type is **nullable** (`if (false) { "value" }` is `null`). `else if` chains with no final `else` are likewise nullable.
- Branches must merge to a common type; divergent branches widen to a union (see types.md flow-typing).
- Flow-sensitive narrowings from the condition apply per-branch (then=truthy, else=falsy).
- A no-else `if` whose then-branch `return`s does NOT make an enclosing fn non-null (the branch may be skipped → result stays nullable).

## `case`
```dang
case (value) {
  1 => "one"
  2 => "two"
  else => "?"
}
```
- Clauses tried top-to-bottom; first match wins (including a stray duplicate or an `else` placed before later clauses).
- Clause bodies must merge to a common type (`Case.Infer: clause N type mismatch` otherwise).
- **No compile-time exhaustiveness check**: if nothing matches and there's no `else`, it's a runtime error `no case clause matched the value: <v>`.

### Value patterns
- Literal scalars: ints `1`, floats `3.14`, strings `"foo"`, booleans, `null`, enum values.
- Literal strings/values auto-coerce to the operand's scalar/enum type (operand `URL!`/enum, clause `"https://..."`/`"ACTIVE"`) — same coercion as field/arg/return boundaries.
- Coercion only applies to syntactic literals; a non-literal value of a different type is a mismatch (`clause N value type mismatch`).

### Type patterns
```dang
case (animal) {
  c: Cat => c.purr
  d: Dog => d.bark
}
```
- Form `binding: Type => expr`; the binding is the operand narrowed to `Type` inside the clause.
- Operand must be a **union or interface**; on a plain object → `type pattern requires a union or interface operand`.
- The named type must be a member of the union / an implementer of the interface, else `type X is not a member of union Y`.
- An interface itself works as a pattern (matches any implementer — a typed catch-all); place specific types before it.
- Also used in `catch` over `Error` subtypes.

### Optional operand
- `case { x > 0 => "+", x < 0 => "-", else => "0" }` — omitting the operand desugars to `case (true)`; clauses are `Boolean!` conditions.

## `loop` (builtin)
```dang
let answer = loop { break 42 }   # block-taking builtin; runs until break
```
- `loop { ... }` is Dang's only loop — a stdlib builtin, not a keyword. It calls its block repeatedly forever; exit via `break`/`return`/`raise`, and `continue` advances to the next iteration.
- There is **no `for` or `while` keyword**; iterate collections with `xs.each { x => ... }` (see stdlib.md) and write conditional loops with a mid-body guard: `loop { if (!cond) { break } ... }`.
- An expression: yields the `break` value (`null` for a bare `break`). The result is **non-null** when every `break` carries a non-null value: `loop { break 42 }` is usable directly as `Int!`.

## `break` / `continue`
- Valid only inside a loop or block-taking call; otherwise compile error (`break outside of loop or block-taking call` / `continue outside of loop or block arg invocation`).
- `break` exits; `break value` makes the loop/block-call yield `value`; bare `break` yields `null`.
- `continue` skips to the next iteration; in `.map`, `continue value` inserts `value` into the result (bare → `null`); in `.each`/`loop` it just advances.
- Target the nearest enclosing loop/block-call only; an ordinary nested function does NOT inherit the target.

## `return`
- Exits the enclosing function/method/constructor early; outside one → `return outside of function`.
- Value type must satisfy the declared return type.
- Unwinds through enclosing blocks and loops (a `return` from inside `.each` exits the whole fn).
- A `return` in a skippable branch (e.g. a no-else `if`) does not make the fn non-null.
- `return` is **NOT an error** and is **NOT catchable** by `try`/`catch`.

## Errors: `try`, `catch`, `raise`

Dang uses errors for *errors* — recoverable failures across boundaries — **not** for `null` or expected branches.

### Raising
```dang
raise "something went wrong"
raise NotFoundError(message: "user gone", resource: "User")
```
- `raise` takes a `String!` or an `Error!`; anything else → `raise requires a String! or Error!, got Int!`.
- A string raise wraps it in a built-in `BasicError` (implements `Error`, `message` = the string). A value implementing `Error` raises as-is.
- `raise` is itself an expression of any type (fresh type var), so it fits in any branch (e.g. a `case`/`catch` arm) without breaking the merged result type.

### The `Error` interface
```dang
interface Error { message: String! }
```
- A real prelude interface; `BasicError` is the built-in implementer for string raises.
- User error types `implements Error`, which forces `message: String!`. Additional fields (`resource`, `field`, `code`) are preserved on the raised value and matchable in `catch`.

### Catching
```dang
try {
  validate(name)
} catch {
  v: ValidationError => v.field
  n: NotFoundError => n.resource
  e: Error => e.message     # interface pattern = typed catch-all
  err => err.message        # bare catch-all, err: Error!
}
```
- The whole `try`/`catch` is one expression — assignable, returnable, nestable.
- The body's success value passes through unchanged when nothing is raised.
- Body and every catch clause must merge to one type (`cannot use String! as Int!` otherwise).
- Catches errors raised anywhere in the body, including ones propagated from called functions and **runtime errors** (null access, `1 / 0` → `division by zero`, GraphQL failures).
- Catch clauses are `case` patterns limited to **type patterns** and a **catch-all** (no value patterns). First match wins. Pattern types must implement `Error`. The bare catch-all binds the error as `Error!`. The `Error` interface itself matches any error; place specific types first.

### Propagation
- Uncaught errors unwind through enclosing calls; a `raise` with no enclosing `catch` terminates the program (`uncaught error: <message>`).
- `return` cannot be caught — `try { return x } catch {..}` still returns `x`.
- Re-raise inside a catch with `raise err` (or a new error); it propagates to the next enclosing `catch`.

### When to raise vs. return null
| situation | use |
|---|---|
| absence is a normal, expected outcome | return `null` (nullable type) |
| caller routinely branches on the result | return `null` / a result value |
| continuing would produce wrong results | `raise` |
| failure crosses a boundary (validation, HTTP/GraphQL, contract) | `raise` |

Anti-patterns: `raise` for early exit (use `return`); `raise` to signal expected absence (return `null`); catching `Error` just to ignore it.
