# Control Flow and Errors

## Everything is an expression
`if`, `case`, `loop`, `rescue` all return values (the last expression in the chosen branch), so they're assignable, returnable, and usable as args.

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
- Clauses tried top-to-bottom; first match wins. Duplicate **value** clauses are legal (first wins), but provably-dead clauses are **compile errors**: any clause after an `else` (`unreachable clause: follows the else catch-all on line N`), a duplicate type pattern, or a type pattern covered by an earlier interface pattern (`unreachable clause: X is already matched by the Y clause on line N`). A trailing `else` after type patterns stays legal — a nullable operand falls through every type pattern.
- Clause bodies merge to a common type when they can; diverging bodies widen to a union, like `if` branches.
- If nothing matches and there's no `else`, the case is `null` — its result type is **nullable**. An `else` keeps it non-null, as do type patterns covering every member of a non-null union (**exhaustive**). An interface operand is exhaustive only via an interface catch-all pattern (its implementer set is open); a nullable operand is never exhaustive (`null` matches no type pattern).

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
- Also used in `rescue` clauses over `Error` subtypes.

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
- Valid only inside a block passed to a call (`.each`, `loop`, user-defined block-taking fns — a `loop` body is just an ordinary block argument); otherwise compile error (`break outside of block-taking call` / `continue outside of block-taking call`).
- `break` exits; `break value` makes the block-taking call yield `value`; bare `break` yields `null`.
- `continue` skips to the next iteration; in `.map`, `continue value` inserts `value` into the result (bare → `null`); in `.each`/`loop` it just advances.
- Target the nearest enclosing block-taking call only; an ordinary nested function does NOT inherit the target.

## `return`
- Exits the enclosing function/method/constructor early; outside one → `return outside of function`.
- Value type must satisfy the declared return type.
- Unwinds through enclosing blocks and loops (a `return` from inside `.each` exits the whole fn).
- A `return` in a skippable branch (e.g. a no-else `if`) does not make the fn non-null.
- `return` is **NOT an error** and is **NOT recoverable** by `rescue`.

## Errors: `raise` and postfix `rescue`

An error is a value implementing the `Error` interface; `raise` throws it up the stack, and the postfix `rescue` operator — itself an expression — recovers it. Raise for failures: continuing would produce wrong results, or the failure crosses a boundary (validation, HTTP/GraphQL, contract). Return `null` for normal absence (see the table below).

### Raising
```dang
raise "something went wrong"
raise NotFoundError(message: "user gone", resource: "User")
```
- `raise` takes a `String!` or an `Error!`; anything else → `raise requires a String! or Error!, got Int!`.
- A string raise wraps it in a built-in `BasicError` (implements `Error`, `message` = the string). A value implementing `Error` raises as-is.
- `raise` is itself an expression of any type (fresh type var), so it fits in any branch (e.g. a `case` clause or `rescue` arm) without breaking the merged result type.
- **Implicit cause**: a raise during a rescue arm's dynamic extent (clause expr or fallback, including through calls) records the rescued error as the new error's cause — out-of-band, not a field. A plain `raise err` re-raise does not self-cause; a raised value with its own non-null `cause: Error` field takes over the chain explicitly. The chain surfaces in uncaught output (`caused by:`), not in the type system.

### The `Error` interface
```dang
interface Error { message: String! }
```
- A real prelude interface, implemented by the **built-in taxonomy**: `BasicError` (string raises, nothing else), `AssertionError` (failed `assert` blocks), `RuntimeError` (interpreter faults: division by zero, failed non-null assertions/casts, invalid enum values), `GraphQLError` (a GraphQL response reporting errors; adds `path: [String!]!` and `extensions: String!` — the extensions object as JSON text, `"{}"` when absent).
- User error types `implements Error`, which forces `message: String!` (omitting it → ``object Foo is missing `message: String!`, required by interface Error``). Additional fields (`resource`, `field`, `code`) are preserved on the raised value and readable in `rescue` clauses.

### Rescuing: the fallback form `expr rescue fallback`
```dang
let contents = dir.file("VERSION").contents rescue null
let ref = commit rescue "main"
```
- If evaluating the operand raises **any** error, the result is the fallback; on success the operand's value passes through unchanged.
- The operand can be any term, including a block: `{ let msg = "boom"; raise msg } rescue "rescued"`.

