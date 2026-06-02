\use-plugin{dang}

# Classes (`type`) {#classes}

> Meta: lead with "a `type` declares both a type and its prototype constructor." That's the unusual thing. Cross-link to [mutation](./mutation.md) — don't try to explain CoW here.

## Declaration

```dang
type Person {
  pub name: String!
  pub age: Int! = 0

  pub greet: String! {
    "hi, I'm " + name
  }
}
```

- declares a type `Person` and a constructor function `Person`
- members (slots) are fields or methods, indistinguishable in syntax

## Public vs. private members

- `pub` — readable from outside the type and part of the constructor signature
- `let` — readable only inside methods
- `let` with a default is **not** a constructor parameter; without a default it is

## Implicit constructor

- positional parameters in declaration order
- required slots first, defaults after
- can also be called with named arguments

```dang
Person("Alice", age: 30)
Person(name: "Alice")
```

## Zero-arg auto-construction

- a type whose constructor needs nothing constructs on reference:
  `let p = Person` ≡ `let p = Person()`

## Explicit constructor: `new`

```dang
type Greeter {
  pub greeting: String!

  new(name: String!) {
    self.greeting = "hello, " + name
    self
  }
}
```

- overrides the implicit constructor
- constructor args are *local* — distinct from fields
- must return `self`
- can accept block args: `new(&condition: Boolean!) { ... }`

## `self`

- bound during constructor and method execution
- bare names inside a method resolve to slots on `self` first
- assigning `self.field = ...` forks the receiver (see [mutation](./mutation.md))
- `self` is the value returned by chainable methods

## Computed fields

- a field with a body and no arg list is computed on `self`:

```dang
pub fullName: String! { firstName + " " + lastName }
```

## Implements

```dang
type Person implements Named & Identifiable { ... }
```

- see [interfaces](./interfaces-unions.md)

## Forward references

- methods can reference types and slots defined later in the file/directory
- defaults can reference siblings

> Meta: mention that user-defined `type`s can be used as GraphQL inputs to imported APIs once the schema allows it; that's a frequent question.
