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

### Degrading to `String`

```dang
scalar URL

let u: URL! = "https://example.com/a"

# a scalar value degrades to String! at value-handoff boundaries — typed slots,
# call args, returns, reassignment, ::, case-clause values — since it is a
# string underneath
let s: String! = u

shout(msg: String!): String! { msg.toUpper }
shout(u)                        # degrades into the String! argument → "HTTPS://EXAMPLE.COM/A"

u == "https://example.com/a"    # compares by underlying string → true
`link: ${u}`                    # interpolation / toString yield the bare string
(u :: String!).toUpper          # a String *method* needs a String receiver: degrade first
```

## Scalar bodies {#scalar-bodies}

```dang
scalar Slug {
  # new() runs at every materialization — a string literal in a Slug! slot, a
  # `:: Slug!` cast, codec decode, and the derived Slug(...) constructor — and
  # returns the canonical underlying String!, which the runtime wraps back up
  new(raw: String!) {
    raw.trimSpace.toLower.replace(" ", "-")
  }

  # members are methods only (a scalar holds no state beyond its string); inside
  # one, `self` is the scalar value — degrade it to String! via a slot or cast
  words: [String!]! { (self :: String!).split("-") }
  first: String! { self.words[0] ?? "" }   # bare sibling call re-dispatches on self
}

# the scalar's name doubles as a constructor for any String expression
Slug("  Hello World  ")             # Slug("hello-world")
Slug("A b") == Slug("a-b")          # true — normalization makes equality semantic
Slug("one two three").words         # ["one", "two", "three"]
Slug("one two three").first         # "one"
```

```dang
scalar Upper {
  # a raise in the hook makes a bad value recoverable at the offending slot
  new(raw: String!) {
    if (raw != raw.toUpper) { raise `not upper: ${raw}` }
    raw
  }

  # a self { } block adds statics, reached as Upper.member (see Static members)
  self {
    of(s: String!): Upper! { Upper(s.toUpper) }
  }
}

let ok: Upper! = "HELLO"            # Upper("HELLO")
Upper("nope") rescue "rejected"     # "rejected" — construction is recoverable
Upper.of("mixed Case")             # Upper("MIXED CASE")
```

- a scalar's `self { }` block declares statics exactly like a `type`'s (see [#static-members])

## Built-in scalars

- `Int!`, `Float!`, `String!`, `Boolean!`, `ID!` (see [#nullability])
- `ID!` is its own scalar, not a `String` alias: `String!` does not unify with `ID!` (no implicit interop) — but string *literals* still coerce into `ID!` slots (`idSlot: ID! = "abc"`)
- `Path!` and `Regexp!` are scalars carrying their own methods and a construction hook — `Path` normalizes on construction, `Regexp` compiles its pattern. `Path` is declared in the prelude with a scalar body (see [#scalar-bodies]); full method signatures for both are in [#stdlib]

> Meta: scalar fields are invariant across interface implementation — `id: String!` does not satisfy `id: ID!`. Cross-ref the variance rules in [#interfaces-unions].

> See also: enum values and custom scalars declared in an imported schema are reached through the module name (see [#modules]); GraphQL representations are covered in [#interop].
