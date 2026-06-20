\use-plugin{dang}

# GraphQL interop {#interop}

> Meta: this is the page that justifies the language. Lead with "schema-as-stdlib" — when you `import Dagger`, every type and root function in the schema becomes part of the language. Import wiring (`dang.toml`, `import`) lives in [#modules]; type machinery in [#nullability]; record/object access in [#objects].

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
user.{{ name, email, posts.{{ title, createdAt }} }}
```

- multi-field selection uses double braces `.{{ }}`, mirroring record literals `{{ }}` (both infer to the same anonymous structural type)
- desugars to a single GraphQL query — the headline feature
- nested selections work to arbitrary depth
- arguments on nested fields:

```dang
user.{{ posts(first: 5).{{ title }} }}
```

- positional args work in nested selections too: `users.{{ posts(1).{{ ... }} }}`
- selection on a nullable receiver **short-circuits**: if `user` is `null`, `user.{{ name }}` is `null` (not an error), and the result type is nullable
- the result is a record (`{{ ... }}`); access fields by name; see [#objects]
- after selection you only have the fields you selected: accessing an unselected field is a *compile* error (`field "lives" not found in Cat`), even after a `case` narrows the type
- `.{{ }}` selection is navigation (it reads fields), so it short-circuits on null; the single-brace dot-block `.{ }` is block *application*, so it passes the receiver — null included — into the block. Same `.`-brace surface, different jobs; see [#blocks]'s [#dot-block]

## Inline fragments

```dang
node(id: "x").{{
  ... on User { name, email }
  ... on Post { title }
}}
```

- type-conditional selection on unions and interfaces ([#interfaces-unions])
- selects different field sets per concrete type; applies to a single value or to a list (maps over elements)
- type conditions resolve against the receiver's (GraphQL) schema, not a local type that shadows the name
- can nest: `... on Post { title, author.{{name}} }`, and selections-of-selections `edges.{{ node.{{ ... on User { ... } }} }}`
- the union-type result narrows in `case` (see [#interfaces-unions])

### Lazy inline fragments

- the lazy form `... on User` (no field block) narrows the value to that type without selecting fields, returning a chainable lazy value / typed reference
- works on a single value or a list (`pets.{{ ... on Cat, ... on Dog }}` — comma- or newline-separated); multiple lazy fragments narrow a union
- a narrowing that doesn't match returns `null`: `cat.{{... on Dog}} == null` (e.g. `node(...).{{... on Post}}` when the node is a User)
- the non-null form `... on User!` asserts and unwraps; a mismatch is a *runtime* error: `inline fragment type assertion failed: expected one of Cat, got Dog`
- useful for narrowing a GraphQL interface/union value before chaining (`node(id).{{... on User}}.name`)

## Lists of objects

```dang
users.{{ name, email }}
```

- applies the selection elementwise; result is `[ {{ name, email }} ]` (a list of records)
- index into the result to force it: `users.{{name}}[0].name`; see [#collections]

## Mutations

- root `Mutation` fields are functions like queries
- the result is whatever the schema declares
- side effects happen when the call executes, which is when its value is **forced**
- the laziness/forcing rules below apply to mutations exactly as they do to queries

> Laziness: GraphQL field access in Dang is lazy. A `GraphQLValue` accumulates a query chain (`.field`, `.{{...}}` selections, args); no request is sent until the value is **forced** — i.e. materialized at an expected-type boundary (assertion, `print`, assignment to a typed field, indexing into a result, etc.). Forcing runs the built-up selection as a single `Execute` against the endpoint. This is the desugaring that makes `user.{{ name, posts.{{ title }} }}` one round-trip. See [#mutation] for how forcing interacts with side effects.

## Object equality

- `==`/`!=` compare GraphQL objects by **reference identity** — there's no network call. A GraphQL object's identity is the query that produced it, so the same handle equals itself but two independent constructions don't, even when they denote the same server object: `primaryUser == user(id: "1")` is `false`
- to ask whether two objects are the *same server entity*, compare an identifying field explicitly — `a.id == b.id` — which forces the necessary fetch where you can see it, instead of hiding I/O inside `==`
- a `.{{ }}` selection is different: it materializes an **anonymous record**, which compares by **value**, so two selections with the same fields and equal values are equal (see [#operators])

## Errors from the server

- non-null violations and GraphQL errors raise — catchable via `try`/`catch` (see [#errors])

## Talking to multiple endpoints

- one `[imports.Name]` per endpoint; each becomes its own qualified namespace (config in [#modules])
- types from different endpoints are *distinct* even if they share a name (e.g. `Test.User` ≠ `Other.User`)
- a bare unqualified name provided by two imports is ambiguous — must qualify (see [#modules])

