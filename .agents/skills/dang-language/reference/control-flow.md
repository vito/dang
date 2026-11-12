# Control Flow and Errors

## Everything is an expression
`if`, `case`, `loop`, `rescue` all return values (the last expression in the chosen branch), so they're assignable, returnable, and usable as args.

## `if` / `else`
```dang
let status = if (active) "on" else "off"
```
- Branches are plain expressions; braces aren't part of the `if` syntax. `if (x) { a } else { b }` is just `if`/`else` applied to two blocks — use a block when a branch needs multiple expressions.
- Condition must be `Boolean!` — **no truthiness**. A non-`Boolean` condition is a compile error (`condition must be Boolean!, got Int!`).
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
- Clauses tried top-to-bottom; first match wins. Duplicate **value** clauses are legal (first wins), but provably-dead clauses are **compile errors**: any clause after an `else` (`unreachable clause: follows the else catch-all on line N`), a duplicate type pattern, or a type pattern an earlier one covers (an interface pattern its type implements, `[Animal!]!` before `[Cat!]!`) (`unreachable clause: X! is already matched by the Y! clause on line N`). A trailing `else` after type patterns stays legal — a nullable operand falls through every type pattern.
- Clause bodies merge to a common type when they can; diverging bodies widen to a union, like `if` branches.
- If nothing matches and there's no `else`, the case is `null` — its result type is **nullable**. An `else` keeps it non-null, as do type patterns covering every possibility of the operand (**exhaustive**: each member of a union, each option of an inline union like `[Int!]! | [String!]!`, or a pattern the operand's type fits), value patterns naming every member of an enum (`Color.RED`, an imported bare `RED`, or `"RED"`), or `true` and `false` for a `Boolean`. An interface operand is exhaustive only via an interface catch-all pattern (its implementer set is open); a nullable operand is exhaustive only with a `null` pattern as well.

### Value patterns
- Literal scalars: ints `1`, floats `3.14`, strings `"foo"`, booleans, `null`, enum values.
- Literal strings/values auto-coerce to the operand's scalar/enum type (operand `URL!`/enum, clause `"https://..."`/`"ACTIVE"`) — same coercion as field/arg/return boundaries.
- Coercion only applies to syntactic literals; a non-literal value of a different type is a mismatch (`clause N value type mismatch`).

