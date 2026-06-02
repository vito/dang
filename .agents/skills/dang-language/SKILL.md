---
name: dang-language
description: Dang surface-language reference — syntax for types, methods, mutation, blocks, errors, and GraphQL polymorphism. Use when writing or reviewing `.dang` code.
---

# Dang Language Reference

Dang is a strongly-typed scripting language with Hindley-Milner inference and
GraphQL-derived types. Type annotations are usually optional; `T` is nullable,
`T!` is non-null.

## Declarations

```dang
pub x = 42              # public, type inferred
let secret = "hidden"   # private (only visible in enclosing scope)
pub y: String! = "hi"   # explicit type
pub z: Int              # declaration without value
```

Inside a `type`, `pub` fields are externally visible; `let` fields are
accessible only to the type's own methods.

## Functions and methods

```dang
pub add(a: Int!, b: Int!): Int! { a + b }

type Person {
  pub name: String!
  pub greet: String! { "Hello, I'm " + name }   # zero-arg method
}
```

**Auto-calling**: zero-argument functions and all-default-args constructors are
called automatically when referenced by name. `Person.greet` invokes `greet`;
`Defaults` (a type with all-default fields) constructs an instance.

## Constructors

Two forms — implicit (field-derived) and explicit (`new()`).

```dang
# Field-derived: public fields become positional args in declaration order
type Config {
  pub host: String!
  pub port: Int! = 8080
}
let c = Config("localhost", 9090)
let c2 = Config(host: "localhost")   # named args

# Explicit new() — must return self
type Greeter {
  pub greeting: String!
  new(name: String!) {
    self.greeting = "Hello, " + name
    self
  }
}
```

In constructors, bare field assignment works when names don't shadow:

```dang
type Vector {
  pub x: Int! = 0
  new(vx: Int!) { x = vx; self }   # bare ok, no shadow
}

type Point {
  pub x: Int!
  new(x: Int!) { self.x = x; self }   # self. required, parameter shadows
}
```

## Mutation and copy-on-write

`=` reassigns, `+=` adds-and-reassigns (Int, String, List). Field assignment
clones the root and replaces the binding — originals are never mutated.

```dang
let a = {{x: 1}}
let b = a
b.x = 2
assert { a.x == 1 }   # original unchanged
```

Methods that mutate `self` must return `self` to surface the updated copy.
Each method call operates on an isolated copy of the receiver, so chaining is
the natural pattern:

```dang
type Counter {
  pub value: Int!
  pub incr: Counter! { value += 1; self }
}
assert { Counter(0).incr.incr.incr.value == 3 }
```

Block scoping: a bare `x = ...` inside `{ ... }` updates the outer slot. A
`let x` inside the block shadows.

## Block arguments

Functions can take a block (Ruby-style). Declare with `&name(...): T`; call
with `{ ... }`:

```dang
pub twice(&block(x: Int!): Int!): Int! { block(1) + block(2) }
pub r = twice() { x -> x * 10 }   # 30

# Standard library uses these heavily
pub doubled = [1, 2, 3].map { x -> x * 2 }
```

Block arg must come last in the signature. Only one per function. Blocks can
omit unused parameters: `{ "constant" }`.

Closures inside a method/constructor share `self` across iterations, so
patterns like `source.each { item => self.items += [item] }` accumulate as
expected.

## Errors

```dang
raise "something went wrong"             # sugar for Error(message: "...")
raise ValidationError(message: "...", field: "name")

try {
  doSomething()
} catch {
  v: ValidationError => "invalid " + v.field
  n: NotFoundError   => "missing " + n.resource
  err                => "other: " + err.message
}
```

Custom error types implement the built-in `Error` interface (one field:
`message: String!`). `try`/`catch` catches both `raise` and runtime errors
(null access, division by zero, GraphQL failures). All clauses and the body
must return the same type. Unmatched errors re-raise.

## Flow-sensitive null checking

```dang
if (maybeValue != null) {
  maybeValue + " ok"   # narrowed from String to String! here
} else {
  "no value"
}
```

Works for `x != null`, `x == null`, and reversed forms. **Does not** handle
`&&`, `||`, `!`, or compound expressions — keep the check immediate.

## Type discrimination — `case`

For union and interface types, discriminate with a typed binding:

```dang
case (value) {
  v: ValidationError => "field " + v.field
  n: NotFoundError   => "missing " + n.resource
  else               => "other"
}
```

Inside the clause, `v` has the narrowed concrete type, so member-specific
fields are accessible.

## Polymorphism

```dang
interface Named { pub name: String! }

type Person implements Named {
  pub name: String!
  pub age: Int!
}

type Book implements Named & Serializable {
  pub name: String!
  pub data: String!
}

union Pet = Cat | Dog   # flat unions only; members must be object types
```

A type implementing an interface must provide all interface fields; method
return types are covariant, argument types contravariant, and any extra
arguments must be optional.

## Regex

Backtick templates auto-coerce to the `Regexp` scalar at call sites. A
`Match` is a first-class object with positional and named captures.

```dang
"call 555-1212".containsMatch(`\d+`)              # Boolean!
"call 555-1212".match(`(\d+)`)                    # Match (nullable)
"a1 b22 c333".matchAll(`\d+`)                     # [Match!]!
"a, b ,  c".splitMatches(`\s*,\s*`)               # ["a", "b", "c"]

# replaceMatches uses Go-style $0 / $1 / $name / ${name} backref expansion
"555-1212".replaceMatches(
  `(?P<area>\d{3})-(?P<num>\d{4})`,
  with: "$area.$num",
)                                                 # "555.1212"

# rewriteMatches takes a block and receives a Match for each occurrence
"hello world".rewriteMatches(`\w+`) { m =>
  m.string.toUpper
}                                                 # "HELLO WORLD"
```

`Match` fields: `string` (whole match), `captures` (positional; index 0 is
`$1`), `capture(name)` (named; null if absent), `start`, `end` (byte
offsets). Unmatched optional groups surface as `""`. Pattern syntax is Go
RE2 — named groups use `(?P<name>...)`.

## GraphQL integration

- **Enums** load from the schema: `Status.ACTIVE`, `Status.PENDING`. Enum
  values are only comparable to other values of the same enum type.
- **Scalars** (`scalar Timestamp`) are opaque string-backed distinct types —
  not interchangeable with `String`.
- **Object selection** `obj.{field1, field2}` projects a GraphQL object.
- **Inline fragments** discriminate union/interface query results:

  ```dang
  search(query: "foo").{
    ... on User { name, email }
    ... on Post { title, content }
  }
  ```

## Testing

Place `assert { expr }` directly in `.dang` files under `tests/`. Files
matching `tests/test_*.dang` run as language tests; files under
`tests/errors/` expect an error and compare against
`tests/testdata/<name>.golden`. See the `testing` skill for the runner
commands and golden-update flow.
