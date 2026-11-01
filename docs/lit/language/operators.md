\use-plugin{dang}
\literate-fences

# Operators {#operators}

> Meta: lead with the precedence table — it's what people scroll back to. Per-operator notes can be terse if the table is precise.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

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

`??` binds loosest of all, which matters when you mix it with arithmetic.
Here the second line reads as `scores[0] ?? (0 + 100)`:

```dang
let scores = [10, null, 30]

[scores[0] ?? 0 + 100, (scores[0] ?? 0) + 100]
```

`::` (cast / type hint) is **not** in this chain. In the grammar it's a
sibling of `??` and binds only a bare `Term` on its left, so `(a + b) :: T!`
needs the parens (see [#nullability]):

```dang-failure
1 + 2 :: Int!
```

- the unary/postfix levels (8, 9) also parse as `Term`, so `&expr`, `!expr`, `-expr`, `expr!`, `.field`, `[i]`, `(args)` all bind tighter than every binary operator.
- the postfix `.`-brace forms are both siblings of `.field`/method calls at `.` precedence, so they interleave freely in one chain: `.{{ ... }}` is multi-field [selection][#interop] (record-literal braces, short-circuits on null), and `.{ ... }` is [dot-block application][#dot-block] (single brace, the piping primitive). Their null behaviour differs — see [#dot-block].

## Arithmetic

`+ - * / %` work on `Int` and `Float`. `/` on two `Int`s drops the remainder,
and `%` gives it:

```dang
[7 / 2, 7 % 2]
```

Mixing an `Int` with a `Float` gives a `Float`. For `%`, the remainder takes
the sign of the left operand:

```dang
[7 / 2.0, 7.5 % 2, -7.5 % 2]
```

Dividing by zero raises a runtime error, and so does `% 0`:

```dang-failure
10 / 0
```

`+` also joins strings and lists. The other operators are numeric only:

```dang
"con" + "cat"
```

```dang
[1, 2] + [3]
```

Operands outside an operator's domain are a type error, caught before the
script runs:

```dang-failure
"a" * "b"
```

```dang-failure
1 + "foo"
```

Operands must be **non-null**. Operators don't pass null through the way
field access does, so a nullable operand is a type error:

```dang-failure
scores[1] + 1
```

Hand the operator a non-null value with `!` (raises if the value is null),
with `??`, or with a null check (see [#flow-typing]):

```dang
[scores[0]! + 1, (scores[1] ?? 0) + 1]
```

The result is non-null. List `+` keeps the left operand's type, so the right
operand's elements must fit the left's element type. `[scores[0]] + [1]` is
fine, but this is not:

```dang-failure
[1] + [scores[0]]
```

## Comparison

`<` `<=` `>` `>=` compare numbers (`Int` and `Float` mix) or strings
(alphabetically):

```dang
[1 < 2.5, "apple" < "banana"]
```

Both operands must be the same kind and non-null, as for arithmetic:

```dang-failure
"a" < 1
```

`==` and `!=` only compare values that could be equal: the same type
(nullability aside), `Int` with `Float`, a type with its interface, a union
with its members, and anything with `null`. Nullable operands are fine, and
both return `Boolean!`:

```dang
[1 == 1.0, scores[1] == null, [1, 2] == [1, 2]]
```

Comparing unrelated types is an error:

```dang-failure
1 == "1"
```

A string *literal* compared with an enum or a custom scalar converts to it,
as it does for arguments and `case` patterns (see [#enums-scalars]).
`Color.GREEN` is still the clearer way to write an enum value:

```dang
enum Color { RED GREEN }

let light = Color.GREEN
let userId: ID! = "User:1"

[light == "GREEN", light != Color.RED, userId == "User:1"]
```

An enum literal must name one of the enum's values:

```dang-failure
light == "PURPLE"
```

A `String` held in a variable doesn't convert at all. Cast it:

```dang
let wanted = "GREEN"
```

```dang-failure
light == wanted
```

```dang
light == (wanted :: Color!)
```

**Records** (`{{…}}` literals and `.{{…}}` selections) compare by value:
equal when every field is equal. Records with different fields can't be
compared:

```dang
[{{a: 1}} == {{a: 1}}, {{a: 1}} == {{a: 2}}]
```

```dang-failure
{{a: 1}} == {{b: 1}}
```

**Objects of a named type** compare by type, then by their stored fields.
Computed members (`field: T { … }`) are behavior, not stored state, so
they're ignored:

```dang
type Rabbit {
  name: String!
  shout: String! { name.toUpper }
}

[Rabbit("Bun") == Rabbit("Bun"), Rabbit("Bun") == Rabbit("Hop")]
```

Two different types, or a named object and a record, can't be compared:

```dang
type Hare { name: String! }
```

```dang-failure
Rabbit("Bun") == Hare("Bun")
```

**GraphQL objects** compare by identity. A GraphQL object's identity is the
query that produced it, so two queries for the same user are not equal. To
ask whether two objects are the same server entity, compare an identifying
field such as `id`. This uses the bundled `Demo` schema (see [#interop]):

```dang
import Demo

[primaryUser == user(id: "User:1"), primaryUser.id == user(id: "User:1").id]
```

## Logical

`and` and `or` short-circuit, so the right side runs only when it's needed.
Both return `Boolean!`, and `!` negates a `Boolean!`:

```dang
[false and 1 / 0 == 0, !true]
```

There's no truthiness: every operand must be a `Boolean!`:

```dang-failure
1 and true
```

## Default (`??`)

`a ?? b` is `a` unless `a` is null, in which case it's `b`. The result has
the type of the right operand, so a non-null fallback gives a non-null
result (`T ?? T!` is `T!`, `T ?? T` is `T`). It's right-associative: `a ??
b ?? c` reads as `a ?? (b ?? c)`.

```dang
let nickname = null :: String
let username = "ada" :: String

nickname ?? username ?? "anonymous"
```

## Non-null assertion (postfix `!`)

`expr!` asserts that a nullable value isn't null. It narrows the type from
`T` to `T!`, and raises at runtime if the value is null. Use it when
[flow-sensitive narrowing][#flow-typing] can't prove what you know, such as
a field or call result. Prefer narrowing when you can.

```dang
username!.toUpper
```

```dang-failure
nickname!
```

It sticks to the term right before it: `a.b!` is `(a.b)!`, and `a! + b` is
`(a!) + b`. Write it with no space before the `!`; `a != b` and `a!=b` are
still inequality.

## Compound assignment

`x += y` is short for `x = x + y`, and works for numbers, strings, and
lists:

```dang
let total = 1
total += 2

let words = ["a"]
words += ["b"]

[total, words.length]
```

The target must be a variable, field, or argument, and since `+=` reads it,
it must be **non-null**:

```dang-failure
let visits: Int = 0
visits += 1
```

Declare it non-null (with a default), or write `visits = (visits ?? 0) + 1`.
Assigning through `!` (`visits! += 1`) is rejected too.

`=` is plain reassignment, not an operator on the precedence chain.

## Cast / type hint: `::`

- covered in [#nullability]

## Unary

- `!expr` — boolean not
- `-expr` — numeric negation (`Int`/`Float`)
- `&expr` — function reference (see [#functions])
- all three bind a `Term`, so `-(1 + 2)` and `!(a or b)` need parens

```dang
[-(1 + 2), -1 + 2]
```

> Meta: it's worth a paragraph on why `and`/`or` are keywords (readability) rather than `&&`/`||`. Same for `!=` vs `<>`.
