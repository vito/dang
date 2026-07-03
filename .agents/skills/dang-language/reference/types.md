# Types, Nullability, Flow-typing, Enums, Scalars

## Built-in types
- `Int!`, `Float!`, `String!`, `Boolean!`, `ID!`
- `[T]` and `[T]!` — lists
- `{{ ... }}` — records (always non-null)
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
- Recover via `??` or flow narrowing (below).

## Type variables
- single lowercase letters (`a`, `b`), used in generic signatures, inferred at call sites.

## Type hints / casts: `::`
- `expr :: Type!` annotates an expression's type (`TypeHint` node).
- Binds only a bare `Term` on the left — wrap compound exprs in parens: `(a + b) :: T!`.
- `::` is the explicit materialization/coercion boundary: `String` → custom scalar (`URL!`, `Timestamp!`), enum (`"PASSED" :: Status!`), and `ID` coercions go here. The reverse degrade also works: `myUrl :: String!`, `p :: String!`.
- Coercion source is limited: only **`String`** values coerce to custom scalars/enums (and custom scalars back to `String`). `42 :: URL!` is rejected **statically** (`type hint mismatch: Int!, but hint expects URL!`).
- Enum casts checked at **runtime**: `"NOPE" :: Status!` → `invalid enum value`.
- Nullable → non-null casts do **not** strip the wrapper statically; they defer to a runtime `Coerce` that rejects null: `JSON.decode("null") :: String!` → `null is not allowed for String!`.

Two uses to keep distinct:
- type *hint* — narrowing/disambiguation, e.g. `[] :: [String!]!` binding a type variable.
- type *cast* — coercion, e.g. `myUrl :: URL!`. The runtime-assertion behavior for non-null casts is a footgun.

## Coercion rules
- Assignment / arguments: pure subtyping, **no** scalar coercion — a non-literal `String!` won't pass where `URL!` is expected (`cannot use String! as URL!`).
- **Exception 1 (refine)**: *literal* expressions (string/template literals, list literals of them) auto-coerce to compatible scalars at value-handoff boundaries (call args, typed slots, returns) — `fetchURL(url: "https://…")` and ``fetchURL(url: `https://${host}`)`` both work.
- **Exception 2 (degrade)**: a **custom scalar value** (Path, ID, URL, Regexp, schema scalars — every non-primitive scalar except the `JSON`/`YAML`/`TOML` codec namespaces) auto-degrades to `String`/`String!` at the same value-handoff boundaries (call args, typed slots, returns, reassignment, `::`, case-clause values) — custom scalars are strings underneath. So `dir.file(p)` works where `file(path: String!)`. Any expression qualifies, not just literals — the mirror image of the literal rule. No transitivity: `Path!` still can't flow into `URL!`.
- `::` casts: explicit `String`/enum/`ID` coercion permitted, both directions for scalars.
- List merges are pure — `String!` does not become `ID!` element-wise, and `[p, "b"]` is `no common type between Path! and String!` (degrade applies at boundaries, not merges).

## Flow-sensitive typing (null narrowing)

Narrowing applies to **bare symbols** (locals, and bare `self`-field references inside methods).

```dang
if (x != null) { print("got " + x) }   # x is T! in the then-branch
if (x == null) { return "no value" }    # x is T! after the guard
if (x == null) { ... } else { ... }     # x is T! in the else
loop { if (x == null) { break }, x = x.next }   # x is T! after the guard
```

### Diverging constructs are narrowing-aware
- `return`, `raise`, `break`, `continue` all count as diverging.
- Code after a diverging guard sees the narrowed type for the rest of the enclosing scope.
- `else if` chains: the parser wraps `else if` in a Block, so the outer guard's falsy facts still apply afterward.
- Sequential guards accumulate, each narrowing independently in order.

### Compound conditions
- Guard with `or`: entering the diverging branch means *both* checks failed, so **both** narrow afterward:
  ```dang
  if (x == null or y == null) { raise "missing" }   # both x, y are T! after
  ```
- Compound `and` *inside a then-branch* narrows both operands in that branch:
  ```dang
  if (maybe != null and other != null) { maybe + other }   # both T! here
  ```

### Limitations (the surprising gaps)
- Narrowing is **intra-procedural** — calling a function doesn't carry narrowed types across.
- **`and`-guard does NOT narrow**: `if (x == null and y == null) { raise … }` tells us only that *at least one* is non-null afterward, so neither narrows.
- **Field accesses don't narrow**: a null check on `h.val` does not narrow later `h.val` accesses (each access could differ). Workaround: bind to a local first — `let v = h.val; if (v == null) { … }`.
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
  Color.GREEN => "go"
  else => "?"        # no static exhaustiveness check
}

"RED" :: Color!      # runtime-validated coercion; "NOPE" :: Status! → invalid enum value "NOPE" for Status
```

## Custom scalars

```dang
scalar Email
scalar JSON
```
- *Literal* expressions auto-coerce to a compatible scalar at value-handoff boundaries (call args, typed slots, list literals, returns) — including backtick templates with `${...}`.
- A *non-literal* String (a variable or `a + b`) does NOT auto-coerce — needs an explicit `::` cast: `fetchURL(url: myUrl :: URL!)`, `(base + domain) :: URL!`. Otherwise `cannot use String! as URL!`.
- List merging is pure/invariant: `["abc" :: ID!, "def"]` → `no common type between String! and ID!`.
- Runtime materialization supports `String -> custom scalar` and `custom scalar -> String` only (`42 :: URL!` is a type error).
- Scalars from GraphQL responses flow naturally; no cast needed.
- Values flow **out** freely: every custom scalar **degrades to `String` at value-handoff boundaries** (Coercion rules, Exception 2), and `toString`/interpolation yield the raw string. String *methods* still need a String receiver — degrade through a slot or cast first (`(u :: String!).toUpper`).
- Equality, comparison against plain strings, list membership, records/lists all work: a custom scalar `==` a `String` compares the underlying strings (`urlValue == "https://…"`).
- The builtin `Path` scalar additionally carries its own methods and normalizes on construction — see stdlib.md.

### `ID!`
- Its own scalar, not a `String` alias: `String!` does not unify with `ID!` (no implicit interop). But string *literals* still coerce into `ID!` slots (`idSlot: ID! = "abc"`).
- Scalar fields are **invariant** across interface implementation — `id: String!` does not satisfy `id: ID!` (see objects.md variance).
