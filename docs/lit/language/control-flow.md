\use-plugin{dang}
\literate-fences

# Control flow {#control-flow}

> Meta: keep `if`, `case`, and `loop` close together — they're all expression-form. The "no statements vs. expressions" point is worth stating once at the top.

Dang has no control-flow *statements*. `if`, `case`, `loop`, and `try` are
expressions: each yields the last expression of whichever branch ran, so
anything you can do with a value — bind it, return it, pass it as an
argument — you can do with a conditional or a loop.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## Everything is an expression

All four forms, sitting in expression position as list elements:

```dang
[
  if (true) 1 else 2,
  case { else => 3 },
  loop { break 4 },
  try { raise "!" } catch { err => 5 },
]
```

The rest of this page covers `if`, `case`, and `loop`, plus the jumps —
`break`, `continue`, `return` — that cut across them. `try`/`catch`/`raise`
have their own page: [#errors].

## `if` / `else`

Each branch of an `if` is a plain expression:

```dang
let ready = true

if (ready) "on" else "off"
```

Braces aren't part of the `if` syntax — a braced branch is just a block
grouping several expressions into one (see [#blocks]):

```dang
if (ready) { let msg = "on", msg.toUpper } else { "off" }
```

The condition must be a `Boolean!` — there is no truthiness, and a
non-`Boolean` condition is a compile error:

```dang-failure
if (1) "yes" else "no"
```

`else if` chains aren't special syntax either: the `else` branch is simply
another `if` expression:

```dang
grade(score: Int!): String! {
  if (score >= 90) "A" else if (score >= 80) "B" else "C"
}

grade(85)
```

With no `else`, the expression is `null` whenever the condition is false, so
its type is nullable — and an `else if` chain with no final `else` is
likewise nullable. (Not even a `return` from the then-branch can undo that;
see `return` below.)

```dang
if (false) "value"
```

Narrowings from the condition apply per branch (then = truthy, else = falsy;
see [#flow-typing]). Testing `nickname` against `null` is what makes
`.toUpper` — a `String!` method — safe in the `else` branch:

```dang
let nickname = "zoe" :: String

if (nickname == null) "anonymous" else nickname.toUpper
```

Branches must merge to a common type; branches that *diverge* widen to a
union instead (see [#flow-typing]). `pet` here is a `Cat! | Dog!`, which
`case` type patterns will take back apart shortly:

```dang
type Cat { name: String!, lives: Int! = 9 }
type Dog { name: String! }

let pet = if (grade(95) == "A") Cat(name: "Whiskers") else Dog(name: "Rex")
```

## `case`

`case` compares an operand against clauses, top to bottom — the first match
wins, so a duplicate clause (or anything after an early `else`) is simply
never reached:

```dang
case (1 + 1) {
  2 => "first match wins"
  2 => "a duplicate is never reached"
  else => "no match above"
}
```

Clause bodies must merge to one common type:

```dang-failure
case (1) {
  1 => "one"
  2 => 2
}
```

And there is **no compile-time exhaustiveness check**: when nothing matches
and there's no `else`, the `case` raises a runtime error, catchable like any
other (see [#errors]):

```dang
try { case (7) { 1 => "one" } } catch { err => err.message }
```

### Value patterns

A value pattern is a literal scalar — an int, float, string, boolean, enum
value, or `null`:

```dang
let digit = 2

case (digit) {
  1 => "one"
  2 => "two"
  else => "?"
}
```

`null` is just another pattern:

```dang
case (null :: Int) {
  1 => "one"
  null => "nothing"
}
```

Literal patterns coerce to the operand's scalar or enum type — an operand of
a custom scalar like `URL!` matches a `"https://…"` clause, an enum operand
matches `"ACTIVE"` — the same coercion as at argument and return boundaries
(see [#enums-scalars]). Only *syntactic* literals coerce; a non-literal
value of a different type is a compile error (`clause N value type
mismatch`).

### Type patterns

A type pattern has the form `binding: Type => expr`; the binding is the
operand narrowed to that type inside the clause. Here it takes apart the
`Cat! | Dog!` union `pet` picked up under `if`/`else` above:

```dang
case (pet) {
  c: Cat => `a cat with ${c.lives} lives`
  d: Dog => `a dog named ${d.name}`
}
```

The operand must be a union or an interface (see [#interfaces-unions]) — a
plain object type is already fully known, so there is nothing to narrow:

```dang-failure
case (Cat(name: "Solo")) { c: Cat => c.name }
```

And the named type must be one of the operand's actual possibilities:

```dang-failure
case (pet) { s: String => s }
```

An interface-typed operand works the same way, with patterns checked against
its implementers — and an interface is itself a valid pattern, a typed
catch-all matching any implementer, so specific types go first:

```dang
interface Sound { noise: String! }
type Bell implements Sound { noise: String! = "ding" }
type Horn implements Sound { noise: String! = "honk" }

play(s: Sound!): String! {
  case (s) {
    b: Bell => `a bell goes ${b.noise}`
    other: Sound => `something goes ${other.noise}`
  }
}

[play(Bell), play(Horn)]
```

`catch` clauses use these same type patterns, over `Error` implementers —
see [#errors].

### Optional operand

Omitting the operand desugars to `case (true)`: each clause is then a
`Boolean!` condition, making `case` Dang's cond-style conditional chain:

```dang
let temp = 35

case {
  temp < 0 => "freezing"
  temp > 30 => "hot"
  else => "mild"
}
```

## `loop`

`loop { ... }` is Dang's only loop — and it's a stdlib builtin (see
[#stdlib]), not a keyword: it calls its block over and over, forever, until
a `break`, `return`, or `raise` exits it.

```dang
loop { break 42 }
```

There is no `for` or `while`. Collections are iterated with `.each` and
friends (see [#collections] and [#blocks]), and a conditional loop is a
`loop` with a guard at the top:

```dang
let n = 1
loop {
  if (n >= 100) { break }
  n = n * 2
}

n
```

A `loop` is an expression: it yields whatever value its `break` carries.
When every `break` carries a non-null value the loop's type is non-null too —
this one is an `Int!`, usable directly in arithmetic — while a bare `break`
yields `null` and makes it nullable:

```dang
loop { break 42 } + 1
```

## `break` and `continue`

`break` and `continue` are valid only inside a loop or a block-taking call —
anywhere else they're compile errors (`continue` reports the same way):

```dang-failure
break
```

`break` exits the nearest enclosing loop or block-taking call, and `break
value` makes it yield `value` (a bare `break` yields `null`). `.each`
normally returns its receiver, but a `break` overrides that:

```dang
[5, 10, 15, 20].each { x => if (x > 12) { break x } }
```

`continue` ends the current iteration. In `.map`, `continue value` supplies
that element's result — so this bare `continue` inserts `null`:

```dang
[1, 2, 3].map { x => if (x == 2) { continue }, x }
```

In `.each` and `loop` there is no per-element result, so `continue` just
advances:

```dang
let sum = 0
[1, 2, 3, 4].each { x => if (x % 2 == 1) { continue }, sum += x }

sum
```

Both target the *nearest* enclosing loop or block-call only, and an ordinary
function declared inside a block does not inherit that target — a `break`
there is the same compile error as outside. (More block-specific wrinkles in
[#blocks].)

## `return`

`return` exits the enclosing function, method, or constructor early — there
is no `return` for the normal result, since the last expression already is
the result (see [#functions]). Its value must satisfy the declared return
type, and there must be a function to exit:

```dang-failure
return "early"
```

It unwinds through any blocks and loops in between, so returning from inside
`.each` exits the whole function:

```dang
firstEven(nums: [Int!]!): Int {
  nums.each { n => if (n % 2 == 0) { return n } }
  null
}

firstEven([3, 7, 8, 9])
```

A `return` in a *skippable* branch — a no-else `if`, say — does not make the
function non-null. `shout`'s only exit with a value is a `String!`, but the
branch may be skipped, so its return type stays nullable:

```dang
shout(word: String): String {
  if (word != null) { return word.toUpper }
}

[shout("hey"), shout(null)]
```

And `return` is not an error: `try`/`catch` cannot intercept it. (`sneaky`
is a zero-arg function, so referencing it calls it — see [#functions].)

```dang
sneaky: String! {
  try { return "returned" } catch { err => `caught: ${err.message}` }
}

sneaky
```

## `try` / `catch` / `raise`

Errors are expressions with the same shape — a `try`/`catch` yields a value,
and `raise` fits in any branch — but they earn their own page: [#errors].
