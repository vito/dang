\use-plugin{dang}

# Objects (`type`) {#objects}

> Meta: lead with "a `type` declares both a type and its prototype constructor." That's the unusual thing. Cross-link to [#mutation] — don't try to explain CoW here.

## Declaration

```dang
type Person {
  name: String!
  age: Int! = 0

  greet: String! {
    "hi, I'm " + name
  }
}
```

- declares a type `Person` and a constructor function `Person`
- members are fields or methods, indistinguishable in syntax (`name` + optional `: Type`, `= default`, or `{ body }`); `let` makes a member private

## Public vs. private members

- a bare member is **public** — readable from outside the type; public is the default for a `type`
- `let` — readable only inside the type's own methods/defaults (private)
- `pub` is still accepted as an explicit public marker, but it is redundant and `dang fmt` removes it (see [#fields])
- whether a member is a **constructor parameter** depends on having NO default, NOT on visibility:
  - `x: T!` (no default) → required positional param
  - `x: T! = d` → optional param (default `d`)
  - `let x: T!` (no default) → required positional param too, e.g. `Foo("public_value", "private_value")`
  - `let x: T! = d` → NOT a param; the default is used
  - method/computed members (have a `{ body }`) are never constructor params

## Implicit constructor

- one positional parameter per non-default field, in **declaration order** (NOT required-first-then-defaults — a defaulted field may precede a required one; positional args still line up by declaration order)
  - e.g. `Mixed { pub publicWithDefault = "default"; let privateRequired: Int! }` constructs as `Mixed("default", 42)`
- can also be called with named arguments (`Counter(value: 42)`)
- field defaults are evaluated with `self` bound, so a default may reference earlier/sibling fields, e.g. `combined = prefix + "_" + suffix`

```dang
Person("Alice", age: 30)
Person(name: "Alice")
```

## Zero-arg auto-construction

- a type whose constructor needs nothing (all fields have defaults / no required params) constructs on bare reference:
  `let p = Person` ≡ `let p = Person()`
- a bare reference to a function requiring a **block argument** is an error (`function requires a block argument`) — same as calling it without a block; use `&name` to reference it without calling

## Explicit constructor: `new`

```dang
type Greeter {
  greeting: String!

  new(name: String!) {
    self.greeting = "hello, " + name
    self
  }
}
```

- declared as `new(args) { body }` or `new { body }` (no parens when no args)
- no `pub` and no return-type annotation — both are errors ("'new' is a constructor, not a method")
- `new` is only valid inside a `type` body
- overrides the implicit constructor (the type's fields no longer auto-become params; `new`'s arg list defines the signature)
- constructor args are *local* bindings — distinct from fields, even when same-named:
  - mutable: `foo = foo + 10` / `foo += foo` rebinds the arg, does NOT touch `self.foo`
  - shadow same-named fields and outer scope; bare name = arg, field write needs `self.field =`
  - NOT visible in method bodies — only in `new`
- must return the constructed type (`self`, or a method chain that returns it): body's last expression must be `Foo!`
  - returning another type errors: `new() must return Wrong!, got String!`
  - returning `null` errors
- may end by chaining other methods, which propagate their forked `self`, e.g. `self.withSuffix("!")`
- self-field mutation inside a loop accumulates into one fork
- can accept block args: `new(&condition: Boolean!) { ... }`; a block param uses `&name`

## `self`

- bound during constructor and method execution (and during field-default / computed-field evaluation)
- bare names inside a method resolve against the current receiver first: bare `name` reads `self.name`, bare `incrBy(1)` calls `self.incrBy(1)`
- field **reads** never need `self.`; for **assignment**, both forms work:
  - bare `a += 1` / `value = v` on a field forks `self` and sets the field
  - `self.field = ...` is the explicit form; required only to disambiguate from a same-named local/arg
  - bare assignment binds a *local/arg* when the name is one in scope (shadowing); otherwise targets the field
- assigning a field forks the receiver (see [#mutation])
- `self` is the value returned by chainable methods (the final `self` returns the accumulated fork)

## Computed fields

- a member with a type and a body but no arg list is a computed field — a zero-arg function evaluated on `self` each access:

```dang
fullName: String! { firstName + " " + lastName }
```

- accessed like a plain field (`obj.fullName`, no call parens); recomputes against the current receiver
- a defaulted-value member (`pub computedField = config.name + "_computed"`) is computed once at construction; a `{ body }` computed field is re-evaluated per access

## Implements

```dang
type Person implements Named & Identifiable { name: String!, id: String! }
```

- see [#interfaces-unions]

## Forward references

- methods can reference types and fields defined later in the same file
- references also resolve across files in a directory module, order-independently
- defaults can reference sibling fields (evaluated with `self` bound)

> Meta: user-defined `type`s and imported GraphQL **input** types share the same `Type(args)` construction syntax. Schema input types are constructed exactly like local types: `CreateUserInput(name: "Alice", email: "...")` passed as `Mutation.createUser(input: ...)`. See [#interop].
