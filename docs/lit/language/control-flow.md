\use-plugin{dang}
\literate-fences

# Control flow {#control-flow}

> Meta: keep `if`, `case`, and `loop` close together — they're all expression-form. The "no statements vs. expressions" point is worth stating once at the top.

Dang has no control-flow *statements*. `if`, `case`, `loop`, and `rescue`
are expressions: each yields the last expression of whichever branch ran,
so anything you can do with a value — bind it, return it, pass it as an
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
  { raise "!" } rescue 5,
]
```

The rest of this page covers `if`, `case`, and `loop`, plus the jumps —
`break`, `continue`, `return` — that cut across them. `raise` and `rescue`
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
if (ready) { let msg = "on"; msg.toUpper } else { "off" }
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
type Cat { name: String!; lives: Int! = 9 }
type Dog { name: String! }

let pet = if (grade(95) == "A") {
  Cat(name: "Whiskers")
} else {
  Dog(name: "Rex")
}
```

## `case`

`case` compares an operand against clauses, top to bottom — the first match
wins, so a duplicate value clause is simply never reached:

```dang
case (1 + 1) {
  2 => "first match wins"
  2 => "a duplicate is never reached"
  else => "no match above"
}
```

Provably dead clauses are rejected at compile time: nothing may follow an
`else`, and a *type pattern* (below) that repeats an earlier one — or that an
earlier pattern already covers, such as an interface pattern its type
implements — is an error:

```dang-failure
case (1) {
  else => "matches everything"
  1 => "never reached"
}
```

Clause bodies merge to one common type when they can; bodies that *diverge*
widen to a union instead, exactly like `if` branches (see [#flow-typing]) —
this one is a `String! | Int!`:

```dang
case (1) {
  1 => "one"
  2 => 2
}
```

And when nothing matches and there's no `else`, the `case` is `null` — so,
like a no-`else` `if`, its type is nullable. Adding an `else` keeps the
result non-null. (So is covering every possibility; see *Type patterns*
below.)

```dang
case (7) { 1 => "one" }
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

A type pattern has the form `binding: Type! => expr`: the clause takes the
operand's values of that type, and binds them as that type. A pattern is
written as the non-null type its arm binds, since `null` never matches one.
The type can be any type: a named type, which may be qualified
(`g: Lib.Greeter! =>`, naming an imported type even where a type of your own
shadows the bare name), a list or map type, or a record type. It must be a
type some value of the operand may have, as the types relate: the operand's
type itself, a type that implements it or belongs to it, or one it implements
or belongs to. Two interfaces related by neither don't qualify, even if some
type implements both.
Here it takes apart the `Cat! | Dog!` union `pet` picked up under
`if`/`else` above:

```dang
case (pet) {
  c: Cat! => `a cat with ${c.lives} lives`
  d: Dog! => `a dog named ${d.name}`
}
```

Type patterns covering every possibility of a non-null operand are
*exhaustive*: nothing can fall through, so no `else` is needed and the result
stays non-null — the case above is a `String!`. Leave a member out and the
usual nullable fallthrough applies — `pet` is a `Cat`, so this is `null`:

```dang
case (pet) { d: Dog! => d.name }
```

Value patterns are exhaustive the same way when they name every value the
operand can have: each member of an enum (as `Color.RED`, an imported bare
`RED`, or a string literal `"RED"`), or both `true` and `false` for a
`Boolean!`. A nullable operand (enum, Boolean or union) can also be `null`,
which no type pattern matches, so it takes a `null` pattern too:

```dang
enum Light { RED GREEN }

signal(light: Light): String! {
  case (light) {
    Light.RED => "stop"
    Light.GREEN => "go"
    null => "off"
  }
}

[signal(Light.RED), signal(null)]
```

A named type matches a value of that type, of a type implementing it (for an
interface), or of one of its members (for a union). A pattern that takes
every value of the operand is fine, just exhaustive on its own:

```dang
case (Cat(name: "Solo")) { c: Cat! => c.name }
```

But a pattern no value of the operand can match is an error:

```dang-failure
case (pet) { s: String! => s }
```

An interface-typed operand works the same way, with patterns checked against
its implementers — and an interface is itself a valid pattern, a typed
catch-all matching any implementer, so specific types go first. An
interface's implementer set is open, so matching specific implementers is
never exhaustive: it takes that catch-all (or an `else`) to keep the result
non-null, as in `play` returning `String!` here:

```dang
interface Sound { noise: String! }
type Bell implements Sound { noise: String! = "ding" }
type Horn implements Sound { noise: String! = "honk" }

play(s: Sound!): String! {
  case (s) {
    b: Bell! => `a bell goes ${b.noise}`
    other: Sound! => `something goes ${other.noise}`
  }
}

[play(Bell), play(Horn)]
```

Lists and maps match by their element types, and records by their fields and
the fields' types, so arms can tell apart an operand that may be either of two
list types:

```dang
let picked = if (grade(95) == "A") { [1, 2] } else { ["a", "b"] }

case (picked) {
  ns: [Int!]! => `numbers adding up to ${ns.reduce(0) { a, n => a + n }}`
  ss: [String!]! => `words: ${ss.join(", ")}`
}
```

A list matches by the element type it was built with — its static type where
it was written, or where it last passed a typed boundary such as an
annotation or a parameter of a concrete list type — not by what it happens to hold. `[1, x]` with a
nullable `x` is an `[Int]!`, so it doesn't match `[Int!]!` even while `x`
isn't null, and a list typed `[Animal!]!` doesn't match `[Cat!]!` even when
every animal in it is a cat. Only where that type isn't known (a list built
in a generic function, from a type variable) do the elements decide.

Decoded data matches by its shape instead, and there `else` is required (see
[#decoded-values]).

`rescue` clauses use these same type patterns, over `Error` implementers —
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

`break` and `continue` are valid only inside a block being passed to a call
— `.each`, `loop`, a user-defined block-taking function; there is no other
category, since a `loop` body is just an ordinary block argument. Anywhere
else they're compile errors (`continue` reports the same way):

```dang-failure
break
```

`break` exits the nearest enclosing block-taking call, and `break value`
makes it yield `value` (a bare `break` yields `null`). `.each` normally
returns its receiver, but a `break` overrides that:

```dang
[5, 10, 15, 20].each { x => if (x > 12) { break x } }
```

`continue` ends the current iteration. In `.map`, `continue value` supplies
that element's result — so this bare `continue` inserts `null`:

```dang
[1, 2, 3].map { x => if (x == 2) { continue }; x }
```

In `.each` and `loop` there is no per-element result, so `continue` just
advances:

```dang
let sum = 0
[1, 2, 3, 4].each { x => if (x % 2 == 1) { continue }; sum += x }

sum
```

Both target the *nearest* enclosing block-taking call only, and an ordinary
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

And `return` is not an error: `rescue` cannot intercept it, even when the
same block could genuinely raise on another path:

```dang
sneaky(n: Int!): String! {
  {
    if (n == 0) { raise "zero" }
    return "returned"
  } rescue {
    err: Error! => `caught: ${err.message}`
  }
}

sneaky(1)
```

## `raise` / `rescue`

Errors are expressions with the same shape — postfix `rescue` attaches a
recovery to any expression and yields a value, and `raise` fits in any
branch — but they earn their own page: [#errors].
