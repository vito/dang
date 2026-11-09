\use-plugin{dang}
\literate-fences

# Enums and scalars {#enums-scalars}

> Meta: split this 50/50 — both feel obvious on the page but trip people up in real code (enum literal vs. string, scalar coercion timing).

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## Enums

### Declaration

An enum lists its members as bare `CAPS` names:

```dang
enum Color { RED GREEN BLUE }
```

Members are separated by spaces or newlines, not commas:

```dang-failure
enum Size { SMALL, LARGE }
```

### Values

A value is written with its enum's name, as `Color.RED`. An enum
value is a field on the enum (see [#modules]), not a top-level name, so a
bare `RED` isn't in scope, even inside `case`. (Enums imported from a schema
are the exception, below.)

```dang-failure
RED
```

`Color.values` lists every member, as a real `[Color!]!`:

```dang
Color.values
```

Two values of the same enum are equal when they're the same member. Values
of different enums can't be compared, even when the member names match:

```dang
enum Signal { RED AMBER GREEN }

Color.RED == Color.RED
```

```dang-failure
Color.RED == Signal.RED
```

A string literal compared with an enum value converts to it, as it does in
an argument, so it must name a member. `Color.RED` is still the clearer
spelling:

```dang
let shade = Color.BLUE

[shade == "BLUE", shade != "RED"]
```

A `String` held in a variable doesn't convert; cast it with `::` (below):

```dang
let picked = "BLUE"
```

```dang-failure
shade == picked
```

### As function arguments

An enum is an ordinary parameter type. A string literal naming a member
works too, and is checked before the script runs:

```dang
paint(c: Color!): String! { `painted ${c}` }

[paint(Color.GREEN), paint("BLUE")]
```

```dang-failure
paint("PURPLE")
```

Enums from a schema work the same way, including inside input objects. With
the bundled `Demo` schema (see [#interop]):

```dang
import Demo

usersByStatus(status: Status.ACTIVE).{{ name }}.map { _.name }
```

An imported enum's values are also in scope bare, as long as no other
import uses the same name (see [#modules]):

```dang
usersByStatus(status: PENDING).{{ name }}.map { _.name }
```

```dang
let byNameDesc = UserSort(field: UserSortField.NAME, direction: SortDirection.DESC)

sortedUsers(sort: byNameDesc).{{ name }}.map { _.name }
```

### In `case`

Value patterns are the qualified members (or string literals). A `case` that
names every member is exhaustive: it needs no `else`, and its result stays
non-null:

```dang
action(c: Color!): String! {
  case (c) {
    Color.RED => "stop"
    Color.GREEN => "go"
    Color.BLUE => "wait"
  }
}

[action(Color.RED), action(Color.BLUE)]
```

Leave a member out and the `case` can fall through to `null`, so it no
longer fits a `String!`. Add the member, or an `else`:

```dang-failure
caution(c: Color!): String! {
  case (c) {
    Color.RED => "stop"
    Color.GREEN => "go"
  }
}
```

A nullable operand also needs a `null =>` arm (see [#control-flow]).

### From strings (`::`)

A string that isn't a literal becomes an enum value with a `::` cast. The
cast checks the string when it runs:

```dang
let favorite = "GREEN"

favorite :: Color!
```

```dang-failure
let wanted = "PURPLE"
wanted :: Color!
```

## Custom scalars

### Declaration

```dang
scalar Email
scalar Semver
```

A custom scalar is a string with its own type. An `Email!` and a `String!`
are different types.

### Coercion

A *literal* converts to a compatible scalar wherever a value is handed over:
call arguments, typed slots, list and map literals, and return values. That
covers string literals, list and map literals, and templates, even ones with
`${...}` inside:

```dang
sendTo(to: Email!): String! { `mail for ${to}` }

let name = "grace"

[sendTo("ada@example.com"), sendTo(`${name}@example.com`)]
```

A string that isn't a literal, such as a variable or `a + b`, doesn't
convert:

```dang
let address = "ada@example.com"
```

```dang-failure
sendTo(address)
```

It needs an explicit `::` cast:

```dang
sendTo(address :: Email!)
```

List literals don't convert their elements to match each other, so a plain
string doesn't join a list of `Email!`s:

```dang-failure
["ada@example.com" :: Email!, "grace@example.com"]
```

Only a `String` converts to a scalar. `42 :: Email!` is a type error:

```dang-failure
42 :: Email!
```

Scalars from GraphQL responses need no cast. Demo's `homepage` is a `URL!`,
and `fetchURL` takes one:

```dang
fetchURL(url: homepage)
```

Some APIs define custom scalars (like a `JSON` scalar) whose values are
numbers, booleans, or even structured JSON. Dang keeps those values as they
are: passed back as an argument, one is sent unchanged, and where a `String`
is expected (degrading, interpolation, `toString`) it reads as its JSON
text. Demo's `Opaque` scalar is one of these:

```dang
[describeOpaque(value: opaqueNumber), `object: ${opaqueObject}`]
```

Dang's own `JSON` goes further: it is the type for data of any shape.
Decoding into it accepts any value, kept as its JSON text (see
[#json-values]). A schema's `scalar JSON` stays a schema scalar like the
ones above, its values as the server sends them.

### Treating scalars as opaque

Scalars support equality and list membership, and can go in records and
lists. They have no string methods and no ordering:

```dang
let ada: Email! = "ada@example.com"

[ada == ada, [ada].contains(ada)]
```

```dang-failure
ada.toUpper
```

> Meta: the `::` rule is the durable contract — literals are a convenience, non-literal values always go through an explicit cast.

### Degrading to `String`

A scalar value turns back into a `String!` wherever a value is handed over:
typed slots, call arguments, return values, reassignment, `::`, and
`case`-clause values. It's a string underneath:

```dang
let plain: String! = ada

shout(msg: String!): String! { msg.toUpper }

[plain, shout(ada), `to: ${ada}`]
```

A string literal compared with a scalar converts to it, like a literal
argument. A `String` held in a variable doesn't; cast it first:

```dang
[ada == "ada@example.com", ada == (plain :: Email!)]
```

```dang-failure
ada == plain
```

A `String` *method* needs a `String` receiver, so degrade first:

```dang
(ada :: String!).toUpper
```

## Scalar bodies {#scalar-bodies}

A scalar can have a body. Its `new()` hook takes the raw string and returns
the canonical one. It runs every time a value is made: a string literal in
a `Slug!` slot, a `:: Slug!` cast, a codec decode, and the `Slug(...)`
constructor that declaring `new()` adds:

```dang
scalar Slug {
  new(raw: String!) {
    raw.trimSpace.toLower.replace(" ", "-")
  }

  words: [String!]! { (self :: String!).split("-") }
  first: String! { words[0] ?? "" }
}

Slug("  Hello World  ")
```

Since every value goes through `new()`, equality compares the normalized
strings:

```dang
Slug("A b") == Slug("a-b")
```

Members must be methods: a scalar holds nothing but its string. Inside a
method, `self` is the scalar value, and sibling methods can be called bare:

```dang
Slug("one two three").first
```

```dang-failure
scalar Version { major: Int! = 1 }
```

A `raise` in the hook rejects a bad value, and `rescue` can recover from it
at the spot where the value was made:

```dang
scalar Upper {
  new(raw: String!) {
    if (raw != raw.toUpper) { raise `not upper: ${raw}` }
    raw
  }

  self {
    of(s: String!): Upper! { Upper(s.toUpper) }
  }
}

Upper("nope") rescue { e: Error => e.message }
```

A scalar's `self { }` block declares statics exactly like a `type`'s (see
[#static-members]):

```dang
Upper.of("mixed Case")
```

## Built-in scalars

- `Int!`, `Float!`, `String!`, `Boolean!`, `ID!` (see [#nullability])
- `Path!` and `Regexp!` are scalars carrying their own methods and a construction hook — `Path` normalizes on construction, `Regexp` compiles its pattern. `Path` is declared in the prelude with a scalar body (see [#scalar-bodies]); full method signatures for both are in [#stdlib]

`ID!` is its own scalar, not a `String` alias. String literals still
convert into `ID!` slots, but a `String!` value doesn't:

```dang
let key: ID! = "abc"
let text = "abc"
```

```dang-failure
let other: ID! = text
```

`Path` normalizes as it's made:

```dang
"src/../docs/./index.md" :: Path!
```

> Meta: scalar fields are invariant across interface implementation — `id: String!` does not satisfy `id: ID!`. Cross-ref the variance rules in [#interfaces-unions].

> See also: enum values and custom scalars declared in an imported schema are reachable through the module name (`Demo.Status.ACTIVE`), unqualified (`Status.ACTIVE`), or, for enum values, bare when no other import uses the name (see [#modules]); GraphQL representations are covered in [#interop].
