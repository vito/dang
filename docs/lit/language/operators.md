\use-plugin{dang}

# Operators {#operators}

> Meta: lead with the precedence table — it's what people scroll back to. Per-operator notes can be terse if the table is precise.

## Precedence (low → high)

Verified against `pkg/dang/dang.peg` (the `DefaultExpr → … → MultiplicativeExpr → Term` chain).

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

- `::` (cast / type hint) is **not** in this chain. In the grammar it's a sibling of `??` (`Form <- … / DefaultExpr / TypeHint / Term`) and binds only a bare `Term` on its left, e.g. `(a + b) :: T!` needs the parens. See [#types].
- the unary/postfix levels (8, 9) also parse as `Term`, so `&expr`, `!expr`, `-expr`, `.field`, `[i]`, `(args)` all bind tighter than every binary operator.

## Arithmetic

- `+ - * /` on `Int` and `Float` (mixed `Int`/`Float` operands promote to `Float`)
- `%` is `Int`-only
- `/` and `%` on zero → runtime error (`division by zero` / `modulo by zero`)
- `+` overloads on `String!` (concat) and lists (concat) — see `test_list_concatenation.dang`
- result type unifies the operands (`Default`/arithmetic use `hm.Assignable`)

## Comparison

- `<` `<=` `>` `>=` on numbers only (`Int`/`Float`, mixed allowed) — **not** on strings; the eval has no string-ordering path (`ast_operators.go` `lessThanEval` etc.)
- `==` `!=` are type-safe — mismatched types compare `false`, no coercion (`test_equality.dang`: `num == str` is `false`)
- `==`/`!=` work on numbers, strings, bools, null, lists, records; both return `Boolean!`

## Logical

- `and`, `or` short-circuit; result type is `Boolean!`
- `!` is unary negation on `Boolean!`

## Default (`??`)

- `nullable ?? fallback` — returns fallback when LHS is null (`Default.Eval` checks `NullValue`)
- result type is the **fallback's** type: `Default.Infer` returns the right operand's type after `Assignable(rt, lt)`. So `T ?? T! → T!`; `T ?? T → T`
- right-associative: `a ?? b ?? c` parses as `a ?? (b ?? c)` (`test_default_operator.dang` chaining)

## Compound assignment

- `+=` desugars to `+` (`AssignOp` in grammar maps `+=` to `+`); works on `Int`/`Float`, `String`, and lists (`test_plus_equals.dang`)
- requires the LHS to be a mutable field (`pub`/`let`)
- `=` is plain reassignment, not an operator on the precedence chain

## Cast / type hint: `::`

- covered in [#types]

## Unary

- `!expr` — boolean not (`test_unary_negation.dang`)
- `-expr` — numeric negation (`Int`/`Float`; `test_unary_minus.dang`)
- `&expr` — function reference (see [#functions])
- all three bind a `Term`, so `-(1 + 2)` and `!(a or b)` need parens

> Meta: it's worth a paragraph on why `and`/`or` are keywords (readability) rather than `&&`/`||`. Same for `!=` vs `<>`.
