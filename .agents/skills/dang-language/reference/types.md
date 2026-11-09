# Types, Nullability, Flow-typing, Enums, Scalars

## Built-in types
- `Int!`, `Float!`, `String!`, `Boolean!`, `ID!`
- `[T]` and `[T]!` — lists
- `{{ ... }}` — records (always non-null)
- `Decoded` — decoded data not yet given a type (see stdlib.md)
- custom GraphQL scalars: `URL`, `Timestamp`, `JSON`, ...
- user-defined: `type`, `interface`, `union`, `enum`, `scalar`

## The `!` sigil (nullability)
- `T!` — non-null `T`; `T` (no bang) — nullable `T`.
- Assignability: `T!` satisfies `T`, but `T` does **not** satisfy `T!` (`null` can't init a `T!` slot).
- `!` is a postfix wrapper on a (possibly list) type: `[String!]!` = non-null list of non-null strings.
- Object/`type` literals are always non-null (no nullable-object form).
- `[T]` and `Int!` are unrelated — you can't assign `Int!` to `[Int]`.

### List nullability matrix
| written | meaning |
|---|---|
| `[T]` | nullable list of nullable T |
| `[T]!` | non-null list of nullable T |
| `[T!]` | nullable list of non-null T |
| `[T!]!` | non-null list of non-null T |

## Null propagation
- `nullable.field` is nullable even if `field: T!`.
- Chains short-circuit: `a.b.c` is null if any link is null.
- Function values can be null (a list element `fs[0]`, `&h.hook` off a nullable `h`, an `if` without `else`): not callable until checked (`if (f != null) { f(1) }`), asserted (`fs[0]!(1)`, the call form of `!`), or defaulted with `??`. `h.hook(1)` through a nullable `h` still propagates null.
- Operators do **not** propagate null: arithmetic (`+ - * / %`, incl. string/list `+`) and ordering (`< <= > >=`) need non-null operands, so `xs[0] + 1`, `m["k"] * 2`, `"hi " + maybeName` are type errors (`operator addition is not defined for nullable type Int: …`). Use `!`, `??`, or a null check. `==`/`!=` accept nullable operands.
- Recover via `??` or flow narrowing (below).

## Type variables
- single lowercase letters (`a`, `b`), used in generic signatures, inferred at call sites.

## Type hints / casts: `::`
- `expr :: Type!` annotates an expression's type.
- Binds only a bare `Term` on the left — wrap compound exprs in parens: `(a + b) :: T!`.
- `::` is the explicit materialization/coercion boundary: `String` → custom scalar (`URL!`, `Timestamp!`), enum (`"PASSED" :: Status!`), and `ID` coercions go here. The reverse degrade also works: `myUrl :: String!`, `p :: String!`.
- Coercion source is limited: only **`String`** values coerce to custom scalars/enums (and custom scalars back to `String`). `42 :: URL!` is rejected **statically** (`type hint mismatch: Int!, but hint expects URL!`).
- Enum casts of a string **literal** are checked **statically**: `"NOPE" :: Status!` → `"NOPE" is not a value of Status; expected one of …`. A non-literal `String` is checked at **runtime** (`s :: Status!` → `invalid enum value`).
- Nullable → non-null casts are checked at **runtime**, rejecting null: `JSON.decode("null") :: String!` → `null is not allowed for String!`.

Two uses to keep distinct:
- type *hint* — narrowing/disambiguation, e.g. `[] :: [String!]!` pinning a type variable (rarely needed: empty literals usually infer from context — see stdlib.md).
- type *cast* — coercion, e.g. `myUrl :: URL!`. The runtime-assertion behavior for non-null casts is a footgun.

## Coercion rules
- Assignment / arguments: pure subtyping, **no** scalar coercion — a non-literal `String!` won't pass where `URL!` is expected (`cannot use String! as URL!`).
- **Exception 1 (refine)**: *literal* expressions (string/template literals, list literals of them, map literals whose values are) auto-coerce to compatible scalars at value-handoff boundaries (call args, typed slots, returns) — `fetchURL(url: "https://…")` and ``fetchURL(url: `https://${host}`)`` both work.
- **Exception 2 (degrade)**: a **custom scalar value** (Path, ID, URL, Regexp, schema scalars — every non-primitive scalar; builtin `JSON` values degrade to their JSON text) auto-degrades to `String`/`String!` at the same value-handoff boundaries (call args, typed slots, returns, reassignment, `::`, case-clause values) — custom scalars are strings underneath. So `dir.file(p)` works where `file(path: String!)`. Any expression qualifies, not just literals — the mirror image of the literal rule. No transitivity: `Path!` still can't flow into `URL!`.
- `::` casts: explicit `String`/enum/`ID` coercion permitted, both directions for scalars.
- List merges are pure — `String!` does not become `ID!` element-wise, and `[p, "b"]` is `no common type between Path! and String!` (degrade applies at boundaries, not merges).

## Flow-sensitive typing (null narrowing)

Narrowing applies to **bare symbols** (locals, and bare `self`-field references inside methods).

```dang
if (x != null) { print("got " + x) }   # x is T! in the then-branch
if (x == null) { return "no value" }    # x is T! after the guard
if (x == null) { ... } else { ... }     # x is T! in the else
loop { if (x == null) { break }; x = x.next }   # x is T! after the guard
```

### Diverging constructs are narrowing-aware
- `return`, `raise`, `break`, `continue` all count as diverging.
- Code after a diverging guard sees the narrowed type for the rest of the enclosing scope.
- `else if` chains: the outer guard's falsy facts still apply afterward.
- Sequential guards accumulate, each narrowing independently in order.
- At script top level, a guard narrows the later top-level *statements* only: declarations (`let`, functions) all run before the first statement, so `if (x == null) { raise … }; let y: Int! = x` is still an error.
- `case` clauses narrow like guards: after `null => …`, later clauses see a bare-name operand as `T!`; in an operand-less `case`, later clauses see earlier conditions' falsy facts (`x == null => …` then `x` is `T!`), and each clause body its own condition's truthy facts. A clause matching a non-null value narrows the operand in its body.

### Compound conditions
- Guard with `or`: entering the diverging branch means *both* checks failed, so **both** narrow afterward:
  ```dang
  if (x == null or y == null) { raise "missing" }   # both x, y are T! after
  ```
- Compound `and` *inside a then-branch* narrows both operands in that branch:
  ```dang
  if (maybe != null and other != null) { maybe + other }   # both T! here
  ```
- The right operand of `and` sees the left's truthy narrowing, and of `or` the left's falsy one, so guards work inline: `x != null and x > 0`, `x == null or x > 0`.

### Reassigning a narrowed name
- A narrowed name may be assigned anything its declared type allows (`x = x.next`). A non-null value keeps it narrowed; a possibly-null one ends the narrowing until the next check.
- So do `self.x = v` (for the bare field `x`), calling a closure that assigns `x` null, and calling a block parameter (for anything declared outside the function taking it). Writes in the other branch of the same `if` or arm of the same `case`, in a branch that returns (or breaks/continues, for the rest of the loop body; not inside a `rescue` operand or a block passed to a Dang function, where a raise before the exit may be rescued), or in an uncalled nested function don't count.
- A field write evaluates its value before walking the target's path: in `x.n = v` (or `+=`), a value that may null `x` ends its narrowing for the write (compile error); compute the value into a local first, or assert `x!.n = v`.
- A block or nested function can't rely on an outer check of `x` if it may run again or later after `x` is set to null (the block assigns `x` null itself, or `x` is set to null after a nested function or stored block reading it is created): compile error. Check inside it, or bind the checked value to a local.

### Limitations (the surprising gaps)
- Narrowing is **intra-procedural** — calling a function doesn't carry narrowed types across.
- **`and`-guard does NOT narrow**: `if (x == null and y == null) { raise … }` tells us only that *at least one* is non-null afterward, so neither narrows.
- **Field accesses don't narrow**: a null check on `h.val` does not narrow later `h.val` accesses (each access could differ). Workaround: bind to a local first — `let v = h.val; if (v == null) { … }`. Same for a computed member (`text: String { … }`) used by its bare name: each read calls it again.
- In an `else` after a `== null` guard, the variable is known *null* (not `T!`) — using it as non-null there errors.

### Type narrowing via `case`
```dang
case (animal) {
  c: Cat => c.purr     # c is Cat!
  d: Dog => d.bark     # d is Dog!
}
```
- `binding: TypeName => …` binds the operand narrowed to the pattern type.
- `rescue` typed clauses narrow the bound error the same way.
- Recovers a concrete value from a widened conditional: an `if`/`else` over divergent branches infers as a **union**, which `case` then narrows.
- An `if`/`else` where one branch is `null` infers **nullable**; divergent concrete branches widen to a common interface/supertype, or to a union when unrelated. Only *using* the result forces the union/narrowing.

## Enums

```dang
enum Color { RED GREEN BLUE }
```
- Members are bare `CAPS` identifiers separated by whitespace/newlines — **NOT commas** (`RED, GREEN` is a syntax error).
- Accessed as `Color.RED` — always qualified by the enum name. The bare value (`RED`) is NOT in scope; `RED` alone → `"RED" not found`, including inside `case`. (Exception: an imported enum's values may be unqualified — see graphql.md.)
- `Color.values` → `[Color!]!` (a real list: `.length`, `.contains`, indexing).
- Equality compares by value; values of different enums / different members are not equal.

```dang
usersByStatus(status: Status.ACTIVE)
UserSort(field: UserSortField.NAME, direction: SortDirection.DESC)   # into input objects

case (c) {
  Color.RED => "stop"
  Color.GREEN => "go"  # every member named: exhaustive, String! (no else needed; a nullable c also needs `null =>`)
}

"RED" :: Color!      # validated coercion; a literal that isn't a value ("NOPE" :: Status!) is a compile error, a non-literal String is checked at runtime
```

## Custom scalars

```dang
scalar Email
scalar URL
```
- *Literal* expressions auto-coerce to a compatible scalar at value-handoff boundaries (call args, typed slots, list and map literals, returns) — including backtick templates with `${...}`.
- A *non-literal* String (a variable or `a + b`) does NOT auto-coerce — needs an explicit `::` cast: `fetchURL(url: myUrl :: URL!)`, `(base + domain) :: URL!`. Otherwise `cannot use String! as URL!`.
- List merging is pure/invariant: `["abc" :: ID!, "def"]` → `no common type between String! and ID!`.
- Runtime materialization supports `String -> custom scalar` and `custom scalar -> String` only (`42 :: URL!` is a type error).
- Scalars from GraphQL responses flow naturally; no cast needed.
- **Decoding** (`JSON`/`YAML`/`TOML.decode`) differs by owner: a scalar from an imported **schema** accepts any JSON value (object, array, number, ...) and holds it as JSON text; a **Dang-owned** scalar — `scalar Foo` you declare (with or without a body), `Path` and `Regexp` — accepts **only a string** (`expected string for scalar Foo, got array`). The builtin `JSON` (and a `scalar JSON` you declare) is the exception: it accepts any value, held as its JSON text. Details in stdlib.md "Coercion during parsing".
- Values flow **out** freely: every custom scalar **degrades to `String` at value-handoff boundaries** (Coercion rules, Exception 2), and `toString`/interpolation yield the raw string. String *methods* still need a String receiver — degrade through a slot or cast first (`(u :: String!).toUpper`).
- Equality, list membership, records/lists all work between values of the same scalar. A string *literal* converts to the scalar (`urlValue == "https://…"`); a non-literal `String` needs a cast (`urlValue == (s :: URL!)` or `(urlValue :: String!) == s`).
- The `Path` scalar carries its own methods and normalizes on construction — see stdlib.md. It is declared with the scalar-body syntax below.
- The builtin `JSON` is the **untyped JSON value**: decoding into it accepts any value and holds it as its JSON text (a string `"x"` is held as the text `"x"`, quotes included), `JSON.decode(j)` re-materializes it at a type, and encoding embeds it as data rather than a string — see stdlib.md. A schema's `scalar JSON` (e.g. `Dagger.JSON`) is an ordinary schema scalar, below.
- A scalar from a connected schema whose server returns a non-string value (a number, object, …) holds that value's JSON text, and sends it back as the same value.

### Scalar bodies: methods and the `new()` hook

A `scalar` declaration may carry a body of **methods** and at most one
**`new()` hook** — the string-refinement pattern (a scalar that normalizes/
validates its underlying string and offers behavior):

```dang
scalar Slug {
  new(raw: String!) {                # runs at EVERY materialization
    raw.trimSpace.toLower.replace(" ", "-")
  }
  words: [String!]! { (self :: String!).split("-") }
  first: String! { self.words[0] ?? "" }
}
```

- **`new(raw: String!)`** takes exactly one non-null String parameter and its
  body returns **`String!`** — the canonical underlying string; the runtime
  wraps it into the scalar. It runs at every way a value comes to exist:
  string literals in scalar-typed slots, `:: Slug!` casts, codec decode, and
  the derived constructor. A `raise` in the hook surfaces as a recoverable
  materialization error at the offending slot. `self` is unavailable inside
  `new()` (no value exists yet).
- Declaring `new()` makes the scalar's name callable as a **constructor
  function**: `Slug(anyStringExpr)` — the entry point for non-literal
  Strings, mirroring how `type` names double as constructors.
- **Members must be methods** (computed members or functions, `let` for
  private helpers) — stored fields are rejected; a scalar carries no state
  beyond its string. Inside a method, `self` is the non-null scalar; get the
  raw string via degrade (`let s: String! = self`) or `self :: String!`.
  Sibling methods are callable bare or via `self.`.
- Construction is total only if the hook never raises; equality is semantic
  when the hook normalizes (`Slug("A b") == Slug("a-b")`).

### `ID!`
- Its own scalar, not a `String` alias: `String!` does not unify with `ID!` (no implicit interop). But string *literals* still coerce into `ID!` slots (`idSlot: ID! = "abc"`).
- Scalar fields are **invariant** across interface implementation — `id: String!` does not satisfy `id: ID!` (see objects.md variance).
