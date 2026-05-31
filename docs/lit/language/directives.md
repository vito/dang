\use-plugin{dang}

# Directives {#directives}

> Meta: short page. The interesting bit is that Dang directives are real declarations, type-checked, not magic comments — that's the differentiator from most languages with comment-pragmas.

## What a directive is

- a typed annotation attachable to a declaration or argument
- declared like GraphQL directives:

```dang
directive @deprecated(reason: String = "no longer supported")
  on FIELD_DEFINITION | OBJECT
```

## Locations (`on ...`)

- `FIELD_DEFINITION`, `OBJECT`, `ARGUMENT_DEFINITION`, `INTERFACE`, `UNION`, `ENUM`, ... (mirror GraphQL)
- `|`-separated when a directive applies in multiple positions

## Applying

```dang
type Person @deprecated(reason: "use NewPerson") {
  pub name: String! @deprecated
  pub email: String! @cache(ttl: 60)
}
```

- suffix form attaches to the slot or type
- prefix form: `@deprecated @cache(ttl: 60) pub field: String!`

## Arguments

- named: `@cache(ttl: 60, key: "user")`
- positional shorthand: `@cache(60, key: "user")`
- defaults supported

## Qualified access

- `@MyApi.experimental` when an import shadows a local name

## Common built-ins

- `@defaultPath(path: ...)` — provides a default for a `Directory!` slot
- `@ignorePatterns(patterns: [...])` — filtering metadata
- (plus everything imported from connected schemas)

> Meta: a small "structural vs. semantic" framing helps here — directives only *attach data to declarations*; they never run code at the call site.
