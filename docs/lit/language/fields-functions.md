\use-plugin{dang}
\literate-fences

# Fields and functions {#fields-functions}

> Meta: introduce the *field* concept first and let the examples carry it — the whole rule is just: `let` makes a field local, a type makes it public, and a bare `name = value` reassigns. Then the four "feels weird at first" function points: zero-arity has no parens; the body has no `return`; positional args can mix with named; `&fn` is for references. Don't mention `pub` (it's legacy and formatted away). Object-context behavior lives in [#objects] — link, don't duplicate.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## Fields {#fields}

A **field** is a named, typed thing — a value, a function, or a computed
expression — declared in the current scope:

* Top-level fields, declared across `.dang` files in the same directory
* Type-level fields declared within an [#objects] or [#interfaces-unions]
* Block-level fields declared within the nearest enclosing `{` and `}`

A field is recognized by its **shape** — a name followed by a type, an argument
list, or a block body:

```dang
type Profile {
  name: String!                          # typed field
  greet: String! { `hi, ${name}!` }      # computed field / method
  plus(x: Int!): Int! { x + bonus }      # method with args
  bonus: Int! = 100                      # typed field with default
  nickname: String = null                # nullable
  let secret = "shhh"                    # local field (untyped is fine)
}

let ada = Profile(name: "Ada")

ada.greet
```

A field with no value (`name: String!`) is a required constructor parameter
in a type, as `name` is above. A `let` field with no default is required
too, while a `let` field *with* a default is not a parameter at all:

```dang
type Token {
  let value: String!
  let prefix = "tok_"
  masked: String! { prefix + "***" }
}

Token("abc123").masked
```

A field's default runs once, while the object is being built, so it sees
only values: the other fields and the enclosing scope, not `self` or
methods (see [#field-defaults]). Inside a type, its own `let` fields win over
outer bindings of the same name:

```dang
let label = "outer"

type Tag {
  let label = "inner"
  show: String! { label }
}

Tag.show
```

### Visibility

A field is **public** when it declares a type, and **local** when it starts with
`let`. `ada.greet` and `ada.bonus` above are public; `secret` is local.

`let` introduces a local field. A *type-level* or *top-level* `let` is
private to the module: any `.dang` file in the same directory can read it,
but code that imports the module can't (see [#modules]). That's how you share
helpers across a module without exporting them. A *block-level* `let` is a
fresh local.

### Declaration vs. reassignment

A bare `name = value` reassigns an existing field — see [#mutation]. To declare a
new field instead, give it a type or introduce it with `let`:

```dang
let total = 0   # declares a local field
total = 5       # reassigns it
total += 1      # compound update

total
```

The new value must fit the field's declared type:

```dang-failure
total = "six"
```

- `name = newValue` mutates an existing field (or local/arg of that name)
- only top-level script code can reassign a top-level field; functions and
  methods can't — see [#top-level-bindings]
- `+=` for compound update (Int add, String/List concatenation)
- nested-path mutation is copy-on-write: copying an object then writing
  `m.a.b.c = 2` leaves the original unchanged
- inside a `type`'s methods, bare `name = ...` resolves to the field when
  nothing shadows it; if a parameter (or local) shadows the field name,
  **field** mutation requires `self.name = ...` — see [#lexical-scope]
- the type body itself can't reassign the type's fields — see [#field-defaults]

Assigning a bare function name to a function-valued field *calls* the
function. Use `&name` to assign the function itself (see *Function
references* below):

```dang
type Clock {
  now: String! { "noon" }
}

later: String! { "midnight" }
```

```dang-failure
let clock = Clock
clock.now = later
```

```dang
let clock = Clock
clock.now = &later
clock.now
```

### Docstrings

A `"""..."""` literal right before a declaration documents it. This works on
modules, types, fields, functions, function parameters, directives, and
directive arguments:

```dang
"""
Greets the named user.
"""
welcome(
  """name of the person to greet"""
  name: String!
): String! {
  `welcome, ${name}!`
}

welcome("Ada")
```

## Functions {#functions}

A function is a field with an argument list. The last expression of the body
is the result, so the normal result needs no `return`:

```dang
add(a: Int!, b: Int!): Int! { a + b }

add(2, 3)
```

`return expr` is for leaving *early*. It unwinds through enclosing blocks
and loops, and also works in `new(...)` constructors. Outside a function,
method, or constructor it's an error (see [#control-flow]). Forms in a body
are separated by newlines or `;`.

### Zero-arity and auto-calling

A function with no arguments drops the parentheses, both where it's declared
and where it's called. It runs when referenced, like a property:

```dang
motd: String! { "hello" }

motd
```

`&motd` gets the function without calling it (see below). GraphQL fields
with no required arguments work the same way. With the bundled `Demo`
schema (see [#interop]):

```dang
import Demo

serverInfo.version
```

### Arguments

Arguments can be named, positional, or both:

```dang
[add(a: 10, b: 20), add(10, 20), add(10, b: 20)]
```

Positional arguments come first, then named ones. The same rule applies to
directive applications:

```dang-failure
add(a: 10, 20)
```

Defaults:

- declared on the parameter: `name: String! = "world"`
- a default may reference *earlier parameters* in the same list; the param shadows any outer binding of the same name
- in a free function a default may reference enclosing-scope names
- in a method a default may reference fields of the same type
- a nullable arg passed `null` falls back to its default; a nullable arg with no default stays `null`
- `new(...)` params follow the same rules, except that their defaults follow
  the field-default rule: they see the fields' values and earlier params, not
  `self` — see [#field-defaults]

```dang
span(start: Int! = 0, end: Int! = start + 10): [Int!]! { [start, end] }

[span, span(5), span(5, 6)]
```

A non-null parameter *with* a default (`name: String! = "world"`) is
**nullable on the caller's side but non-null on the receiver's side**.
Callers may omit it, pass `null`, or pass a nullable `String`; each falls
back to the default. Inside the body the parameter is a plain `String!`, so
it needs no null checks. Prefer this over a nullable parameter whenever a
sensible default (even a sentinel like `""`) exists: the caller can leave
it out, and the body never sees null.

```dang
salute(name: String! = "world"): String! { "hi " + name }

let nobody = null :: String

[salute, salute(null), salute(nobody), salute("Ada")]
```

### Function references: `&fn`

`&` (see [#operators]) gives you a function without calling it. You need it
to store a function in a function-typed field, or to pass one as an
argument. Here `Greeter.hello` is swapped for another function:

```dang
type Greeter {
  hello(name: String!): String! { `hello, ${name}` }
  greet(name: String!): String! { hello(name) }
}

shout(name: String!): String! { `HEY ${name.toUpper}` }

let loud = Greeter
loud.hello = &shout
loud.greet("ada")
```

A function stored where another function type is expected must accept every
argument that type promises (parameters are contravariant, results
covariant), and name its parameters the same, in the same order: callers
name arguments by the target's signature. `wave` takes `who`, not `name`:

```dang
wave(who: String!): String! { `*waves at ${who}*` }
```

```dang-failure
let polite = Greeter
polite.hello = &wave
```

Blocks are the exception: a block takes on the parameter names of the
signature it's passed for.

- `&greet` captures a zero-arity function/method without auto-calling it; it stays live and re-reads its closure each call
- combined with `.method` selection: `&user.greet`
- a captured ref must still satisfy the target's block-parameter signature
- a method ref is lexical: wherever it is stored or called from, it runs against the `self` where it was written, never the caller's — see [#lexical-scope]
- inside a method, `&helper` and `&self.helper` read `self` when called, so they see field writes made after the ref was taken; a ref through any other receiver (`&r.helper`) holds that value, since values are immutable
- a ref to a closure that writes outside itself can be called or passed as an argument, but not stored or returned — see [#impure-closures]. Named functions and methods only write their own copy of `self`, so refs to them can go anywhere

### Nested functions

Functions declared inside a function, method, or `new()` body are closures,
like blocks. They see the enclosing scope lexically, so its arguments and
locals shadow fields inside them just as in the body. Their writes land in
the enclosing scope, so `bump` here counts up across calls:

```dang
tally: [Int!]! {
  let n = 0
  bump: Int! { n += 1; n }
  let first = bump
  let second = bump
  [first, second, n]
}

tally
```

- inside a method they share its `self`: field writes inside them (bare or `self.`) accumulate into the method's copy
- a nested function that writes the method's locals or `self` can't be stored or returned, or called from a `{{ }}` field — see [#impure-closures], [#concurrent-fields]
- editor hover on a closure lists what it writes outside itself and which block parameters it calls, e.g. `// Effects: writes n; calls block`
- sibling methods differ: each call forks `self` on its own, and its changes surface only through a returned `self`

> Meta: link forward to [#blocks] — block arguments are the more common form of "pass code." Function refs are for the cases where you need a true callable to store or rebind.

## Forward references

tl;dr: they work.

`.dang` files within a directory share a common scope, like in Go. A field
may use fields declared later in the same file or in sibling files, and
types may use types declared later:

```dang
area: Int! { width * height }
width: Int! = 3
height: Int! = 4

area
```

A *direct* initializer cycle is rejected before the script runs:

```dang-failure
first: Int! = second
second: Int! = first
```

A cycle hidden behind an auto-called function or a constructor default is
caught at runtime, when the variable is first read. Run as a script, this
reports `initialization cycle while evaluating variable "start"`:

```dang-static
start: Int! = next
next: Int! { start + 1 }
```
