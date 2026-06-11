\use-plugin{dang}

# Interfaces and unions {#interfaces-unions}

> Meta: these align directly with GraphQL interfaces and unions, so users coming from a schema will recognize them. Worth saying that up front to set expectations.

- interfaces and unions map 1:1 to their GraphQL counterparts; a schema's interfaces/unions are available as [#types] values
- both are discriminated with `case` (see [#control-flow]) and, for GraphQL values, with inline fragments (see [#graphql])
- the interface/union type itself is also a runtime value (`Named != null`, `Pet != null`)

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
```

- `implements A & B` for multiple
- field types must satisfy the interface (subtyping rules below)
- declared fields are zero-arg functions / fields (see [#objects]); a method field also satisfies an interface field
- the built-in `Error` interface (see [#errors]) is an ordinary interface; user error types implement it
- missing a required field → `object X is missing \`f(): T\`, required by interface I`
- a field whose type doesn't satisfy the interface → `field "f": type ... is not compatible with interface type ...`

### Interface inheritance

```dang
interface User implements Named { email: String! }
interface Post implements Named & Timestamped { title: String! }
```

- a child interface must re-declare (cover) every field the parent declares
- the child's field types must be compatible with the parent's (same variance rules as type implementations)
- a value of the child interface type widens to the parent interface type (and lists likewise: `[User!]!` → `[Named!]!`)

### Variance

- **return types** may be more specific (covariant): an interface field `getData: String` (nullable) can be implemented as `getData: String!` (non-null). Weakening (`String!` → `String`) is rejected: `return type String is not compatible with interface return type String! (covariance required)`
- **argument types** may be more general (contravariant): can accept nullable where the interface wants non-null (e.g. interface `process(input: String!)`, impl `process(input: String)`). Extra *optional* args are also allowed.
- **list elements** are covariant in both nullability and type: `[String!]` satisfies `[String]`, and `[Dog!]` satisfies `[Animal!]`
- **scalar fields** are invariant — `id: String!` does not satisfy `id: ID!` (distinct scalar types; see [#enums-scalars])

### Pattern-matching an interface

```dang
case (n) {
  p: Person => p.age
  c: Company => c.size
  fallback: Named => fallback.name
}
```

- `binding: Type` narrows; the binding is typed as the matched concrete/interface type inside the arm
- an interface pattern (`n: Named`) matches *any* implementer — useful as a catch-all after specific types
- the operand must be a union or interface; a type pattern on a plain object → `type pattern requires a union or interface operand`
- see [#control-flow] for full `case` semantics; flow typing of the binding is covered in [#flow-typing]

## Unions

### Declaration

```dang
union Pet = Cat | Dog
```

- members must be object types (no scalars/interfaces/enums) → `union member X must be an object type, got enum`
- members must exist → `union member X not found`
- only members may be matched in `case`: a non-member type pattern → `type X is not a member of union Pet`

### Discriminating

```dang
case (pet) {
  c: Cat => c.purr
  d: Dog => d.bark
}
```

- NOT statically exhaustive — a `case` missing some members type-checks fine
- a value that matches no arm is a *runtime* error: `no case clause matched the value`
- add `else => ...` as a catch-all to cover unmatched members

### Inline fragments

> Meta: inline fragments are the GraphQL selection syntax (see [#graphql]); they apply to single values *and* lists.

```dang
pets.{{
  ... on Cat { name, lives }
  ... on Dog { name, tricks }
}}
```

- selects different field sets per concrete type
- applies to a single value or to a list (maps over elements)
- after selection you only have the fields you selected: accessing an unselected field is a *compile* error (`field "lives" not found in Cat`), even after a `case` narrows the type
- type conditions resolve against the receiver's (GraphQL) schema, not a local type that shadows the name
- can nest: `... on Post { title, author.{{name}} }`, and selections-of-selections `edges.{{ node.{{ ... on User { ... } }} }}`

### Lazy inline fragments

- `... on Cat` (no field block) yields a typed reference / type assertion without selecting fields
- works on a single value or a list (`pets.{{ ... on Cat, ... on Dog }}` — comma- or newline-separated)
- non-matching assertion returns `null`: `cat.{{... on Dog}} == null`
- non-null form `... on Cat!` asserts and unwraps; a mismatch is a *runtime* error: `inline fragment type assertion failed: expected one of Cat, got Dog`
- useful for narrowing a GraphQL interface/union value before chaining (`node(id).{{... on User}}.name`)

## Interface vs. union vs. enum

> Meta: newcomers conflate these; a one-line contrast keeps them straight. (enums live in [#enums-scalars].)

| | what it is | members | discriminate with | open to extension |
|---|---|---|---|---|
| interface | a shared field contract | object/interface types that *implement* it | `case` type patterns, inline fragments | yes — any type can implement |
| union | a closed set of object types | object types only, listed explicitly | `case` type patterns, inline fragments | no — fixed member list |
| enum | a closed set of named constants | bare identifiers (`RED`) | `case` value patterns | no — fixed value list |
