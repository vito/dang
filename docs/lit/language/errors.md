\use-plugin{dang}
\literate-fences

# Errors: `raise` and `rescue` {#errors}

An error is a value — an object implementing the `Error` interface — and
error handling is expression-shaped like everything else in Dang (see
[#control-flow]): `raise` cuts the computation short with an error
describing what went wrong, and the postfix `rescue` operator attaches a
recovery to any expression, yielding the operand's value when it succeeds
and the rescue's when it doesn't.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## Raising

In its simplest form, `raise` takes a message string. The error unwinds to
the nearest enclosing `rescue` — and with no `rescue` anywhere up the
stack, it terminates the program:

```dang-failure
raise "something went wrong"
```

Rescued, the message comes back as the error's `message` field: raising a
`String!` wraps it in the built-in `BasicError`, so even a string raise
produces a real error value:

```dang
{ raise "out of coffee" } rescue {
  err: Error => "plan B: " + err.message
}
```

(Why the braces? `rescue` binds tighter than `raise`, so a bare `raise x
rescue y` reads as `raise (x rescue y)` — to rescue a raise, wrap it in a
block.)

Only a `String!` or a value implementing `Error` (below) can be raised:

```dang-failure
raise 42
```

`raise` is itself an expression, and an expression of *any* type — a fresh
type variable — so it can sit in any branch without breaking the merged
result type. `halve` stays an `Int!` function even though its `else` branch
raises; and because errors propagate out of calls, the failure surfaces at
the *caller's* `rescue`:

```dang
halve(n: Int!): Int! {
  if (n % 2 == 0) {
    n / 2
  } else {
    raise `${n} is odd`
  }
}

[halve(10), halve(7) rescue 0]
```

## Rescuing with a fallback

That `halve(7) rescue 0` is the workhorse form: `expr rescue fallback`
yields `expr`'s value — unless *anything* raises while computing it (an
explicit raise, an error propagating out of a call, a runtime error), in
which case it yields `fallback` instead. When nothing raises, the operand
passes through untouched:

```dang
let attempt = "all good" rescue "recovered"

attempt.toUpper
```

The most useful fallback is often `null`: it turns "this failed" into
"this is absent", which the null machinery — `??` (see [#operators]) and
flow narrowing (see [#flow-typing]) — already knows how to handle.
`rescue` binds tighter than `??` (and looser than `or`), so the two chain
without parentheses — handle the error, then handle the null:

```dang
halve(9) rescue null ?? 0
```

The fallback can be any term — a literal, a call, a `{{ }}` record:

```dang
config: {{retries: Int!}}! { raise "config missing" }

config rescue {{retries: 3}}
```

One brace caveat: a single `{` after `rescue` always opens a *clause
block* (next sections), never a block fallback — a multi-step fallback is
spelled `expr rescue { else => { ... } }`.

The fallback form replaces **any** error, so keep the operand small: the
narrower the expression in front of `rescue`, the less it can silently
swallow.

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
a `rescue` to read:

```dang
type NotFoundError implements Error {
  message: String!
  resource: String!
}

{ raise NotFoundError(message: "user gone", resource: "User") } rescue {
  err: Error => err.message
}
```

And `Error!` is an ordinary interface type — a parameter type, a type
pattern, anywhere a type goes:

```dang
describe(err: Error!): String! { `error: ${err.message}` }

{ raise "no more tea" } rescue {
  err: Error => describe(err)
}
```

## Rescue clauses

To look at the error instead of discarding it, give `rescue` a clause
block: `expr rescue { clauses }`. Clauses are the same **type patterns**
as `case` (see [#control-flow]) — `binding: Type => expr`, with the
binding narrowed to the matched type — plus `else` for a catch-all that
discards the error. `lookup` here raises a different error type for each
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

A clause block dispatches on the raised error's type, routing each to its
own recovery — the binding is the error narrowed to the matched type,
extra fields included:

```dang
fetch(id: Int!): String! {
  lookup(id) rescue {
    v: ValidationError => `bad input: ${v.field}`
    n: NotFoundError => `no such ${n.resource}`
    err: Error => err.message
  }
}

[fetch(7), fetch(0), fetch(404)]
```

The `Error` interface is itself a valid pattern — a typed catch-all
matching any error, including runtime errors like division by zero, which
arrive wrapped in `BasicError` so `.message` is always there:

```dang
toString(100 / 0) rescue {
  err: Error => err.message
}
```

Clauses are tried top to bottom, first match wins, so specific types go
before general ones. This `ValidationError` clause is unreachable:

```dang
lookup(0) rescue {
  e: Error => `something failed: ${e.message}`
  v: ValidationError => "never reached"
}
```

To handle an error without inspecting it, `else =>` discards it:

```dang
halve(9) rescue {
  else => 0
}
```

And that's the only unbound catch-all — a clause either names the error's
type or declines to bind it, so a bare `err =>` is rejected:

```dang-failure
lookup(0) rescue { err => err.message }
```

Pattern types must implement `Error` — validated like a `case` over an
interface operand (see [#control-flow]):

```dang-failure
lookup(7) rescue { s: String => s }
```

Value patterns have no place in a rescue — there is no operand to compare,
only an error to inspect — and they don't even parse:

```dang-failure
lookup(404) rescue { 404 => "no user" }
```

Nor can a clause block be empty — replacing any error with a value is what
the fallback form is for:

```dang-failure
lookup(7) rescue { }
```

## No match? It re-raises

When *no* clause matches, the error is re-raised to the next enclosing
`rescue` — a partial rescue narrows what it handles instead of swallowing
the rest. (That's also why it doesn't make the result nullable the way a
non-exhaustive `case` does: a miss re-raises, it never yields `null`.)
Chaining is left-associative, so the next enclosing `rescue` can simply be
the next link of a chain:

```dang
lookup(404) rescue {
  v: ValidationError => "bad input"
} rescue {
  n: NotFoundError => `escalated: no ${n.resource}`
}
```

## Widening

The operand and the clause arms merge to one type when they can; arms that
diverge widen to a union instead, exactly like `if` branches and `case`
clauses (see [#control-flow]). Here the operand is an `Int!` and the
rescue recovers with a `String!`, so the whole expression is an `Int! |
String!` — which `case` type patterns can take back apart:

```dang
let outcome = halve(7) rescue {
  err: Error => err.message
}

case (outcome) {
  n: Int => "halved fine"
  s: String => `halving failed: ${s}`
}
```

## Rescuing a block

The operand can be any term — including a block, which puts several steps
under one recovery. This is the postfix spelling of what other languages
do with a try block:

```dang
quarter(n: Int!): String! {
  {
    let half = halve(n)
    let result = halve(half)
    `${n} quarters to ${result}`
  } rescue {
    err: Error => `no luck: ${err.message}`
  }
}

[quarter(8), quarter(6)]
```

## Propagation

Uncaught errors unwind through enclosing function calls until a `rescue`
takes them — above, `fetch` rescued what `lookup` raised — and with no
`rescue` all the way up, the program terminates with the error's message:

```dang-failure
lookup(404)
```

A rescue clause can also rethrow: `raise err` re-raises the same error, or
raise a new one to recast it. Either way it propagates to the next
enclosing `rescue`:

```dang
{
  lookup(-5) rescue {
    err: Error => raise `lookup failed: ${err.message}`
  }
} rescue {
  err: Error => err.message
}
```

Jumps are not errors: `return`, `break`, and `continue` pass through a
`rescue` untouched (see [#control-flow]), so an early exit can't be
accidentally rescued:

```dang
bail: String! {
  { return "returned, not caught" } rescue {
    err: Error => "caught?!"
  }
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

## Migrating from `try`/`catch`

`rescue` replaced Dang's original `try { } catch { }` blocks. The legacy
syntax still parses, so old code fails with a pointer rather than a
puzzle — type-checking rejects it with the migration path spelled out:

```dang-failure
try { halve(7) } catch { err => 0 }
```

Run `dang fmt -w` to migrate: it rewrites `try`/`catch` to the equivalent
postfix `rescue`, turning bare `err =>` catch-alls into `err: Error =>`
along the way. With the block syntax gone, `try` and `catch` are ordinary
identifiers again; `rescue` is the reserved word (see [#syntax]).

## Anti-patterns

- using `raise` for early exit (use `return` — see [#control-flow])
- using `raise` to signal expected absence (return `null` — see [#flow-typing])
- rescuing errors you can't do anything about — a fallback swallows *every* failure in its operand; keep the operand narrow and let the rest propagate
