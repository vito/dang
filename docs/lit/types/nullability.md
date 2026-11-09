\use-plugin{dang}
\literate-fences

# Types and nullability {#nullability}

> Meta: nullability is the page's main attraction. Most readers won't have seen GraphQL-style `T!` outside of a schema and may misread it as "definitely-null" or "force-unwrap."

Dang's types follow GraphQL: every type is nullable unless it ends in `!`.
The type checker tracks which values may be null and makes you deal with
them before you use them.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## Built-in types

- `Int!`, `Float!`, `String!`, `Boolean!`, `ID!`
- `[T]` and `[T]!` — lists
- `{{ ... }}` — records
- `Decoded` — decoded data not yet given a type (see [#decoded-values])
- custom GraphQL scalars: `URL`, `Timestamp`, `JSON`, ...
- user-defined: `type`, `interface`, `union`, `enum`, `scalar` (see their pages)

## The `!` sigil

`T!` is a non-null `T`. Plain `T` is nullable: it holds a `T` or `null`.

```dang
let nickname: String = null

nickname
```

A `T!` can go anywhere a `T` is expected, but not the other way around. A
`String` may be null, so it can't fill a `String!`:

```dang-failure
let display: String! = nickname
```

`!` applies to the type just before it, so list types read from the inside
out: `[String!]!` is a non-null list of non-null strings. A constructor call
or a record literal always gives a non-null value.

## Lists, nullability matrix

| written | meaning |
|---|---|
| `[T]` | nullable list of nullable T |
| `[T]!` | non-null list of nullable T |
| `[T!]` | nullable list of non-null T |
| `[T!]!` | non-null list of non-null T |

A list literal with a `null` in it is a `[T]!`, so it doesn't fit a list of
non-null elements:

```dang-failure
let scores: [Int!]! = [10, null, 30]
```

## Null propagation

Reading a field through a nullable value gives a nullable result, even if
the field itself is non-null. A chain stops at the first null link:

```dang
type User {
  name: String!
  nickname: String
  manager: User
}

let ada = User(name: "Ada", manager: User(name: "Grace"))

[ada.manager.name, ada.manager.manager.name]
```

Operators are the exception. Arithmetic and ordering need non-null operands,
so a nullable operand is a type error, not a `null` result (see
[#operators]):

```dang-failure
let scores = [10, null, 30]
scores[0] + 1
```

To get a non-null value back, give a default with `??` (see [#operators]),
or check for null and let narrowing do the rest (next section):

```dang
ada.nickname ?? ada.name
```

Function values can be null too: an element of a list of functions, a
method taken off a nullable value (`&h.hook`), an `if` without `else`. Such a
value can't be called until you check it for null, assert it with `!`, or
give it a default with `??`:

```dang
increment(x: Int!): Int! { x + 1 }
square(x: Int!): Int! { x * x }

let steps = [&increment, &square]
```

```dang-failure
steps[0](5)
```

```dang
[steps[0]!(5), steps[1]!(5)]
```

Calling a method through a nullable value (`h.hook(1)`) still just
propagates the null.

## Flow-sensitive narrowing {#flow-typing}

> Meta: this section pays for itself — the alternative is writing `!` casts everywhere. Make sure the examples include both narrowing inside a branch and narrowing after a guard clause.

### Null narrowing

Checking a name against `null` narrows its type. After a guard that leaves
early, the name is non-null for the rest of the scope:

```dang
greet(name: String): String! {
  if (name == null) { return "hello, stranger" }
  `hello, ${name.toUpper}`
}

[greet("ada"), greet(null)]
```

Inside a branch, the check narrows just that branch. Here `count` is an
`Int!` in the then-branch, and the `if` has no `else`, so `double` returns
`Int`:

```dang
double(count: Int): Int {
  if (count != null) { count * 2 }
}

[double(4), double(null)]
```

The `else` branch of an `== null` check is narrowed the same way:

```dang
excited(name: String): String! {
  if (name == null) { "nobody" } else { name + "!" }
}

excited("Grace")
```

A guard that `break`s narrows the rest of a loop body. This walks a linked
list until it runs out:

```dang
type Link { value: Int!; next: Link }

let cur: Link = Link(1, Link(2, Link(3)))
let sum = 0
loop {
  if (cur == null) { break }
  sum += cur.value
  cur = cur.next
}

sum
```

`cur` is declared as a `Link` so it can hold the `null` at the end. A
mutable binding keeps the type of its first value, so `let cur = Link(1)`
would make it a `Link!`, and the `null` from `cur.next` wouldn't fit.

### Diverging constructs are narrowing-aware

`return`, `raise`, `break`, and `continue` all leave the current path. When
a guard's then-branch does one of them, the code after the guard knows the
check was false:

- the narrowing lasts for the rest of the enclosing scope
- in an `else if` chain, the earlier checks' facts still hold afterward
- guards in a row add up, each narrowing its own name
- inside a `loop` or `.each` block, a guard that `break`s or `continue`s
  narrows the rest of that iteration

At the top level of a script, a guard narrows the statements after it, but
not declarations. Every `let` and function runs before the first statement,
so the `let` below runs before the guard does:

```dang
let input = "hello" :: String
```

```dang-failure
if (input == null) { raise "missing input" }
let loud: String! = input.toUpper
```

`case` clauses narrow too. A clause runs only when the clauses before it
didn't match, so after a `null =>` clause the operand is non-null. This
works when the operand is a bare name:

```dang
describe(n: Int): String! {
  case (n) {
    null => "nothing"
    0 => "zero"
    else => `${n + 1} comes next`
  }
}

[describe(null), describe(0), describe(4)]
```

The same goes for the conditions of a `case` without an operand:

```dang
size(n: Int): String! {
  case {
    n == null => "unknown"
    n > 100 => "big"
    else => "small"
  }
}

[size(null), size(500), size(7)]
```

### Reassigning a narrowed name

A narrowed name can still be assigned anything its declared type allows. A
non-null value keeps it narrowed. A value that may be null ends the
narrowing until the next check:

```dang-failure
secondValue(list: Link): Int! {
  let cur = list
  if (cur == null) { return 0 }
  cur = cur.next
  cur.value + 1
}
```

The same goes for writes the checker sees less directly:

- `self.x = v` ends the narrowing of the bare field `x`
- calling a closure that assigns `x` null ends it, even if the closure was
  written before the check
- calling a block parameter ends the narrowing of anything declared outside
  the function taking it, which the caller's block may reassign
- a block or nested function can't rely on a check made outside it if it may
  run again, or later, after `x` is set to null: when the block itself
  assigns `x` null (it runs again on the next iteration), or when `x` is set
  to null after a nested function or stored block that reads it is created.
  Check `x` inside it, or bind the checked value to a local
- a field write evaluates its value before it walks the target's path, so in
  `x.n = v` a value that may set `x` to null (calling such a closure, or a
  block parameter) ends the narrowing of `x` for the write: compute the value
  into a local first, or assert the root (`x!.n = v`)

Here `reset` sets `total` back to null, so calling it ends the narrowing:

```dang-failure
tally: Int! {
  let total: Int = 1
  reset: Int! { total = null; 0 }
  if (total == null) { return 0 }
  reset
  total + 1
}
```

Writes a read can't follow from don't count: those in the other branch of
the same `if` or another arm of the same `case`, in a branch that returns
(or, for the rest of a loop body, breaks or continues), or in a nested
function that isn't called. A branch's exit doesn't hide its writes inside a
`rescue` operand or a block passed to a Dang function, though: a raise
before the exit may be rescued.

### Type narrowing via `case`

A `binding: Type =>` clause binds the operand narrowed to that type. This is
how you take apart a union, such as the one an `if`/`else` with unrelated
branches produces:

```dang
type Cat { name: String! }
type Dog { name: String!; tricks: [String!]! = [] }

let pet = if (sum > 5) Dog(name: "Rex", tricks: ["sit"]) else Cat(name: "Tom")

case (pet) {
  c: Cat => `${c.name} ignores you`
  d: Dog => `${d.name} can ${d.tricks.join(" and ")}`
}
```

`rescue` clauses use the same form, so a typed rescue clause narrows the
error it binds (see [#errors]).

### Conditional result inference (related)

An `if` with a `null` branch, or with no `else`, has a nullable type.
Branches of unrelated types widen to their common interface, or to a union
(like `pet` above). Either way, the type only matters once you use the
result. Here `label` is a nullable `String`:

```dang-failure
let label = if (sum > 5) "many" else null
label + "!"
```

### Compound conditions

An `or` guard that leaves early narrows both names, because getting past it
means both checks were false:

```dang
add(a: Int, b: Int): Int! {
  if (a == null or b == null) { raise "missing an operand" }
  a + b
}

add(1, 2)
```

An `and` condition narrows both names inside the then-branch:

```dang
multiply(a: Int, b: Int): Int {
  if (a != null and b != null) { a * b }
}

[multiply(2, 3), multiply(2, null)]
```

The right side of `and` sees the left side's narrowing, and the right side
of `or` sees the opposite, so a guard works inline:

```dang
isPositive(n: Int): Boolean! { n != null and n > 0 }

[isPositive(3), isPositive(null), isPositive(-1)]
```

### Limitations

Narrowing is local to one function: calling another function doesn't carry
narrowed types across.

An `and` guard that leaves early does **not** narrow. Getting past it only
means *at least one* name is non-null, so neither is narrowed:

```dang-failure
sumBoth(a: Int, b: Int): Int! {
  if (a == null and b == null) { raise "both missing" }
  a + b
}
```

**Field reads don't narrow.** Each `.field` read could return a different
value, so checking `user.nickname` says nothing about the next read:

```dang-failure
loudNickname(user: User!): String! {
  if (user.nickname == null) { return user.name }
  user.nickname.toUpper
}
```

Bind the field to a local first, and check that:

```dang
loudNickname(user: User!): String! {
  let nick = user.nickname
  if (nick == null) { return user.name }
  nick.toUpper
}

loudNickname(User(name: "Ada", nickname: "countess"))
```

The same applies to a computed member (`title: String { … }`) read by its
bare name: each read calls it again. Narrowing does work on locals,
parameters, and a method's bare reads of its own stored fields.

When narrowing can't reach the value — a field or call result, or a spot where the checker just can't follow your reasoning — the postfix `!` operator is the explicit escape hatch: `expr!` narrows `T` to `T!` and raises at runtime if the value turns out to be null. See [#operators].

```dang
ada.manager!.name
```

See also [#errors] (`raise`/`rescue` divergence) and [#control-flow] (guards, loops, `case`).

> Meta: field-narrowing and the `and`-guard non-narrowing are the two most surprising gaps in practice. Both are documented above with the re-bind-to-a-local workaround.

## Type variables

A single lowercase letter (`a`, `b`) in a signature is a type variable. It
stands for whatever type the caller passes, and is inferred at each call:

```dang
pair(x: a): [a]! { [x, x] }

[pair(1), pair(2)]
```

```dang
pair("hi")
```

A type variable is opaque. The function can pass the value along, return
it, or compare it with `==` and `!=`, but nothing else:

```dang-failure
doubled(x: a): a { x * 2 }
```

Use a concrete type (here `Int!`) if the body needs to operate on the value.

## Type hints / casts: `::`

`expr :: Type` gives an expression a type. It binds only a single term on
its left, so wrap anything bigger in parentheses: `(a + b) :: Int!` (see
[#operators] for precedence).

As a *hint*, it fills in a type the checker can't infer, such as the
element type of an empty list:

```dang
let tags = [] :: [String!]!
tags.length
```

As a *cast*, it converts a `String` into a custom scalar or an enum. This is
how a value that isn't a literal becomes one (see [#enums-scalars]):

```dang
scalar URL

let host = "example.com"
let site = `https://${host}` :: URL!
site
```

```dang
enum Plan { FREE PRO }

let plan = "PRO" :: Plan!
plan == Plan.PRO
```

Only `String` values convert this way. Anything else is rejected before the
script runs:

```dang-failure
42 :: URL!
```

A string literal cast to an enum is checked before the script runs, too. A
string that isn't a literal is checked when the cast runs:

```dang-failure
"ENTERPRISE" :: Plan!
```

```dang-failure
let wanted = "ENTERPRISE"
wanted :: Plan!
```

Casting a nullable value to a non-null type is a runtime check, not a
promise: it raises if the value is null.

```dang-failure
JSON.decode("null") :: String!
```

> Meta: `::` deserves a worked example showing the difference between a type *hint* (narrowing/disambiguation, e.g. `[] :: [String!]!` binding a type variable) and a type *cast* (coercion, e.g. `myUrl :: URL!`). The runtime-assertion behavior for non-null casts is a footgun worth a short callout.

## Coercion rules

Passing a value to a parameter or a typed slot doesn't convert it. A
`String!` variable doesn't fit where a `URL!` is expected:

```dang
fetch(url: URL!): String! { `GET ${url}` }

let address = "https://example.com"
```

```dang-failure
fetch(address)
```

The exception is a *literal*: a string literal, a template, or a list or map
literal of them converts to a compatible scalar wherever a value is handed
over — call arguments, typed slots, and return values:

```dang
[fetch("https://example.com"), fetch(`https://${host}/docs`)]
```

For any other value, cast with `::` (see above):

```dang
fetch(address :: URL!)
```

List literals don't convert their elements to match each other. One `ID!`
element doesn't turn the plain string next to it into an `ID!`:

```dang-failure
["abc" :: ID!, "def"]
```