### Type patterns
```dang
case (animal) {
  c: Cat! => c.purr
  d: Dog! => d.bark
}
```
- Form `binding: Type! => expr`: the arm takes the operand's values of type `Type!` and binds them as `Type!`. The `!` is required (a bare `c: Cat =>` → ``a type pattern is the non-null type its arm binds: write `c: Cat! =>` ``); `null` never matches a type pattern. `Type` may be qualified (`g: Lib.Greeter! =>`, also in `rescue` clauses), resolved like that qualified type anywhere else — the way to name an imported type a local one shadows.
- `case` is a **type switch**: the pattern may be any type — named (object, interface, union, scalar, enum: match by identity, implementation or membership), list `[T]!`, map `Map[T]!`, or record `{{a: Int!}}!` (same field set, fitting field types). It must **overlap** the operand's type, as the types relate (identity, implementation, union membership — two interfaces related by neither don't overlap, even if one type implements both), else `pattern P can't match a value of type O` (on a union/interface operand: `type X is not a member of union Y` / `does not implement interface`). A pattern the operand's type fits takes every value (`a: Animal! =>` on a `Cat!`), so later arms are unreachable. A union holding decoded data (`JSON.decode(t) rescue 0` : `Decoded | Int!`) is rejected for any `case` → `a case can't match ..., a union holding decoded data: separate the decoded data first` (rescue to `null` instead).
- **Lists and maps match by their recorded element type** — the static element type they were built with (a literal's inferred type, a concatenation's or selection's result type), re-recorded at a typed boundary of a concrete type (annotation, parameter, return; a generic `[a]!` parameter keeps what the list recorded) — never by their contents: `[1, x]` with nullable `x` is `[Int]!` and doesn't match `[Int!]!`; a `[Animal!]!` list doesn't match `[Cat!]!` even if every element is a Cat; `[Cat(), Fish()]` is `[Animal!]!`. Only where that type still has type variables (built in a generic body, `[]`) do the elements decide (vacuously, for an empty list). Arms can exhaust an operand like `[Int!]! | [String!]!`.
- **On `Decoded` data**, an arm matches when the data has its type's **shape** — kind (number/string/boolean/list/map/object), list/map/object structure, and every required key present, recursively, honoring the decode's field names and strictness — checked without running any user code (no scalar `new()`, constructors or validators). Only the first matching arm then converts the data, exactly like a cast, so a constructor `raise` or a scalar hook rejecting the data is the case's **error**, not a fall-through. Patterns: `n: Int!`, `p: Point!`, a custom scalar (matches any string; a schema's scalar without a body holds data of any shape, so its arm matches any data, like a `Decoded!` arm), and list/map patterns `items: [Decoded!]!`, `obj: Map[Decoded!]!`, matched by their elements' shapes. **`else` is required** (`a case on decoded data needs an else`) unless an `x: Decoded!` arm takes the rest (on a nullable `Decoded` operand, together with a `null =>` arm, else `needs a \`null\` arm or an else`); value patterns alone (`null => ...`) need no `else`. On a nullable `Decoded` operand null data takes only `null =>`/`else`, however it arrived; on `Decoded!` null data goes to a `Decoded` arm or `else`. Caveats: `1` matches both `Int` and `Float` (order arms); a type arm accepts any object holding its required keys (extra keys pass unless the decode was strict; a field the decoding format ignores (`@JSON.ignore` for JSON data) reads no key, so a non-null one without a default never matches; explicit null for a defaulted non-null field takes the default, except a `Decoded!` field, which keeps null data); arms bind non-null values, so null data goes to `else`, a `Decoded` arm, or (nullable `Decoded` operand only) a `null =>` arm. Arm types must be concrete and materializable (`a pattern on decoded data needs a concrete type`, `decoded data never materializes as interface X`, also for an interface a match needs in the type's non-null fields: `..., which T.f holds`);
  duplicate arms, arms after a `Decoded!` arm, and arms an earlier arm covers (`Int!` after `Float!`; a `String!`, scalar or enum arm after a `String!` or custom-scalar arm, which take every string; any object/map arm after `Map[Decoded!]!`; a list/map arm after one whose elements cover its elements, e.g. `[Int!]!` after `[Decoded]!`) are unreachable-clause errors.
- On a union or interface operand, a named type no member or implementer can be → `type X is not a member of union Y` / `type X does not implement interface Y`.
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
- A block passed to a function that stores, returns, or collects its block param may outlive the call, so a `break` (or `return`) leaving that block is a compile error: `` ... so this block may outlive this call and cannot use `break` ``. A `break` targeting a `loop` inside the block is fine.

## `return`
- Exits the enclosing function/method/constructor early; outside one → `return outside of function`.
- Value type must satisfy the declared return type.
- Unwinds through enclosing blocks and loops (a `return` from inside `.each` exits the whole fn) — except from a block that may outlive its call (see above).
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
- Any type with a public `message: String!` is an error: `Error` is a Dang interface, so it is structural (see objects.md). `implements Error` additionally checks the shape at the declaration (omitting `message` → ``object Foo is missing `message: String!`, required by interface Error``). A schema's own error type with `message: String!` (Dagger's `Error`) raises and rescues too; its message is read when raised. Additional fields (`resource`, `field`, `code`) are preserved on the raised value and readable in `rescue` clauses. Raising a type without the shape → `raise requires a String! or Error!, got Foo!: missing field "message"`.

### Rescuing: the fallback form `expr rescue fallback`
```dang
let contents = dir.file("VERSION").contents rescue null
let ref = commit rescue "main"
```
- If evaluating the operand raises **any** error, the result is the fallback; on success the operand's value passes through unchanged. (Jumps and cancellation are not rescued; see Propagation.)
- The operand can be any term, including a block: `{ let msg = "boom"; raise msg } rescue "rescued"`.

