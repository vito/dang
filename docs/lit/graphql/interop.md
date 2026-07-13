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

- multi-field selection and record literals are the **same construct**: the double braces `{{ }}`, the same anonymous structural type, and the same concurrent evaluation. A selection `recv.{{ name }}` is a record literal whose fields are read off `recv` — `{{ name: recv.name }}`
- desugars to a single GraphQL query — the headline feature
- a field may be **aliased** to rename it in the result, GraphQL-style — the alias goes before the colon: `user.{{ fullName: name, email }}` yields a record with keys `fullName` and `email`
- a bare field is shorthand for aliasing it to itself: `user.{{ name }}` means `user.{{ name: name }}`, exactly as `{{ name }}` is shorthand for `{{ name: name }}` in a record literal (see [#objects])
- aliases are emitted as real GraphQL aliases, so the **same field** can be selected more than once under different arguments: `user.{{ small: avatarUrl(size: 100), large: avatarUrl(size: 200) }}`
- `{{ }}` **always evaluates its fields concurrently**, failing fast on the first error (see [#syntax]): over a GraphQL receiver that is the single batched query above, over a plain object it is parallel Dang evaluation, and over a list it runs the elements in parallel
- a record literal is `{{ }}` with no receiver, so it has the same parallelism: `{{ users: users.{{ name }}, posts: posts.{{ title }} }}` sends both queries at once
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
  ... on User {{ name, email }}
  ... on Post {{ title }}
}}
```

- type-conditional selection on unions and interfaces ([#interfaces-unions])
- the field set is delimited by the same double braces as any other selection: `... on User {{ name, email }}`
- selects different field sets per concrete type; applies to a single value or to a list (maps over elements)
- type conditions resolve against the receiver's (GraphQL) schema, not a local type that shadows the name
- the type name may be module-qualified, matching an imported type: `... on Dagger.Editor` (or `... on Dagger.Editor!`)
- can nest: `... on Post {{ title, author.{{name}} }}`, and selections-of-selections `edges.{{ node.{{ ... on User {{ ... }} }} }}`
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

- a selection on a list **distributes over its elements**: `users.{{ name, email }}` selects those fields from every element, producing `[ {{ name, email }} ]` — a list of records
- this is the shape GraphQL itself produces: a selection set on a list-typed field applies to each element, so `users.{{ name, email }}` mirrors the response of `{ users { name email } }`
- `.{{ … }}` and `.field` do different jobs on a list. `.field` treats the **list** as the receiver and resolves a list method (`users.length`, `users.map { … }`); `.{{ … }}` reaches **into** each element. So `xs.{{ f }}` is not `xs.f` — the former is a list of `{{ f }}` records, the latter looks for a method `f` on the list itself
- nested selections distribute the same way: `users.{{ name, posts.{{ title }} }}` gives a list of records each holding its own list of post records
- the result records compare by **value** ([#operators]), so a list of projections can be compared, asserted on, or deduplicated directly: `users.{{ name }} == [{{ name: "Alice" }}, {{ name: "Bob" }}]`
- the result is an ordinary list — chain list operations on it (`users.{{ name }}.map { r => r.name }`) or index to force the query: `users.{{ name }}[0].name`; see [#collections]

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

- non-null violations and GraphQL errors raise — recoverable via `rescue` (see [#errors])

## Talking to multiple endpoints

- one `[imports.Name]` per endpoint; each becomes its own qualified namespace (config in [#modules])
- types from different endpoints are *distinct* even if they share a name (e.g. `Test.User` ≠ `Other.User`)
- a bare unqualified name provided by two imports is ambiguous — must qualify (see [#modules])

