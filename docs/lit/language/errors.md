\use-plugin{dang}

# Errors: `try`, `catch`, `raise` {#errors}

> Meta: positioning matters: errors are not for control flow. Open with "Dang uses errors for *errors* — recoverable failures across boundaries — not for `null` or expected branches."

## Raising

```dang
raise "something went wrong"
raise NotFoundError(message: "user gone", resource: "User")
```

- raise a string → wrapped in a default `Error`
- raise any value implementing `Error` (must have `message: String!`)

## The `Error` interface

```dang
interface Error {
  pub message: String!
}
```

- user error types must `implements Error`
- additional fields are preserved and matchable in `catch`

## Catching

```dang
try {
  validate(name)
} catch {
  err => "fallback: " + err.message
}
```

- `try` body is an expression; `catch` body is too
- whole `try`/`catch` is an expression — assignable, returnable

## Type-pattern catches

```dang
try { ... } catch {
  v: ValidationError => v.field
  n: NotFoundError => n.resource
  err => err.message       # catch-all
}
```

- first match wins
- pattern types must implement `Error`

## Propagation

- uncaught errors unwind through function calls
- a `raise` with no enclosing `catch` terminates the program
- `return` *cannot* be caught; it's not an error

## Common patterns

> Meta: a small "when to raise vs. when to return null" table would help here. The rule of thumb: raise when continuing would yield wrong results; return null when absence is normal.

- input validation
- failed external calls (HTTP/GraphQL)
- contract violations

## Anti-patterns

- using `raise` for early exit (use `return`)
- catching `Error` just to ignore it
