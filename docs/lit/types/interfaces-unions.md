\use-plugin{dang}
\literate-fences

# Interfaces and unions {#interfaces-unions}

> Meta: these align directly with GraphQL interfaces and unions, so users coming from a schema will recognize them. Worth saying that up front to set expectations.

Interfaces and unions work like their GraphQL counterparts, so a schema's
interfaces and unions are ordinary Dang types. Both are taken apart with
`case` (see [#control-flow]), and GraphQL values also with inline fragments
(see [#interop]).

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## Interfaces

### Declaration

```dang
interface Named {
  name: String!
}
```

### Implementation

```dang
type Person implements Named {
  name: String!
  age: Int!
}

type Company implements Named {
  name: String!
  size: Int!
}
```

- `implements A & B` for multiple
- field types must satisfy the interface (subtyping rules below)
- the built-in `Error` interface (see [#errors]) is an ordinary interface; user error types implement it

A method satisfies an interface field too. `Robot` computes its `name`:

```dang
type Robot implements Named {
  serial: Int!
  name: String! { `unit-${serial}` }
}

greet(n: Named!): String! { `hello, ${n.name}` }

[greet(Person("Ada", 36)), greet(Robot(7))]
```

Conformance is checked. A missing field, or one whose type doesn't fit, is
an error:

```dang-failure
type Rock implements Named { weight: Int! }
```

```dang-failure
type Pebble implements Named { name: Int! }
```

An interface's fields are readable by anyone holding the interface, so a
`let` member can't implement a public interface field (nor can a child
interface redeclare a parent's public field with `let`):

```dang-failure
type Secret implements Named { let name: String! = "classified" }
```

The interface name is also a value: its namespace, not an instance. It
can't be passed where an instance is expected, and no type pattern matches
it:

```dang-failure
greet(Named)
```

### Interface inheritance

```dang
interface Dated { createdAt: String! }

interface Author implements Named {
  name: String!
  email: String!
}

interface Story implements Named & Dated {
  name: String!
  createdAt: String!
  title: String!
}
```

A child interface must re-declare every field the parent declares, with
compatible types (the same variance rules as type implementations):

```dang-failure
interface Label implements Named { text: String! }
```

A value of the child interface type widens to the parent, and lists do too:

```dang
type Essay implements Story {
  name: String!
  createdAt: String!
  title: String!
}

let stories: [Story!]! = [Essay("Ada", "1843", "Notes")]
let everyone: [Named!]! = stories

everyone.map { _.name }
```

### Variance

**Return types** may be more specific (covariant). An interface field
`summary: String` (nullable) can be implemented as `summary: String!`
(non-null), but not the other way around:

```dang
interface Summarized { summary: String }
type Report implements Summarized { summary: String! = "all good" }
```

```dang-failure
interface Headlined { headline: String! }
type Draft implements Headlined { headline: String }
```

**Argument types** may be more general (contravariant): an implementation
can accept a nullable argument where the interface wants a non-null one.
Extra *optional* arguments are allowed too:

```dang
interface Formatter { format(input: String!): String! }

type Lenient implements Formatter {
  format(input: String, width: Int! = 10): String! { (input ?? "").padLeft(width) }
}

Lenient.format("hi") + "|"
```

- **block arguments** are arguments too: the implementation's block parameter must accept every block the interface's callers may pass, naming its parameters as the interface's does. An implementation may call its block with something more specific than the interface promises the block, not more general
- **list elements** are covariant in both nullability and type: `[String!]` satisfies `[String]`, and `[Dog!]` satisfies `[Animal!]`

**Scalar fields** are invariant: an `ID!` field does not satisfy a
`String!` interface field, even though an `ID!` value can be passed where a
`String!` is wanted (see [#enums-scalars]):

```dang-failure
interface Keyed { key: String! }
type Row implements Keyed { key: ID! }
```

### Pattern-matching an interface

```dang
describe(n: Named!): String! {
  case (n) {
    p: Person => `${p.name}, age ${p.age}`
    c: Company => `${c.name}, ${c.size} people`
    fallback: Named => fallback.name
  }
}

[describe(Person("Ada", 36)), describe(Company("Acme", 12)), describe(Robot(7))]
```

- `binding: Type` narrows; the binding is typed as the matched concrete/interface type inside the arm
- an interface pattern (`n: Named`) matches *any* implementer — useful as a catch-all after specific types
- see [#control-flow] for full `case` semantics; flow typing of the binding is covered in [#flow-typing]

The operand must be a union or an interface. A plain object type is already
known, so there's nothing to match:

```dang-failure
case (Person("Ada", 36)) { p: Person => p.name }
```

On a **GraphQL** interface or union value, type patterns match the object's
**concrete** type, whatever type the field that produced it declares,
nullable or not. With the bundled `Demo` schema (see [#interop]), whose
`favoriteNode` is a `Node!`:

```dang
import Demo

case (favoriteNode) {
  u: User => u.name
  p: Post => p.title + " by " + p.author.name
  else => "something else"
}
```

- a GraphQL value that only knows its declared type (like `favoriteNode` above) costs one small query: `case` asks the server for the object's `id` and `__typename`, and the arm binds a handle of the concrete type, loaded by that ID, just like a checked nullable field ([#nullable-object-fields]). `p.title` then selects through the loader
- no query is sent when there's nothing to learn: without type patterns, for a value whose concrete type is already known (a checked nullable field, a `... on Post` narrowing), or when the first type pattern is satisfied by the declared type itself (`n: Node =>` first)
- the exception is a value reached through a **mutation**: asking would re-send the mutation, so it keeps its declared type. Only a pattern that type already satisfies (`n: Node`) can match it; the first type pattern that isn't raises `cannot match type pattern Post against a Node reached through mutation …`, instead of falling through to a later arm or `else`. To discriminate a mutation result, select the fields of every type it may be on the mutation call, which sends it once: `Mutation.touchNode(id: "Post:1").{{ ... on Post {{ title }}, ... on User {{ name }} }}`, then `case` on that (a selection without a fragment for the value's actual type fails)
- value patterns still compare against the original handle, so `case (f) { f => … }` matches as `f == f` does ([#interop])
- for when to reach for inline fragments instead, see [#discriminating]

## Unions

### Declaration

```dang
type Cat { name: String!; lives: Int! = 9 }
type Dog { name: String!; tricks: [String!]! = [] }

union Pet = Cat | Dog
```

Members must be object types that exist:

```dang-failure
enum Mood { HAPPY GRUMPY }
union Feeling = Cat | Mood
```

```dang-failure
union Aquarium = Cat | Fish
```

### Discriminating {#discriminating}

```dang
let pets: [Pet!]! = [Cat("Tom"), Dog("Rex", ["sit", "stay"])]

pets.map { pet =>
  case (pet) {
    c: Cat => `${c.name} has ${c.lives} lives`
    d: Dog => `${d.name} knows ${d.tricks.join(" and ")}`
  }
}
```

Covering every member is exhaustive: no `else` needed, and the result stays
non-null. A nullable operand also needs a `null =>` arm. A `case` that
leaves a member out still type-checks, but a value that matches no arm
yields `null`, so the result is nullable. Add `else => ...` to cover the
rest and keep it non-null:

```dang-failure
petName(p: Pet!): String! {
  case (p) { c: Cat => c.name }
}
```

Only members can be matched:

```dang-failure
case (pets[0]!) { p: Person => p.name }
```

- GraphQL union values match their concrete type, the same way as interface values (see above), at the cost of the same one small query
- for GraphQL values, `case` and an inline fragment (below) both discriminate, with different tradeoffs:
  - `.{{ ... on User {{ name }}, ... on Post {{ title }} }}` gets the type **and** the fields in **one** round-trip, and works over a list in that same round-trip. The result is a record holding just the selected fields
  - `case` costs a query to learn the type, then each arm's selections run on their own, but the binding is a full handle: keep chaining off it (`u.posts.{{ title }}`) or pass it along as an argument
  - so read fields with fragments, and branch with `case` when an arm does more with the object than read a few fields

### Inline fragments

> Meta: inline fragments are selection syntax, so the full treatment — the field-selecting form, the lazy narrowing form, null/assertion behavior — lives with the rest of selection in [#interop]. Keep this as a pointer, not a second copy.

An inline fragment selects a different set of fields for each concrete
type. It works on single values and lists:

```dang
let picked = pets.{{
  ... on Cat {{ name, lives }}
  ... on Dog {{ name, tricks }}
}}

JSON.encode(picked)
```

- the lazy form (`... on Cat`, no field block) narrows without selecting, and `... on Cat!` asserts
- see [#interop] for the full rules

## Interface vs. union vs. enum

> Meta: newcomers conflate these; a one-line contrast keeps them straight. (enums live in [#enums-scalars].)

| | what it is | members | discriminate with | open to extension |
|---|---|---|---|---|
| interface | a shared field contract | object/interface types that *implement* it | `case` type patterns, inline fragments | yes — any type can implement |
| union | a closed set of object types | object types only, listed explicitly | `case` type patterns, inline fragments | no — fixed member list |
| enum | a closed set of named constants | bare identifiers (`RED`) | `case` value patterns | no — fixed value list |
