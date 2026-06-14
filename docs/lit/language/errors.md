\use-plugin{dang}
\literate-fences

# Errors: `try`, `catch`, `raise` {#errors}

An error is a value — an object implementing the `Error` interface — and
error handling is expression-shaped like everything else in Dang (see
[#control-flow]): `raise` cuts the computation short with an error
describing what went wrong, and `try`/`catch` is an expression that yields
the body's value when it succeeds or a catch clause's when it doesn't.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## Raising

In its simplest form, `raise` takes a message string. The error unwinds to
the nearest enclosing `catch` — and with no `catch` anywhere up the stack,
it terminates the program:

```dang-failure
raise "something went wrong"
```

Caught, the message comes back as the error's `message` field: raising a
`String!` wraps it in the built-in `BasicError`, so even a string raise
produces a real error value:

```dang
try { raise "out of coffee" } catch { err => "plan B: " + err.message }
```

Only a `String!` or a value implementing `Error` (next section) can be
raised:

```dang-failure
raise 42
```

`raise` is itself an expression, and an expression of *any* type — a fresh
type variable — so it can sit in any branch without breaking the merged
result type. `halve` stays an `Int!` function even though its `else` branch
raises; and because errors propagate out of calls, the failure surfaces at
the caller's `catch`:

```dang
halve(n: Int!): Int! {
  if (n % 2 == 0) {
    n / 2
  } else {
    raise `${n} is odd`
  }
}

[halve(10), try { halve(7) } catch { err => 0 }]
```

## The `Error` interface

`Error` is a real interface, declared in the prelude alongside `BasicError`
(see [#stdlib]), with a single required field:

```dang-static
interface Error {
  message: String!
}
```

A user error type opts in with `implements Error` (see
[#interfaces-unions]), and conformance is enforced — leaving out `message`
is a compile error:

```dang-failure
type BrokenError implements Error { code: Int! }
```

A value implementing `Error` raises as-is — no wrapping — and any
additional fields, like `resource` here, ride along on the raised value for
a `catch` to read:

```dang
type NotFoundError implements Error {
  message: String!
  resource: String!
}

try { raise NotFoundError(message: "user gone", resource: "User") } catch {
  err => err.message
}
```

And `Error!` is an ordinary interface type — a parameter type, a type
pattern, anywhere a type goes:

```dang
describe(err: Error!): String! { `error: ${err.message}` }

try { raise "no more tea" } catch { err => describe(err) }
```

## Catching

The whole `try`/`catch` is one expression — bind it, return it, nest one
inside another. When nothing raises, the body's value passes through
unchanged:

```dang
let attempt = try { "all good" } catch { err => "recovered: " + err.message }

attempt.toUpper
```

The body and the catch clauses merge to one type when they can; arms that
diverge widen to a union instead, exactly like `if` branches and `case`
clauses (see [#control-flow]). Here the body is an `Int!` and the catch
recovers with a `String!`, so the whole expression is an `Int! | String!` —
which `case` type patterns can take back apart:

```dang
let outcome = try { halve(7) } catch { err => err.message }

case (outcome) {
  n: Int => "halved fine"
  s: String => `halving failed: ${s}`
}
```

A `catch` hears everything raised in its body: explicit raises, errors
propagating out of called functions, and runtime errors like division by
zero — which arrive wrapped in `BasicError`, so `.message` is always there:

```dang
try { toString(100 / 0) } catch { err => err.message }
```

## Type-pattern catches

Catch clauses are the same patterns as `case` (see [#control-flow]),
limited to **type patterns** and a bare **catch-all**, which binds the
error as `Error!`. `lookup` here raises a different error type for each
way it can fail:

```dang
type ValidationError implements Error {
  message: String!
  field: String!
}

lookup(id: Int!): String! {
  if (id <= 0) {
    raise ValidationError(message: "id must be positive", field: "id")
  } else if (id > 100) {
    raise NotFoundError(message: `no user ${id}`, resource: "User")
  } else {
    `user-${id}`
  }
}

lookup(7)
```

A `catch` dispatches on the raised error's type, routing each to its own
recovery — the binding is the error narrowed to the matched type, extra
fields included:

```dang
fetch(id: Int!): String! {
  try { lookup(id) } catch {
    v: ValidationError => `bad input: ${v.field}`
    n: NotFoundError => `no such ${n.resource}`
    err => err.message
  }
}

[fetch(7), fetch(0), fetch(404)]
```

Pattern types must implement `Error` — validated like a `case` over an
interface operand (see [#control-flow]):

```dang-failure
try { lookup(7) } catch { s: String => s }
```

Value patterns have no place in a `catch` — there is no operand to compare,
only an error to inspect — and they don't even parse:

```dang-failure
try { lookup(404) } catch { 404 => "no user" }
```

The `Error` interface is itself a valid pattern — a typed catch-all
matching any error — and clauses are tried top to bottom, first match wins,
so specific types go before general ones. This `ValidationError` clause is
unreachable:

```dang
try { lookup(0) } catch {
  e: Error => `something failed: ${e.message}`
  v: ValidationError => "never reached"
}
```

And when *no* clause matches, the error is re-raised to the next enclosing
`catch` — an incomplete `catch` narrows what it handles instead of
swallowing the rest. (That's also why it doesn't make the result nullable
the way a non-exhaustive `case` does: a miss re-raises, it never yields
`null`.)

```dang
try {
  try { lookup(404) } catch { v: ValidationError => "bad input" }
} catch {
  n: NotFoundError => `escalated: no ${n.resource}`
}
```

## Propagation

Uncaught errors unwind through enclosing function calls until a `catch`
takes them — above, `fetch` caught what `lookup` raised — and with no
`catch` all the way up, the program terminates with the error's message:

```dang-failure
lookup(404)
```

A `catch` can also rethrow: `raise err` re-raises the same error, or raise
a new one to recast it. Either way it propagates to the next enclosing
`catch`:

```dang
try {
  try { lookup(-5) } catch { err => raise `lookup failed: ${err.message}` }
} catch {
  err => err.message
}
```

Jumps are not errors: `return`, `break`, and `continue` pass through a
`try` untouched (see [#control-flow]), so an early exit can't be
accidentally caught:

```dang
bail: String! {
  try { return "returned, not caught" } catch { err => "caught?!" }
}

bail
```

## When to raise vs. return null

> Meta: a small "when to raise vs. when to return null" table would help here. The rule of thumb: raise when continuing would yield wrong results; return null when absence is normal.

Not every "no result" is a failure. When absence is a normal, expected
outcome — a search that can come up empty — return `null` and let the
caller branch (see [#flow-typing]):

```dang
find(name: String!): Int {
  if (name == "alice") 1 else if (name == "bob") 2
}

[find("alice"), find("nadia")]
```

`raise` is for failures: continuing would produce wrong results, or the
failure crosses a boundary — invalid input, a violated contract, a failed
HTTP or GraphQL call. `lookup` above raises rather than returning `null`
because an id you're already holding should resolve; a miss means something
went wrong upstream.

| situation | use |
|---|---|
| absence is a normal, expected outcome | return `null` (nullable type) |
| caller routinely branches on the result | return `null` / a result value |
| continuing would produce wrong results | `raise` |
| failure crosses a boundary (validation, HTTP/GraphQL, contract) | `raise` |

## Anti-patterns

- using `raise` for early exit (use `return` — see [#control-flow])
- using `raise` to signal expected absence (return `null` — see [#flow-typing])
- catching `Error` just to ignore it
