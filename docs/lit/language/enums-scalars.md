\use-plugin{dang}

# Enums and scalars {#enums-scalars}

> Meta: split this 50/50 — both feel obvious on the page but trip people up in real code (enum literal vs. string, scalar coercion timing).

## Enums

### Declaration

```dang
enum Color { RED, GREEN, BLUE }
```

### Values

- accessed as `Color.RED`
- unqualified `RED` works if no shadowing in scope (see [modules](./modules.md))
- `Color.values` returns `[Color!]!` — all members

### As function arguments

```dang
usersByStatus(status: Status.ACTIVE)
```

### In `case`

```dang
case (c) {
  Color.RED => "stop"
  Color.GREEN => "go"
}
```

### From strings (`::`)

- `"RED" :: Color!` — runtime-validated coercion

## Custom scalars

### Declaration

```dang
scalar Email
scalar JSON
```

### Coercion

- string literals → scalar at call boundaries (TBD — moving to explicit-only, see `soundness.md`)
- non-literal coercion: `someUrl :: URL!`
- scalars from GraphQL responses flow naturally; no cast needed

### Treating scalars as opaque

- equality, comparison, list membership work
- no string operations unless first cast back to `String`

> Meta: worth a callout on the `::` rule — that's the durable migration target even if today's behavior is looser.

## Built-in scalars

- `Int!`, `Float!`, `String!`, `Boolean!`, `ID!`
- `ID!` is treated as a scalar, not a `String` alias (no implicit interop)
