\use-plugin{dang}
\literate-fences

# GraphQL interop {#interop}

> Meta: this is the page that justifies the language. Lead with "schema-as-stdlib" — when you `import Dagger`, every type and root function in the schema becomes part of the language. Import wiring (`dang.toml`, `import`) lives in [#modules]; type machinery in [#nullability]; record/object access in [#objects].

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.
>
> They run against `Demo`, a small GraphQL schema bundled with the docs and
> served in-process. Each request it receives is printed as `→ query …`, so
> you can see exactly what a snippet sends.

## Schema-as-stdlib

- every imported schema's types are first-class Dang types
- root `Query` and `Mutation` fields become callable functions
- enum and input types map directly
- custom scalars become Dang scalars

```dang
import Demo

hello(name: "Dang")
```

## Calling a field

Arguments are named as in the schema, or positional in the schema's order:

```dang
posts(authorId: "User:2", limit: 1).{{ title }}.map { _.title }
```

```dang
posts(null, 2).{{ title }}.map { _.title }
```

A field with no required arguments is called without parentheses:

```dang
serverInfo.version
```

## Multi-field selection

A selection reads several fields in one GraphQL query:

```dang
let ada = user(id: "User:1").{{ name, emails }}

ada.emails
```

- multi-field selection and record literals are the **same construct**: the double braces `{{ }}`, the same anonymous structural type, and the same concurrent evaluation. A selection `recv.{{ name }}` is a record literal whose fields are read off `recv` — `{{ name: recv.name }}`
- desugars to a single GraphQL query — the headline feature
- `{{ }}` **always evaluates its fields concurrently**, failing fast on the first error (see [#syntax]): over a GraphQL receiver that is the single batched query above, over a plain object it is parallel Dang evaluation, and over a list it runs the elements in parallel
- so fields produce values rather than write shared state: writing a binding declared outside a field, or calling a closure from outside it that writes, is a compile error (see [#concurrent-fields])
- a record literal is `{{ }}` with no receiver, so it has the same parallelism: `{{ users: users.{{ name }}, posts: posts.{{ title }} }}` sends both queries at once
- nested selections work to arbitrary depth

A field may be **aliased** to rename it in the result, GraphQL-style: the
alias goes before the colon. A bare field is shorthand for aliasing it to
itself, so `user.{{ name }}` means `user.{{ name: name }}`, exactly as `{{
name }}` is shorthand for `{{ name: name }}` in a record literal (see
[#objects]):

```dang
user(id: "User:1").{{ fullName: name }}.fullName
```

Aliases are sent as real GraphQL aliases, so the **same field** can be
selected more than once with different arguments. Nested fields take
arguments like any call:

```dang
let pages = user(id: "User:1").{{
  earliest: posts(first: 1).{{ posts.{{ title }} }}
  latest: posts(last: 1).{{ posts.{{ title }} }}
}}

[pages.earliest.posts[0]!.title, pages.latest.posts[0]!.title]
```

A selection on a nullable receiver **short-circuits**: if the receiver is
`null`, the selection is `null` (not an error), and its type is nullable.
`maybeUser` returns `null` for an unknown ID:

```dang
maybeUser(id: "User:99").{{ name }}
```

The result is a record holding only what you selected. Reading a field you
didn't select is a *compile* error, even after a `case` narrows the type:

```dang-failure
user(id: "User:1").{{ name }}.emails
```

`.{{ }}` selection is navigation (it reads fields), so it short-circuits on
null; the single-brace dot-block `.{ }` is block *application*, so it passes
the receiver — null included — into the block. Same `.`-brace surface,
different jobs; see [#blocks]'s [#dot-block].

## Inline fragments

An inline fragment selects fields by concrete type, on unions and
interfaces ([#interfaces-unions]). Fragments are separated by newlines or
commas, and the result narrows in `case`:

```dang
let hit = node(id: "Post:1").{{
  ... on User {{ name }}
  ... on Post {{ title }}
}}

case (hit) {
  u: User! => `user ${u.name}`
  p: Post! => `post ${p.title}`
}
```

- the field set is delimited by the same double braces as any other selection: `... on User {{ name, email }}`
- selects different field sets per concrete type; applies to a single value or to a list (maps over elements)
- type conditions resolve against the receiver's (GraphQL) schema, not a local type that shadows the name
- the type name may be module-qualified, matching an imported type: `... on Dagger.Editor` (or `... on Dagger.Editor!`)
- can nest: `... on Post {{ title, author.{{name}} }}`, and selections-of-selections `edges.{{ node.{{ ... on User {{ ... }} }} }}`

### Lazy inline fragments

The lazy form `... on Post` (no field block) narrows the value to that type
without selecting anything. You can keep chaining off the result:

```dang
node(id: "Post:1").{{ ... on Post }}.title
```

A narrowing that doesn't match is `null`, and so is one on a `null` value:

```dang
node(id: "Post:1").{{ ... on User }} == null
```

The non-null form `... on User!` asserts and unwraps instead. A mismatch, or
a `null` value, is a runtime error:

```dang-failure
node(id: "Post:1").{{ ... on User! }}.name
```

- works on a single value or a list (`pets.{{ ... on Cat, ... on Dog }}` — comma- or newline-separated); multiple lazy fragments narrow a union
- on a value already known to be of a concrete type (a checked nullable field, a `case` binding), a narrowing is matched without a request

## Lists of objects

A selection on a list **distributes over its elements**. This is the shape
GraphQL itself produces: `users.{{ name }}` mirrors the response of `{
users { name } }`, a list of records:

```dang
users.{{ name }} == [{{ name: "John Doe" }}, {{ name: "Jane Smith" }}]
```

The result records compare by **value** ([#operators]), as above. The result
is an ordinary list: index it, or chain list methods (see [#collections]):

```dang
users.{{ name }}.map { _.name }
```

Nested selections distribute the same way, giving each record its own list:

```dang
users.{{ name, posts.{{ posts.{{ title }} }} }}.map { u => `${u.name}: ${u.posts.posts.length}` }
```

`.{{ … }}` and `.field` do different jobs on a list: `.{{ … }}` reaches
**into** each element, while `.field` looks for a list method. List methods
like `.length` and `.map` aren't available on a list of GraphQL objects
until you select the fields you need:

```dang-failure
users.length
```

```dang
users.{{ id }}.length
```

## Mutations

- root `Mutation` fields are functions like queries
- the result is whatever the schema declares
- the laziness/forcing rules below apply to mutations as they do to queries, with one exception: nullable object fields reached through a mutation are not checked for null (see [#nullable-object-fields])

Side effects happen when the call executes, which is when its value is
**forced**, and every forced selection off an object-returning mutation
sends it again. Demo's `touchNode` changes nothing, so it's safe to watch:
`let touched = …` sends nothing, and each read of `touched.id` sends the
mutation once more:

```dang
let touched = Mutation.touchNode(id: "User:1")

[touched.id, touched.id]
```

Select everything you need in one `.{{ }}` on the call to run it exactly
once:

```dang
Mutation.touchNode(id: "User:1").{{ id }}.id
```

> Laziness: GraphQL field access in Dang is lazy. A `GraphQLValue` accumulates a query chain (`.field`, `.{{...}}` selections, args); no request is sent until the value is **forced** — i.e. materialized at an expected-type boundary (assertion, `print`, assignment to a typed field, indexing into a result, etc.). Forcing runs the built-up selection as a single `Execute` against the endpoint. This is the desugaring that makes `user.{{ name, posts.{{ title }} }}` one round-trip. Two exceptions send a small query early: a handle that may be `null` is checked when it escapes the chain (see [#nullable-object-fields]), and `case` asks for the concrete type of an interface or union value it matches type patterns against (see [#interfaces-unions]). See [#mutation] for how forcing interacts with side effects.

## Nullable object fields {#nullable-object-fields}

A field returning a **nullable** object, interface or union (`manager:
User`, not `User!`) may be `null`. So a handle to it is checked when it
**escapes** the chain: when it's bound with `let`, passed as an argument,
returned, compared, and so on. The check is one small query, so the value is
either `null` or a handle known to be non-null, and `== null`, `??`, `!`,
`null` patterns in `case`, and flow narrowing ([#flow-typing]) all behave as
written:

```dang
let boss = user(id: "User:2").manager
if (boss == null) {
  raise "no manager"
}

boss.name
```

The check fetched the manager's `id`, and `boss.name` then started from
`node(id:)` rather than re-running the chain.

A field that is only **selected through** isn't checked: whatever runs the
chain reveals the null itself. User 1 has no manager, so this is `null`, in
one round trip:

```dang
user(id: "User:1").manager.name
```

- anything selected off an unchecked nullable field may be null too, even a non-null field: `let d = a.maybeB.c.d` sends one check, for `d`, at the end of the chain; `a.maybeB.c.name` sends none
- the check fetches the object's `id` along with its `__typename`, when the type has a non-null `ID` id (not `String`, which doesn't promise IDs unique across types). The object is then **loaded by that ID**, the way the Dagger Go SDK does it: later selections start from the loader instead of re-running the chain (`node(id: "…") { ... on Agent { name } }`), and passing the object as an ID argument costs no extra query
- the loader is the schema's Relay-style `node(id: ID!)` root field, which Dagger engines expose. Without one the original chain is kept, with its ID remembered. Without an `id`, the check is just `__typename`. Older Dagger schemas' per-type `<T>ID` scalars and `load<T>FromID` loaders are not supported: a `<T>ID` is just a scalar
- the checked handle has the object's **concrete** type, even when the field returns an interface or union. `case` type patterns match it without another query; on any GraphQL interface or union value, nullable or not, they match the concrete type (see [#interfaces-unions])
- **non-null** object fields (`User!`) on a chain without nullable steps can't be null, so they stay lazy and cost nothing until forced
- this matches the Dagger SDKs, which also run nullable object fields right away (fetching their IDs) and keep non-null ones lazy
- **mutations:** nullable object fields reached through a mutation are *not* checked, because every query built on a mutation re-sends it. They stay lazy and never compare equal to `null`; select a leaf or `.{{ }}` through them to see the null. For the same reason `case` doesn't ask for the concrete type of an interface or union value reached through a mutation; a type pattern it can't decide raises (see [#interfaces-unions])

## Object equality

`==` and `!=` compare GraphQL objects by **reference identity**. The
comparison itself sends nothing (though a nullable operand is checked for
null first, as above). A GraphQL object's identity is the query that
produced it, so the same handle equals itself, but two independent queries
don't, even when they denote the same server object. To ask whether two
objects are the *same server entity*, compare an identifying field, which
runs the fetch where you can see it. `primaryUser` is a non-null `User!`, so
only the `id` reads send anything here:

```dang
let first = primaryUser

[first == first, first == primaryUser, first.id == user(id: "User:1").id]
```

A `.{{ }}` selection is different: it makes an **anonymous record**, which
compares by **value** (see [#operators]):

```dang
primaryUser.{{ name }} == user(id: "User:1").{{ name }}
```

## Errors from the server

Non-null violations and GraphQL errors raise, and `rescue` can recover from
them (see [#errors]):

```dang-failure
user(id: "User:99").name
```

```dang
user(id: "User:1").alwaysFails rescue { e: GraphQLError! => e.message }
```

## Talking to multiple endpoints

- one `[imports.Name]` per endpoint; each becomes its own qualified namespace (config in [#modules])
- types from different endpoints are *distinct* even if they share a name (e.g. `Test.User` ≠ `Other.User`)
- a bare unqualified name provided by two imports is ambiguous — must qualify (see [#modules])

The qualified name always works, even with only one import:

```dang
let viewer: Demo.User! = primaryUser
viewer.name
```
