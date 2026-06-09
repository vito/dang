\use-plugin{dang}

# Operators {#operators}

> Meta: lead with the precedence table — it's what people scroll back to. Per-operator notes can be terse if the table is precise.

## Precedence (low → high)

Precedence follows the `DefaultExpr → … → MultiplicativeExpr → Term` chain in `pkg/dang/dang.peg`.

| level | operators | assoc |
|---|---|---|
| 1 | `??` | right |
| 2 | `or` | left |
| 3 | `and` | left |
| 4 | `==`, `!=` | left |
| 5 | `<`, `<=`, `>`, `>=` | left |
| 6 | `+`, `-` | left |
| 7 | `*`, `/`, `%` | left |
| 8 | `!` (prefix), `-` (unary), `&` (prefix) | — |
| 9 | `!` (postfix), `.`, `.{{ }}`, `.{ }`, `[]`, `()` | left |

- `::` (cast / type hint) is **not** in this chain. In the grammar it's a sibling of `??` (`Form <- … / DefaultExpr / TypeHint / Term`) and binds only a bare `Term` on its left, e.g. `(a + b) :: T!` needs the parens. See [#types].
- the unary/postfix levels (8, 9) also parse as `Term`, so `&expr`, `!expr`, `-expr`, `expr!`, `.field`, `[i]`, `(args)` all bind tighter than every binary operator.
- the postfix `.`-brace forms are both siblings of `.field`/method calls at `.` precedence, so they interleave freely in one chain: `.{{ ... }}` is multi-field [selection][#graphql] (record-literal braces, short-circuits on null), and `.{ ... }` is [dot-block application][#dot-block] (single brace, the piping primitive). Their null behaviour differs — see [#dot-block].

## Arithmetic

- `+ - * /` on `Int` and `Float` (mixed `Int`/`Float` operands widen to `Float`, e.g. `1 * 2.0` ⇒ `2.0`)
- `%` is `Int`-only
- `/` and `%` on zero → runtime error (`division by zero` / `modulo by zero`)
- `+` overloads on `String!` (concat) and lists (concat); `- * / %` are numeric-only
- result type unifies the operands
- operands outside an operator's domain are a **static** type error, not a runtime failure: `"a" * "b"` ("operator multiplication is not defined for type String!"), `1 + "foo"` ("… not defined between types Int! and String!")

## Comparison

- `<` `<=` `>` `>=` on numbers (`Int`/`Float`, mixed allowed) or strings (compared lexicographically) — operands must match (`"a" < 1` is a static type error)
- `==` `!=` are type-safe — mismatched types compare `false`, no coercion (`num == str` is `false`)
- `==`/`!=` work on numbers, strings, bools, null, lists, records; both return `Boolean!`

## Logical

- `and`, `or` short-circuit; result type is `Boolean!`
- `!` is unary negation on `Boolean!`

## Default (`??`)

- `nullable ?? fallback` — returns fallback when LHS is null (`Default.Eval` checks `NullValue`)
- result type is the **fallback's** type: `Default.Infer` returns the right operand's type after `Assignable(rt, lt)`. So `T ?? T! → T!`; `T ?? T → T`
- right-associative: `a ?? b ?? c` parses as `a ?? (b ?? c)`

## Non-null assertion (postfix `!`)

- `expr!` asserts that a nullable value is non-null: it narrows the type from `T` to `T!` and, at runtime, raises `non-null assertion failed: value is null` if the value is actually null
- it's the explicit escape hatch for when [flow-sensitive narrowing][#flow-typing] can't prove non-nullness (e.g. a field or call result that can't be soundly narrowed) — prefer narrowing when you can, and reach for `!` when you know better than the checker
- binds as a `Term` (level 9), so it sticks to the immediately preceding operand: `a.b!` is `(a.b)!`, and `a! + b` is `(a!) + b`
- it's `expr!` with no space before the `!`; `a != b` and `a!=b` still parse as inequality

```dang
let name: String = user.nickname   # nullable
print(name!.length)                # assert non-null, then call

# asserting a value that is null raises at runtime
let missing: String = null
missing!                           # -> non-null assertion failed: value is null
```

## Compound assignment

- `+=` desugars to `+` (`AssignOp` in grammar maps `+=` to `+`); works on `Int`/`Float`, `String`, and lists
- requires the LHS to be a mutable field, local, or arg
- `=` is plain reassignment, not an operator on the precedence chain

## Cast / type hint: `::`

- covered in [#types]

## Unary

- `!expr` — boolean not
- `-expr` — numeric negation (`Int`/`Float`)
- `&expr` — function reference (see [#functions])
- all three bind a `Term`, so `-(1 + 2)` and `!(a or b)` need parens

> Meta: it's worth a paragraph on why `and`/`or` are keywords (readability) rather than `&&`/`||`. Same for `!=` vs `<>`.
