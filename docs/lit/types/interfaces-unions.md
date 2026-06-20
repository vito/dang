\use-plugin{dang}

# Interfaces and unions {#interfaces-unions}

> Meta: these align directly with GraphQL interfaces and unions, so users coming from a schema will recognize them. Worth saying that up front to set expectations.

- interfaces and unions map 1:1 to their GraphQL counterparts; a schema's interfaces/unions are available as [#nullability] values
- both are discriminated with `case` (see [#control-flow]) and, for GraphQL values, with inline fragments (see [#interop])
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

- covering every member is exhaustive: no `else` needed, and the result stays non-null
- a `case` missing some members still type-checks, but a value that matches no arm yields `null`, so the case's type is nullable
- add `else => ...` as a catch-all to cover unmatched members and keep the result non-null

### Inline fragments

> Meta: inline fragments are selection syntax, so the full treatment — the field-selecting form, the lazy narrowing form, null/assertion behavior — lives with the rest of selection in [#interop]. Keep this as a pointer, not a second copy.

```dang
pets.{{
  ... on Cat {{ name, lives }}
  ... on Dog {{ name, tricks }}
}}
```

- selects different field sets per concrete type; works on single values and lists
- the lazy form (`... on Cat`, no field block) narrows without selecting, and `... on Cat!` asserts
- see [#interop] for the full rules

## Interface vs. union vs. enum

> Meta: newcomers conflate these; a one-line contrast keeps them straight. (enums live in [#enums-scalars].)

| | what it is | members | discriminate with | open to extension |
|---|---|---|---|---|
| interface | a shared field contract | object/interface types that *implement* it | `case` type patterns, inline fragments | yes — any type can implement |
| union | a closed set of object types | object types only, listed explicitly | `case` type patterns, inline fragments | no — fixed member list |
| enum | a closed set of named constants | bare identifiers (`RED`) | `case` value patterns | no — fixed value list |
