\use-plugin{dang}

# Errors: `try`, `catch`, `raise` {#errors}

> Meta: positioning matters: errors are not for control flow. Open with "Dang uses errors for *errors* — recoverable failures across boundaries — not for `null` or expected branches."

Dang uses errors for *errors* — recoverable failures across boundaries — not for `null` or expected branches.

## Raising

```dang
raise "something went wrong"
raise NotFoundError(message: "user gone", resource: "User")
```

- `raise` takes a `String!` or an `Error!`; anything else is a compile error (`raise requires a String! or Error!, got Int!`)
- raising a string wraps it in a built-in `BasicError` (implements `Error`, `message` = the string)
- raising a value implementing `Error` raises it as-is
- `raise` is itself an expression of any type (fresh type var), so it can sit in any branch (e.g. a `case`/`catch` arm) without breaking the merged result type

## The `Error` interface

```dang
interface Error {
  message: String!
}
```

- `Error` is a real prelude interface; `BasicError` is the built-in implementer for string raises
- user error types must `implements Error`, which forces a `message: String!` field (see [#interfaces-unions])
- additional fields are preserved on the raised value and matchable in `catch` (e.g. `resource`, `field`, `code`)
- `Error!` is usable like any interface type — as a fn param, in a type pattern, etc.

## Catching

```dang
try {
  validate(name)
} catch {
  err => "fallback: " + err.message
}
```

- whole `try`/`catch` is one expression — assignable, returnable, usable inline (incl. nested / in a `let`)
- the success value of the body passes through unchanged when nothing is raised
- the body and every catch clause must merge to one type, else compile error (`cannot use String! as Int!`)
- catches errors raised anywhere in the body, including ones propagated out of called functions and runtime errors (e.g. `1 / 0` → `division by zero`)

## Type-pattern catches

```dang
try { ... } catch {
  v: ValidationError => v.field
  n: NotFoundError => n.resource
  e: Error => e.message     # interface pattern = typed catch-all
  err => err.message        # bare catch-all, err: Error!
}
```

- catch clauses are the same patterns as `case`, but limited to **type patterns** and a **catch-all** (no value patterns)
- first match wins
- pattern types must implement `Error` (validated against the `Error!` operand, like a `case` over an interface — see [#control-flow])
- the bare catch-all binds the error as `Error!`
- the `Error` interface itself works as a pattern, matching any error; place specific types before it

## Propagation

- uncaught errors unwind through enclosing function calls
- a `raise` with no enclosing `catch` terminates the program (`uncaught error: <message>`)
- `return` *cannot* be caught; it's not an error — `try { return x } catch {..}` still returns `x` from the function
- re-raise inside a catch with `raise err` (or a new error); it propagates to the next enclosing `catch`

## When to raise vs. return null

> Meta: a small "when to raise vs. when to return null" table would help here. The rule of thumb: raise when continuing would yield wrong results; return null when absence is normal.

| situation | use |
|---|---|
| absence is a normal, expected outcome | return `null` (nullable type) |
| caller routinely branches on the result | return `null` / a result value |
| continuing would produce wrong results | `raise` |
| failure crosses a boundary (validation, HTTP/GraphQL, contract) | `raise` |

## Common patterns

- input validation
- failed external calls (HTTP/GraphQL)
- contract violations

## Anti-patterns

- using `raise` for early exit (use `return` — see [#control-flow])
- using `raise` to signal expected absence (return `null` — see [#flow-typing])
- catching `Error` just to ignore it
