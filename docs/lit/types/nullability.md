\use-plugin{dang}

# Types and nullability {#nullability}

> Meta: nullability is the page's main attraction. Most readers won't have seen GraphQL-style `T!` outside of a schema and may misread it as "definitely-null" or "force-unwrap."

## Built-in types

- `Int!`, `Float!`, `String!`, `Boolean!`, `ID!`
- `[T]` and `[T]!` — lists
- `{{ ... }}` — records
- custom GraphQL scalars: `URL`, `Timestamp`, `JSON`, ...
- user-defined: `type`, `interface`, `union`, `enum`, `scalar` (see their pages)

## The `!` sigil

- `T!` — non-null `T`
- `T` (no bang) — nullable `T`
- assignability: `T!` satisfies `T`, but `T` does **not** satisfy `T!` (`null` can't init a `T!` slot)
- grammar: `!` is a postfix wrapper applied to a (possibly list) type — `NonNull <- inner:Type BangToken`. So `[String!]!` parses as `NonNull(List(NonNull(String)))` = non-null list of non-null strings
- object/`type` literals are always non-null in Dang (no nullable-object form)
- `[T]` and `Int!` are unrelated — you can't assign `Int!` to `[Int]`

## Lists, nullability matrix

| written | meaning |
|---|---|
| `[T]` | nullable list of nullable T |
| `[T]!` | non-null list of nullable T |
| `[T!]` | nullable list of non-null T |
| `[T!]!` | non-null list of non-null T |

## Null propagation

- `nullable.field` is nullable even if `field: T!`
- chains short-circuit: `a.b.c` is null if any link is null
- function values can be null too: an element of a list of functions (`fs[0]`), a method taken off a nullable value (`&h.hook`), an `if` without `else`. Such a value can't be called until it's checked for null (`if (f != null) { f(1) }`), asserted (`fs[0]!(1)`), or defaulted (`fs[0] ?? &fallback`). Calling a method through a nullable value (`h.hook(1)`) still propagates the null
- operators are the exception: arithmetic and ordering comparisons need non-null operands, so `xs[0] + 1` is a type error rather than `null` (see [#operators])
- recovers via `??` (see [#operators]) or flow narrowing (next section)

## Flow-sensitive narrowing {#flow-typing}

> Meta: this section pays for itself — the alternative is writing `!` casts everywhere. Make sure the examples include both narrowing inside a branch and narrowing after a guard clause.

### Null narrowing

After a guard:

```dang
if (x == null) { return "no value" }
# x is now T! here
print(x.length)
```

Inside a branch:

```dang
if (x != null) {
  # x is T! here
  print("got " + x)
}
```

Else branch:

```dang
if (x == null) { print("no x") } else {
  # x is T! here
}
```

Loop guards:

```dang
loop {
  if (x == null) { break }
  # x is T! after the guard
  x = x.next
}
```

### Diverging constructs are narrowing-aware

- `return`, `raise`, `break`, `continue` all count as diverging
- code after them in the same scope sees the narrowed type
- a guard whose then-branch diverges narrows the rest of the enclosing scope
- `else if` chains: the parser wraps `else if` in a Block, so the outer guard's falsy facts still apply afterward
- sequential guards accumulate: each narrows independently as forms are processed in order
- inside a `loop` / `.each` block, a guard that `break`s or `continue`s narrows the rest of that iteration (`if (x == null) { break }` → `x` is `T!` after)
- at the top level of a script, a guard narrows the top-level statements after it, but not declarations: every `let` and function runs before the first statement, so `if (x == null) { raise "missing" }` then `let y: Int! = x` is still an error
- `case` clauses narrow too: a clause runs only when the clauses before it didn't match, so after `null => …` the operand (a bare name) is `T!`, and after `x == null => …` in a `case` without an operand, `x` is. A clause matching a non-null value (`1 => …`) narrows the operand in its own body

### Reassigning a narrowed name

A narrowed name can still be assigned anything its declared type allows. A non-null value keeps it narrowed; a possibly-null one ends the narrowing until the next check:

```dang
if (x != null) {
  x = x.next     # fine: x is declared nullable
  x.val + 1      # error: x may be null again
}
```

The same goes for writes the checker sees less directly:

- `self.x = v` ends the narrowing of the bare field `x`
- calling a closure that assigns `x` null ends it, even if the closure was written before the check
- calling a block parameter ends the narrowing of anything declared outside the function taking it, which the caller's block may reassign
- a block or nested function can't rely on a check made outside it if it may run again, or later, after `x` is set to null: when the block itself assigns `x` null (it runs again on the next iteration), or when `x` is set to null after a nested function or stored block that reads it is created. Check `x` inside it, or bind the checked value to a local

Writes a read can't follow from don't count: those in the other branch of the same `if` or another arm of the same `case`, in a branch that returns (or, for the rest of a loop body, breaks or continues), or in a nested function that isn't called. A branch's exit doesn't hide its writes inside a `rescue` operand or a block passed to a Dang function, though: a raise before the exit may be rescued.

### Type narrowing via `case`

```dang
case (animal) {
  c: Cat => c.purr     # c is Cat!
  d: Dog => d.bark     # d is Dog!
}
```

- `binding: TypeName => …` clauses bind the operand narrowed to the pattern type
- `rescue` clauses reuse the same `CaseClause` form, so typed rescue clauses narrow the bound error the same way
- this is how you recover a concrete value from a widened conditional: an `if`/`else` over divergent branches infers as a **union**, which a `case` then narrows

### Conditional result inference (related)

- an `if`/`else` where one branch is `null` infers a **nullable** type, not non-null
- divergent concrete branches widen to their common interface/supertype, or to a union when unrelated
- a discarded divergent conditional is fine; only *using* the result forces the union/narrowing

### Compound conditions

```dang
if (x == null or y == null) { raise "missing" }
# both x and y are T! after the diverging guard
```

- guard with `or`: entering the diverging branch means *both* checks failed, so **both** narrow afterward
- compound `and` *inside a then-branch* narrows both operands in that branch:

```dang
if (maybe != null and other != null) {
  maybe + other   # both T! here
}
```

- the right operand of `and` sees the left's truthy narrowing, and the right operand of `or` the left's falsy narrowing, so a guard works inline: `x != null and x > 0`, `x == null or x > 0`

### Limitations

- narrowing is intra-procedural — calling a function doesn't carry narrowed types across
- **`and`-guard does NOT narrow**: `if (x == null and y == null) { raise … }` tells us only that *at least one* is non-null afterward, so neither narrows individually
- **field accesses don't narrow**: a null check on `h.val` does not narrow later `h.val` accesses, because each `.field` access could return a different value. Workaround: bind to a local first — `let v = h.val; if (v == null) { … }`
- in an `else` branch where the guard checked `== null`, the variable is known *null* (not narrowed to `T!`) — using it as non-null there errors
- narrowing applies to bare symbols (locals, and bare `self`-field references inside methods, which parse as plain `Symbol`s) — but not to a computed member (`text: String { … }`) referenced by its bare name: each read calls it again. Bind it to a local first

When narrowing can't reach the value — a field or call result, or a spot where the checker just can't follow your reasoning — the postfix `!` operator is the explicit escape hatch: `expr!` narrows `T` to `T!` and raises at runtime if the value turns out to be null. See [#operators].

See also [#errors] (`raise`/`rescue` divergence) and [#control-flow] (guards, loops, `case`).

> Meta: field-narrowing and the `and`-guard non-narrowing are the two most surprising gaps in practice. Both are documented above with the re-bind-to-a-local workaround.

## Type variables

- single lowercase letters: `a`, `b`
- used in generic function signatures
- inferred at call sites
- **opaque**: a generic value supports no operations. Inside the body that declares it, a type variable can only be passed through (returned, stored, passed on) or compared with `==`/`!=` — arithmetic and ordering are definition-time errors (`do(&yield: b): b { yield * 2 }` → "operator multiplication is not defined for the generic type b"). Use a concrete non-null type (e.g. `Int!`) if the body must operate on the value.

## Type hints / casts: `::`

- `expr :: Type!` annotates an expression's type (`TypeHint` node)
- grammar: binds only a bare `Term` on the left, so wrap compound exprs in parens — `(a + b) :: T!` (see [#operators] for precedence)
- `::` is the explicit materialization/coercion boundary: `String` → custom scalar (`URL!`, `Timestamp!`, …), enum (`"PASSED" :: Status!`), and `ID` coercions go here
- coercion source is limited: only **`String`** values coerce to custom scalars/enums. `42 :: URL!` is rejected **statically** ("type hint mismatch: Int!, but hint expects URL!")
- enum casts of a string **literal** are checked **statically** against the enum's values: `"NOPE" :: Status!` → `"NOPE" is not a value of Status; expected one of …`. Casting a non-literal `String` is checked at **runtime**: `s :: Status!` → "invalid enum value"
- nullable → non-null casts do **not** strip the wrapper statically; they defer to a runtime `Coerce` that rejects null: `JSON.decode("null") :: String!` → "null is not allowed for String!"

> Meta: `::` deserves a worked example showing the difference between a type *hint* (narrowing/disambiguation, e.g. `[] :: [String!]!` binding a type variable) and a type *cast* (coercion, e.g. `myUrl :: URL!`). The runtime-assertion behavior for non-null casts is a footgun worth a short callout.

## Coercion rules

- assignment / arguments: pure subtyping (`hm.Assignable`), **no** scalar coercion — a non-literal `String!` won't pass where `URL!` is expected: `fetchURL(url: myUrl)` errors "cannot use String! as Test.URL!"
- **exception**: *literal* expressions (string/template literals, list literals of them, map literals whose values are) auto-coerce to compatible scalars at value-handoff boundaries (call args, typed slots, returns) — `fetchURL(url: "https://…")` is fine, and ``fetchURL(url: `https://${host}`)`` works too
- `::` casts: explicit `String`/enum/`ID` coercion permitted (see above)
- list merges are pure — `String!` does not become `ID!` element-wise
- ongoing work — see `soundness.md` for the model being moved toward
