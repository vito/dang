\use-plugin{dang}

# Enums and scalars {#enums-scalars}

> Meta: split this 50/50 — both feel obvious on the page but trip people up in real code (enum literal vs. string, scalar coercion timing).

## Enums

### Declaration

```dang
enum Color { RED GREEN BLUE }
```

- members are bare `CAPS` identifiers, separated by whitespace/newlines — NOT commas (`RED, GREEN` is a syntax error)

### Values

- accessed as `Color.RED` — always qualified by the enum name
- the bare value (`RED`) is NOT in scope; an enum value is a field on the enum module (see [#modules]), not a top-level binding — `RED` alone → `"RED" not found`, including inside `case`
- `Color.values` returns `[Color!]!` — all members (a real list: supports `.length`, `.contains`, indexing)
- equality compares by value; values of different enums / different members are not equal

### As function arguments

```dang
usersByStatus(status: Status.ACTIVE)
```

- also flows into input objects: `UserSort(field: UserSortField.NAME, direction: SortDirection.DESC)`

### In `case`

```dang
case (c) {
  Color.RED => "stop"
  Color.GREEN => "go"
}
```

- value patterns are the qualified members; use `else` for a catch-all (no static exhaustiveness check)

### From strings (`::`)

- `"RED" :: Color!` — runtime-validated coercion
- an invalid value fails at runtime: `"NOPE" :: Status!` → `invalid enum value "NOPE" for Status`

## Custom scalars

### Declaration

```dang
scalar Email
scalar JSON
```

### Coercion

- *literal* expressions auto-coerce to a compatible scalar at value-handoff boundaries (call args, typed slots, list literals, returns). This covers string literals, list literals, and backtick templates — even templates with `${...}` interpolations.
- a *non-literal* String (a variable or `a + b`) does NOT auto-coerce — it needs an explicit `::` cast: `fetchURL(url: myUrl :: URL!)`, `(base + domain) :: URL!`. Without it: `cannot use String! as URL!`.
- list merging is pure / invariant: `["abc" :: ID!, "def"]` → `no common type between String! and ID!` (the plain literal doesn't widen to `ID!`)
- runtime materialization only supports `String -> custom scalar`; e.g. `42 :: URL!` is a type error (`Int!` vs `URL!`)
- scalars from GraphQL responses flow naturally; no cast needed

### Treating scalars as opaque

- equality, comparison, list membership, use in records/lists all work
- no string operations unless first cast back to `String`

> Meta: the `::` rule is the durable contract — literals are a convenience, non-literal values always go through an explicit cast.

## Built-in scalars

- `Int!`, `Float!`, `String!`, `Boolean!`, `ID!` (see [#types])
- `ID!` is its own scalar, not a `String` alias: `String!` does not unify with `ID!` (no implicit interop) — but string *literals* still coerce into `ID!` slots (`idSlot: ID! = "abc"`)

> Meta: scalar fields are invariant across interface implementation — `id: String!` does not satisfy `id: ID!`. Cross-ref the variance rules in [#interfaces-unions].

> See also: enum values and custom scalars declared in an imported schema are reached through the module name (see [#modules]); GraphQL representations are covered in [#graphql].
