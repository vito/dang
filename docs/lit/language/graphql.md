\use-plugin{dang}

# GraphQL interop {#graphql}

> Meta: this is the page that justifies the language. Lead with "schema-as-stdlib" — when you `import Dagger`, every type and root function in the schema becomes part of the language.

## Schema-as-stdlib

- every imported schema's types are first-class Dang types
- root `Query` and `Mutation` fields become callable functions
- enum and input types map directly
- custom scalars become Dang scalars

## Calling a field

```dang
let users = users(limit: 10)
```

- named args: as in the schema
- positional args: in schema-declared order
- a no-arg field calls automatically: `serverInfo.platform` (no parens)

## Multi-field selection

```dang
user.{ name, email, posts.{ title, createdAt } }
```

- desugars to a single GraphQL query — the headline feature
- nested selections work to arbitrary depth
- arguments on nested fields:

```dang
user.{ posts(first: 5).{ title } }
```

## Inline fragments

```dang
node(id: "x").{
  ... on User { name, email }
  ... on Post { title }
}
```

- type-conditional selection on unions and interfaces
- the union-type result narrows in `case`
- lazy form `... on User` (no block) for type-only assertion

## Lists of objects

```dang
users.{ name, email }
```

- applies the selection elementwise; result is `[ {{ name, email }} ]`

## Mutations

- root `Mutation` fields are functions like queries
- the result is whatever the schema declares
- side effects happen when the call executes, which is when its value is **forced**

> Meta: laziness vs. eagerness deserves a short paragraph. GraphQL queries in Dang are lazy — selections build up, the network call fires on materialization (assertion, print, assignment to typed slot, etc.). Confirm exact rules with the implementation.

## Errors from the server

- non-null violations and GraphQL errors raise — catchable via `try`/`catch`
- partial data with errors: TBD (verify behavior)

## Talking to multiple endpoints

- one `[imports.Name]` per endpoint; each becomes its own qualified namespace
- types from different endpoints are *distinct* even if they share a name
