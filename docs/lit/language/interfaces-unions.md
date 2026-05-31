\use-plugin{dang}

# Interfaces and unions {#interfaces-unions}

> Meta: these align directly with GraphQL interfaces and unions, so users coming from a schema will recognize them. Worth saying that up front to set expectations.

## Interfaces

### Declaration

```dang
interface Named {
  pub name: String!
}
```

### Implementation

```dang
type Person implements Named {
  pub name: String!
  pub age: Int!
}
```

- `implements A & B` for multiple
- field types must satisfy the interface (subtyping rules below)

### Interface inheritance

```dang
interface User implements Named { pub email: String! }
```

### Variance

- **return types** may be more specific (covariant): an interface field `name: String` can be implemented as `name: String!`
- **argument types** may be more general (contravariant): can accept nullable where the interface wants non-null
- **list elements** are covariant: `[Dog!]` satisfies `[Animal!]`
- **scalar fields** are invariant — `id: String!` does not satisfy `id: ID!`

### Pattern-matching an interface

```dang
case (n) {
  p: Person => p.age
  c: Company => c.size
  fallback: Named => fallback.name
}
```

## Unions

### Declaration

```dang
union Pet = Cat | Dog
```

- members must be object types (no scalars/interfaces)

### Discriminating

```dang
case (pet) {
  c: Cat => c.purr
  d: Dog => d.bark
}
```

- exhaustive over members; an `else` covers any missed ones

### Inline fragments on lists

```dang
pets.{
  ... on Cat { name, lives }
  ... on Dog { name, tricks }
}
```

- selects different field sets per concrete type
- unmatched types become `null` in the result

### Lazy inline fragments

- `... on Cat` (no field block) yields a typed reference without selecting fields
- useful as a type assertion in queries

> Meta: this page deserves a small comparison table — interface vs. union vs. enum — because newcomers conflate them all the time.
