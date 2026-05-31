\use-plugin{dang}

# Operators {#operators}

> Meta: lead with the precedence table — it's what people scroll back to. Per-operator notes can be terse if the table is precise.

## Precedence (low → high)

| level | operators | assoc |
|---|---|---|
| 1 | `??` | right |
| 2 | `or` | left |
| 3 | `and` | left |
| 4 | `==`, `!=` | left |
| 5 | `<`, `<=`, `>`, `>=` | left |
| 6 | `+`, `-` | left |
| 7 | `*`, `/`, `%` | left |
| 8 | `!`, `-` (unary), `&` (prefix) | — |
| 9 | `.`, `[]`, `()` | left |

## Arithmetic

- `+ - * / %` on `Int!`
- `/` and `%` on zero → runtime error
- `+` overloads on `String!` (concat) and lists (concat)

## Comparison

- `<` `<=` `>` `>=` on `Int!` and `String!`
- `==` `!=` are type-safe — mismatched types compare `false`, no coercion
- works on numbers, strings, bools, null, lists, records

## Logical

- `and`, `or` short-circuit; result type is `Boolean!`
- `!` is unary negation on `Boolean!`

## Default (`??`)

- `nullable ?? fallback` — returns fallback when LHS is null
- types: `T ?? T! → T!`; `T ?? T → T`

## Compound assignment

- `+=` on numeric, string, and list slots
- requires the LHS to be a mutable slot (`pub`/`let`)

## Cast / type hint: `::`

- covered in [Types & nullability](./types.md#type-hints--casts-)

## Unary

- `!expr` — boolean not
- `-expr` — numeric negation
- `&expr` — function reference (see [functions](./functions.md))

> Meta: it's worth a paragraph on why `and`/`or` are keywords (readability) rather than `&&`/`||`. Same for `!=` vs `<>`.
