\use-plugin{dang}
\literate-fences

# Directives {#directives}

> Meta: short page. The interesting bit is that Dang directives are real declarations, type-checked, not magic comments — that's the differentiator from most languages with comment-pragmas.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## What a directive is

- a typed annotation attachable to a declaration or argument
- a *real declaration* — type-checked args and defaults — not a comment pragma
- declared like GraphQL directives (see [#interop]):

```dang
directive @deprecated(reason: String = "No longer supported") on FIELD_DEFINITION | OBJECT
directive @experimental on FIELD_DEFINITION | ARGUMENT_DEFINITION | SCALAR
directive @auth(role: String!) on FIELD_DEFINITION
directive @cache(ttl: Int! = 300, key: String) on FIELD_DEFINITION
directive @check on FIELD_DEFINITION
```

- a directive with no args omits the `()`
- arg list is the same as a function's: required (`role: String!`), optional (`key: String`), and defaulted (`ttl: Int! = 300`)

## Locations (`on ...`)

- `FIELD_DEFINITION`, `OBJECT`, `ARGUMENT_DEFINITION`, `INTERFACE`, `UNION`, `ENUM`, ... (mirror GraphQL)
- `|`-separated when a directive applies in multiple positions
- the list says where a directive belongs, but Dang doesn't check it yet: applying one somewhere else is not an error

## Applying

```dang
type Person @deprecated(reason: "use NewPerson") {
  name: String! @deprecated
  email: String! @cache(ttl: 60)
  greet(who: String! @experimental): String! { `hi, ${who}` }
}

Person("Ada", "ada@example.com").greet("Grace")
```

- suffix form attaches to the field or type: `name: String! @deprecated`
- prefix form on its own, before the declaration: `@check validated: String! { ... }`
- both forms apply to types, scalars (`scalar Tag @experimental`), fields, and function/field arguments (`process(user: Person! @experimental)`)

Multiple prefix directives go on separate lines, and prefix and suffix
directives on the same declaration are all collected. A directive has no
effect on what the field does:

```dang
@check
@experimental
mixedField: String! @cache(ttl: 120) { "mixed" }

mixedField
```

Using a directive that isn't declared is an error:

```dang-failure
type Account { id: String! @audited }
```

## Arguments

Arguments are named, or positional in declaration order, and the
declaration's defaults fill in what's left out:

```dang
type Session {
  token: String! @cache(ttl: 60, key: "user")
  user: String! @cache(60, key: "user")
  role: String! @cache
}
```

Arguments are checked like a function call's: required ones must be given,
types must match, and positional arguments come *before* named ones:

```dang-failure
type Admin { panel: String! @auth }
```

```dang-failure
type Admin { panel: String! @auth(role: 3) }
```

```dang-failure
type Admin { panel: String! @cache(key: "x", 60) }
```

## Qualified access

- `@MyApi.experimental` disambiguates when an import shadows a name
- if two imports both provide an unqualified directive, using it bare → `ambiguous reference to directive @experimental: provided by imports [...]`; qualify to resolve
- qualified access is **suffix-only** — the prefix form does not accept a `Module.` scope (`@MyApi.experimental ...` is a syntax error)

## Common built-ins

- `@defaultPath(path: ...)` — provides a default for a `Directory!` field (Dagger)
- `@ignorePatterns(patterns: [...])` — filtering metadata (Dagger)
- `@JSON.field(name:, omitNull:, omitEmpty:)` / `@JSON.ignore` (and `@YAML.*`, `@TOML.*`) — control how a field serializes (see [#json-yaml])
- `@example(code: String!)` — attaches a runnable example to a declaration, read by doc tooling (the stdlib reference builds its live REPLs from these). Idiomatically the code is a language-tagged fenced template, so editors highlight it as Dang:

````dang
"""
doubles a number
"""
@example(```dang
double(21)
```)
double(n: Int!): Int! { n * 2 }

double(21)
````

- plus every directive imported from connected schemas (see [#modules] for import/qualification)

## Structural, not semantic

> Meta: this framing is the differentiator — call it out.

- a directive *attaches typed data to a declaration*; it never runs code at a call site
- it has no runtime effect on evaluation of the annotated field — it is metadata read by tooling / the schema / a host (e.g. Dagger reads `@defaultPath`)
- contrast with the structural language constructs (objects, see [#objects]) that directives merely decorate
