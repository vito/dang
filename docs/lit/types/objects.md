\use-plugin{dang}
\literate-fences

# Objects (`type`) {#objects}

> Meta: lead with "a `type` declares both a type and its prototype constructor." That's the unusual thing. Cross-link to [#mutation] — don't try to explain CoW here.

A `type` declaration makes two things: a type, and a constructor function
of the same name.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## Declaration

```dang
type Person {
  name: String!
  age: Int! = 0

  greet: String! {
    "hi, I'm " + name
  }
}

Person("Alice").greet
```

- declares a type `Person` and a constructor function `Person`
- members are fields or methods, indistinguishable in syntax (`name` + optional `: Type`, `= default`, or `{ body }`); `let` makes a member private

Type bodies contain **declarations only**: fields, methods, nested types,
imports, directives, `new()`, and static `self { }` blocks. Naked expressions,
`print`, `assert`, conditionals, loops, and reassignment belong in a method or
an explicit constructor, not directly in the type body:

```dang-failure
type Noisy {
  print("constructed")
}
```

Put constructor actions in `new()`, returning `self`. They run once each time
the constructor is called:

```dang
type Announced {
  new() {
    print("constructed")
    self
  }
}

Announced()
```

Declaration-only does not mean effect-free initialization. A field default
may contain a block with actions, subject to the normal default and capture
rules (see [#field-defaults]). Methods and constructors may also contain
statements. Declaring a type inside an unused function does not execute its
initializers.

## Public vs. private members

- a bare member is **public** — readable from outside the type; public is the default for a `type`
- `let` — private to the declaring **module**: readable by any type or method in the same module, rejected across a module boundary (see [#modules])
- `pub` is still accepted as an explicit public marker, but it is redundant and `dang fmt` removes it (see [#fields])

`let` privacy is module-scoped, not type-scoped, so another type in the
same module can read a `let` member:

```dang
type Account { let secret: String! = "hunter2" }
type Vault { unlock(a: Account!): String! { a.secret } }

Vault.unlock(Account)
```

Whether a member is a **constructor parameter** depends on whether it has a
default, not on its visibility:

- `x: T!` (no default) → required positional param
- `x: T! = d` → optional param (default `d`)
- `let x: T!` (no default) → required positional param too
- `let x: T! = d` → NOT a param; the default is used
- method/computed members (have a `{ body }`) are never constructor params

## Implicit constructor

Without a `new`, the constructor takes the parameters listed above, in
**declaration order**. It can be called with positional
arguments, named ones, or both:

```dang
[Person("Alice", age: 30).age, Person(name: "Bob").age, Person("Cy", 41).age]
```

Declaration order means a defaulted field can come before a required one,
and positional arguments still line up with the declarations. Here the first
positional argument goes to `label`:

```dang
type Ticket {
  label: String! = "general"
  let seat: Int!
  show: String! { `${label} #${seat}` }
}

Ticket("vip", 12).show
```

```dang-failure
Ticket(12)
```

Name the argument to skip the defaulted field: `Ticket(seat: 12)`.

Field defaults are the constructor's parameter defaults, so a default may use
other fields. They run while the object is built, so they see only values,
not `self` or methods (see [#field-defaults]):

```dang
type Slot {
  prefix: String!
  suffix: String! = "end"
  combined: String! = prefix + "_" + suffix
}

Slot("start").combined
```

## Zero-arg auto-construction

A type whose constructor needs nothing (every field has a default)
constructs on bare reference: `Settings` is the same as `Settings()`.

```dang
type Settings { verbose: Boolean! = false }

let settings = Settings
settings.verbose
```

A bare reference to a function that requires a **block argument** is an
error, the same as calling it without a block. Use `&name` to reference it
without calling it:

```dang
twice(&body: Int!): Int! { body + body }
```

```dang-failure
let doubled = twice
```

## Explicit constructor: `new`

```dang
type Greeter {
  greeting: String!

  new(name: String!) {
    self.greeting = "hello, " + name
    self
  }
}

Greeter("Ada").greeting
```

- declared as `new(args) { body }` or `new { body }` (no parens when no args)
- overrides the implicit constructor (the type's fields no longer auto-become params; `new`'s arg list defines the signature)
- `new`'s parameter defaults follow the same rule as field defaults, and run after them (see [#field-defaults])
- may end by chaining other methods, which propagate their forked `self`, e.g. `self.withSuffix("!")`
- self-field mutation inside a loop accumulates into one fork
- can accept block args: `new(&condition: Boolean!) { ... }`; a block param uses `&name`

`new` takes no `pub` and no return type, and is only valid inside a `type`
body:

```dang-failure
type Gadget {
  size: Int! = 1
  new(): Gadget! { self }
}
```

Constructor arguments are *local* bindings, separate from fields even when
they share a name. Reassigning `text` here rebinds the argument; the field
is written with `self.text =`. The arguments are visible only in `new`, not
in methods:

```dang
type Shout {
  text: String!

  new(text: String!) {
    text = text.toUpper
    self.text = text + "!"
    self
  }
}

Shout("hey").text
```

The body must return the constructed type (`self`, or a method chain that
returns it). Returning another type, or `null`, is an error:

```dang-failure
type Broken {
  size: Int! = 1
  new { "not a Broken" }
}
```

A constructor can end with a method chain, and its field writes add up, even
inside a loop:

```dang
type Counter {
  value: Int! = 0
  bump: Counter! { value += 1; self }

  new(times: Int!) {
    [1, 2, 3].each { i => if (i <= times) { value += 1 } }
    self.bump
  }
}

Counter(2).value
```

## `self`

- bound in `new`'s body, methods, and computed fields; not in defaults, which run before the object is finished (see [#field-defaults])
- bare names inside a method resolve against the current receiver first: bare `name` reads `self.name`, bare `incrBy(1)` calls `self.incrBy(1)`
- field **reads** never need `self.`; for **assignment**, both forms work:
  - bare `a += 1` / `value = v` on a field forks `self` and sets the field
  - `self.field = ...` is the explicit form; required only to disambiguate from a same-named local/arg
  - bare assignment binds a *local/arg* when the name is one in scope (shadowing); otherwise targets the field
- assigning a field forks the receiver (see [#mutation])
- `self` is the value returned by chainable methods (the final `self` returns the accumulated fork)

## Computed fields

A member with a type and a body but no argument list is a computed field. It
reads like a plain field (`obj.full`, no parentheses) and runs again on every
access, against the current receiver. A member with a default value, by
contrast, is computed once, when the object is built:

```dang
type FullName {
  first: String!
  last: String!
  full: String! { first + " " + last }       # every access
  original: String! = first + " " + last     # once, at construction
}

let author = FullName("Ada", "Lovelace")
author.first = "Augusta"

[author.full, author.original]
```

## Static members {#static-members}

A `self { }` block declares statics. They live on the type itself, reached
as `Type.member`, never on an instance. Like the instance body, a static body
contains declarations only; put actions in a static method or a field
initializer. `new()` and a directly nested `self { }` are not allowed inside
it:

```dang
type Color {
  r: Int! = 0
  g: Int! = 0
  b: Int! = 0

  luminance: Int! { (r + g + b) / 3 }        # an instance member

  self {
    white: Color! { Color(r: 255, g: 255, b: 255) }   # a factory method
    fromGray(v: Int!): Color! { Color(r: v, g: v, b: v) }
    grayMid: Color! { fromGray(128) }                 # a static calling a sibling static
    let scale = 2                                      # a private (let) static helper
    doubled(v: Int!): Int! { v * scale }
  }
}

[Color.white.luminance, Color.fromGray(10).r, Color.grayMid.g, Color.doubled(7)]
```

Instances don't have the statics, and a bare all-default type still
auto-constructs, so `Color.luminance` reads an instance member:

```dang
[Color(r: 3, g: 6, b: 9).luminance, Color.luminance]
```

```dang-failure
Color(r: 1).white
```

Static stored fields hold singleton values on the type:

```dang
type Config {
  host: String! = "localhost"

  self {
    limit: Int! = 100
    default: Config! = Config    # a static holding a constructed instance
  }
}

Config.limit
```

```dang
Config.default.host
```

Statics are immutable, from everywhere (see [#top-level-bindings]):

```dang-failure
Config.limit = 5
```

A scalar may also carry a `self { }` block, including static *stored*
fields, even though a scalar can't have instance stored fields (see
[#scalar-bodies]).

## Implements

```dang
interface Named { name: String! }
interface Identifiable { id: String! }

type Member implements Named & Identifiable {
  name: String!
  id: String!
}

describe(n: Named!): String! { `named ${n.name}` }

describe(Member("Ada", "m1"))
```

- see [#interfaces-unions]

## Forward references

- methods can reference types and fields defined later in the same file
- references also resolve across files in a directory module, order-independently
- defaults can reference sibling fields, including ones declared after them

```dang
type Order {
  total: Int! = price * quantity   # declared before the fields it uses
  price: Int! = 5
  quantity: Int! = 2
  receipt: Receipt! { Receipt(total) }
}

type Receipt { amount: Int! }

Order.receipt.amount
```

> Meta: user-defined `type`s and imported GraphQL **input** types share the same `Type(args)` construction syntax. Schema input types are constructed exactly like local types: `CreateUserInput(name: "Alice", email: "...")` passed as `Mutation.createUser(input: ...)`. See [#interop].