### Rescuing: the clause form `expr rescue { clauses }`
```dang
validate(name) rescue {
  v: ValidationError => v.field
  n: NotFoundError => n.resource
  e: Error => e.message     # interface pattern = typed catch-all, binds e
}
```
- The whole `rescue` is one expression — assignable, returnable, nestable.
- Clauses are `case` patterns limited to **type patterns** plus `else` (a value pattern like `404 =>` is a syntax error). `binding: Type => expr` binds the error narrowed to `Type`; pattern types must implement `Error` (else `type X does not implement interface Error`). `else => expr` is the catch-all that discards the error. First match wins. The `Error` interface itself matches any error (typed catch-all); specific types must come first — ordering is **enforced** (see compile errors below).
- When **no clause matches**, the error is re-raised to the next enclosing `rescue` — a partial `rescue` narrows what it handles rather than swallowing the rest, and (unlike a non-exhaustive `case`) never makes the result nullable: a miss re-raises, it never yields null.
- Rescues errors raised anywhere in the operand, including ones propagated from called functions and **runtime failures**, classified into the taxonomy above: interpreter faults arrive as `RuntimeError`, failed asserts as `AssertionError`, GraphQL response errors as `GraphQLError` (server's own message, not the client-side wrapping) — so handlers dispatch on type instead of string-matching `.message`.
- Operand and clause results merge to one type when they can; arms that diverge **widen to a union** (`1 rescue { e: Error => "s" }` is `Int! | String!`), exactly like `if` branches and `case` clauses. When a widened union later fails to fit somewhere (operator, binding, return, block-arg boundary), the error appends provenance notes citing the arm each member came from: `- String! from the rescue clause at foo.dang:5:3`.

### Precedence and parsing
- Binds tighter than `??`, looser than `or`: `x rescue null ?? "default"` is `(x rescue null) ?? "default"` — handle the error first, then the null.
- Left-associative chaining: `x rescue { n: NotFoundError => .. } rescue { e: Error => .. }` — a miss in the first re-raises into the second.
- A bare `{` after `rescue` always starts a **clause block**; a record fallback uses double braces and is unaffected: `x rescue {{ ok: false }}`.
- `raise x rescue y` parses as `raise (x rescue y)`; to rescue a raise, block it: `{ raise x } rescue y`.
- `rescue` is a reserved word (can't be an identifier or binding name); `try` and `catch` are ordinary identifiers.

### Compile errors
- Zero clauses, `expr rescue { }` → ``rescue requires at least one clause; to replace any error with a value, use the fallback form: `expr rescue value` ``
- A bare catch-all clause, `err =>` → ``bare catch-all `err =>` is no longer supported; bind the error with `err: Error =>` or discard it with `else =>` ``
- **Unreachable clauses** (rescue and `case` alike): a duplicate type pattern or one covered by an earlier interface pattern → `unreachable clause: X is already matched by the Y clause on line N`; any clause after `else` → `unreachable clause: follows the else catch-all on line N`. Rescue-only: because the operand is always `Error!`, an `else` after an `e: Error` catch-all → `unreachable clause: the Error clause on line N already matches every error` (in `case`, a trailing `else` after an interface pattern stays legal — nullable operands fall through type patterns).
- Legacy `try { } catch { }` still parses and runs, but type-checking warns: ``try/catch was replaced by postfix `rescue`; attach `rescue` to an expression or block — run `dang fmt -w` to migrate``. `dang fmt -w` rewrites it (a single-form try body unwraps to plain postfix, bare `err =>` bindings become `err: Error =>`, zero-clause handlers are dropped).

### Propagation
- Uncaught errors unwind through enclosing calls; a `raise` with no enclosing `rescue` terminates the program with a full report: `uncaught <TypeName>: <message>` (plain `uncaught error:` for BasicError) + highlighted raise site + the error's public data fields + the `caused by:` chain + `also failed:` sections for completed sibling failures from a concurrent `{{ }}` — every link and sibling rendered with its own highlighted source snippet (explicit-field causes have no raise site, so they get just the summary line).
- `{{ }}` stays fail-fast with one deterministic primary error; a rescue catching the primary drops the siblings (they appear only in the uncaught report).
- `return` cannot be rescued — `{ return x } rescue fallback` still returns `x`; likewise `break`/`continue` are not errors.
- Re-raise inside a clause with `raise err` (or a new error); it propagates to the next enclosing `rescue`.

### Laziness warnings (non-fatal)
GraphQL failures happen at **execution points** — a leaf field call (scalar/enum underneath, incl. `@expectedType`-mapped fields like Dagger's `sync`) or a `.{{ }}` selection — not where an object-typed chain is built. The compiler warns (never errors) when a rescue is disconnected from them; warnings print to stderr on CLI runs and appear as Warning-severity LSP diagnostics:
- **`this rescue can never fire`** — the operand is provably infallible: only literals, plain reads, and GraphQL chain-building (`dir.file("x") rescue null` — `file` builds a query; nothing runs). Fix: rescue at the leaf (`.contents`, `.sync`, `.{{ }}`).
- **`a lazy T leaves this rescue without executing`** — the operand can fail, but its result is a still-unexecuted handle; the pipeline's failures surface where the handle is later forced, outside the rescue. Tails that ARE execution points (`.sync`, `.stdout`, `.{{ }}`) are exempt.
- **`a lazy T is assigned to X, declared outside this rescue`** — a handle assigned to an outer binding escapes the handler the same way.
The analysis is conservative in the permissive direction: any call, raise, division, indexing, or unknown construct counts as fallible and silences it.

### When to raise vs. return null
| situation | use |
|---|---|
| absence is a normal, expected outcome | return `null` (nullable type) |
| caller routinely branches on the result | return `null` / a result value |
| continuing would produce wrong results | `raise` |
| failure crosses a boundary (validation, HTTP/GraphQL, contract) | `raise` |

Anti-patterns: `raise` for early exit (use `return`); `raise` to signal expected absence (return `null`); rescuing `Error` just to ignore it.