### Rescuing: the clause form `expr rescue { clauses }`
```dang
validate(name) rescue {
  v: ValidationError! => v.field
  n: NotFoundError! => n.resource
  e: Error! => e.message     # interface pattern = typed catch-all, binds e
}
```
- The whole `rescue` is one expression — assignable, returnable, nestable.
- Clauses are `case` patterns limited to **type patterns** plus `else` (a value pattern like `404 =>` is a syntax error). `binding: Type! => expr` binds the error narrowed to `Type!`; pattern types must be errors: a type with `Error`'s shape, or a union of such types (else `type X does not implement interface Error`; for a union with a non-error member, `rescue pattern U! includes X, which isn't an Error, so it is never raised`). `else => expr` is the catch-all that discards the error. First match wins. The `Error` interface itself matches any error (typed catch-all); specific types must come first — ordering is **enforced** (see compile errors below).
- When **no clause matches**, the error is re-raised to the next enclosing `rescue` — a partial `rescue` narrows what it handles rather than swallowing the rest, and (unlike a non-exhaustive `case`) never makes the result nullable: a miss re-raises, it never yields null.
- Rescues errors raised anywhere in the operand, including ones propagated from called functions and **runtime failures**, classified into the taxonomy above: interpreter faults arrive as `RuntimeError`, failed asserts as `AssertionError`, GraphQL response errors as `GraphQLError` (server's own message, not the client-side wrapping) — so handlers dispatch on type instead of string-matching `.message`.
- Operand and clause results merge to one type when they can; arms that diverge **widen to a union** (`1 rescue { e: Error! => "s" }` is `Int! | String!`), exactly like `if` branches and `case` clauses. When a widened union later fails to fit somewhere (operator, binding, return, block-arg boundary), the error appends provenance notes citing the arm each member came from: `- String! from the rescue clause at foo.dang:5:3`.

### Precedence and parsing
- Binds tighter than `??`, looser than `or`: `x rescue null ?? "default"` is `(x rescue null) ?? "default"` — handle the error first, then the null.
- Left-associative chaining: `x rescue { n: NotFoundError! => .. } rescue { e: Error! => .. }` — a miss in the first re-raises into the second.
- A bare `{` after `rescue` always starts a **clause block**; a record fallback uses double braces and is unaffected: `x rescue {{ ok: false }}`.
- `raise x rescue y` parses as `raise (x rescue y)`; to rescue a raise, block it: `{ raise x } rescue y`.

### Compile errors
- Zero clauses, `expr rescue { }` → ``rescue requires at least one clause; to replace any error with a value, use the fallback form: `expr rescue value` ``
- A bare catch-all `err =>` is an error: bind with `err: Error! =>` or discard with `else =>`.
- **Unreachable clauses** (rescue and `case` alike): a duplicate type pattern or one an earlier pattern covers → `unreachable clause: X! is already matched by the Y! clause on line N`; any clause after `else` → `unreachable clause: follows the else catch-all on line N`. Rescue-only: because the operand is always `Error!`, an `else` after an `e: Error!` catch-all → `unreachable clause: the Error! clause on line N already matches every error` (in `case`, a trailing `else` after an interface pattern stays legal — nullable operands fall through type patterns).

### Propagation
- Uncaught errors unwind through enclosing calls; a `raise` with no enclosing `rescue` ends the program with a report: `uncaught <TypeName>: <message>`, the raise site, the error's fields, its `caused by:` chain, and `also failed:` for sibling failures in a concurrent `{{ }}`.
- `{{ }}` stays fail-fast with one deterministic primary error; a rescue catching the primary drops the siblings (they appear only in the uncaught report).
- `return` cannot be rescued — `{ return x } rescue fallback` still returns `x`; likewise `break`/`continue` are not errors.
- **Cancellation cannot be rescued.** When the host stops an evaluation (its timeout passes or it is cancelled), the evaluation ends with `evaluation cancelled: <reason>`. No `rescue` catches it — not the fallback form, not `e: Error! =>`, not `else =>` — and once the evaluation is cancelled, a `rescue` doesn't catch anything else either, so `loop { work() rescue null }` still stops.
- Re-raise inside a clause with `raise err` (or a new error); it propagates to the next enclosing `rescue`.

### Laziness warnings (non-fatal)
GraphQL failures happen at **execution points** — a leaf field call (scalar/enum underneath, incl. `@expectedType`-mapped fields like Dagger's `sync`), a `.{{ }}` selection, or a nullable object field (or anything selected off one) **escaping the chain**, where it is checked for null (`maybeUser(id: x) rescue null` runs the check inside the rescue; `me.parent.name` checks nothing, the leaf runs the chain) — not where an object-typed chain is built. Mutation results are never checked, so they stay lazy. The compiler warns (never errors) when a rescue is disconnected from them; warnings print to stderr on CLI runs and appear as Warning-severity LSP diagnostics:
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
