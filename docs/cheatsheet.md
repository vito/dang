# Dang Cheat Sheet

## Variables & Visibility

```dang
pub x = 42              # public, type inferred (like Kotlin, Swift)
let y = "secret"        # private to enclosing scope (like Rust let)
pub z: Int! = 10        # explicit type annotation
pub w: String           # nullable (no default required)
```

`pub`/`let` = visibility, not mutability. All variables are reassignable. _(Like Kotlin `public`/`private`, not `val`/`var`)_

## Types & Null Safety

```dang
Int!          # non-null Int (like Kotlin Int, Swift Int)
Int           # nullable Int (like Kotlin Int?, Swift Int?)
String!       # non-null String
[Int!]!       # non-null list of non-null Ints
[String]      # nullable list of nullable Strings
{{a: Int!}}!  # non-null object/record type
```

Non-null by `!` suffix — same convention as **GraphQL**. Nullable is the default (opposite of most languages).

## Literals

```dang
42                          # Int
3.14                        # Float
"hello\nworld"              # String (escape sequences)
"""triple quoted"""         # multiline string (like Python, Kotlin, Swift)
true, false                 # Boolean (like JS, Python, Go)
null                        # null (like JS, Kotlin, SQL)
[1, 2, 3]                  # List (like Python, JS, Ruby)
{{name: "Alice", age: 30}} # Object/record (double braces)
```

## Operators

```dang
+  -  *  /  %              # arithmetic (universal)
==  !=  <  <=  >  >=       # comparison (universal)
and  or  !                 # logical (like Python and/or, Ruby)
??                          # null coalescing (like Kotlin, C#, Swift, JS)
::                          # type hint: expr :: Type (like Haskell)
```

## Functions & Methods

```dang
# function (slot with args and body)
pub add(a: Int!, b: Int!): Int! {
  a + b                     # last expression = return value (like Ruby, Rust, Scala)
}

# no-arg function (auto-called on access, like a getter)
pub greeting: String! {
  "hello"
}

# calling
add(1, 2)                   # positional args (universal)
add(a: 1, b: 2)             # named args (like Python, Kotlin, Swift)
greeting                    # auto-call, no parens needed
```

## Classes (Types)

```dang
type Person {
  pub name: String!
  pub age: Int! = 0         # default value

  pub greet: String! {
    "Hi, I'm " + self.name  # self = current instance (like Python, Ruby, Swift)
  }

  pub withAge(age: Int!): Person! {
    self.age = age           # copy-on-write, returns modified copy
    self
  }
}

# construction — public fields become constructor args
let p = Person("Alice", 30)
let p2 = Person(name: "Bob")  # named args, age defaults to 0
let p3 = Person.withAge(25)   # auto-call + chaining
```

## Explicit Constructors

```dang
type Greeter {
  pub greeting: String!

  new(name: String!) {       # like Python __init__, Ruby initialize
    self.greeting = "Hello, " + name + "!"
    self                     # must return self
  }
}
```

## Copy-on-Write Semantics

```dang
let a = Person("Alice")
let b = a.withAge(30)
# a.age == 0, b.age == 30   # original is NEVER mutated (like Clojure, Elixir)
```

All field assignment (`=`, `+=`) clones the object. Builder methods return `self` to pass the modified copy back. _(Like immutable data structures in Haskell, Clojure)_

## Control Flow

```dang
# if/else — expression-based (like Kotlin, Rust, Ruby)
pub result = if (x > 0) { "positive" } else { "non-positive" }

# else if
if (x > 0) { "pos" } else if (x < 0) { "neg" } else { "zero" }

# case — pattern matching (like Haskell case, Rust match, Scala match)
pub label = case (x) {
  1 => "one"
  2 => "two"
  else => "other"
}

# case without operand — condition chains (like Kotlin when, Haskell guards)
case {
  x > 100 => "big"
  x > 10  => "medium"
  else    => "small"
}

# type patterns on unions/interfaces (like Kotlin is, Scala match)
case (animal) {
  c: Cat => "cat: " + c.name
  d: Dog => "dog: " + d.name
  else   => "unknown"
}
```

## Loops

```dang
# for with condition (like C while)
for (x < 10) {
  x += 1
}

# infinite loop (like Rust loop)
for {
  if (done) { break }
  continue
}

# iteration via .each (like Ruby, Swift forEach)
[1, 2, 3].each { item =>
  print(item)
}

# with index
items.each { item, index =>
  print(index)
}
```

## Block Arguments

```dang
# higher-order functions via trailing block (like Ruby blocks, Kotlin lambdas)
[1, 2, 3].map { x => x * 2 }
[1, 2, 3].filter { x => x > 1 }
[1, 2, 3].reduce(0) { acc, x => acc + x }

# declaring a function that takes a block (& prefix like Ruby)
pub apply(&block(x: Int!): String!): String! {
  block(42)
}
apply() { x => toJSON(x) }
```

## Error Handling

```dang
# raise (like Python raise, Ruby raise, Kotlin throw)
raise "something went wrong"

# try/catch expression (like Kotlin try, Scala Try)
pub result = try {
  riskyOperation()
} catch {
  err => "failed: " + err.message
}

# typed catch clauses (like Java multi-catch, Kotlin catch)
try { op() } catch {
  v: ValidationError => "invalid: " + v.field
  n: NotFoundError   => "missing: " + n.resource
  err                => "other: " + err.message
}
```

## Interfaces & Unions

```dang
# interfaces (like Go, TypeScript, Java)
interface Named {
  pub name: String!
}

type Person implements Named {     # like Go implicit, Java explicit
  pub name: String!
}

type Book implements Named & Serializable {  # multiple interfaces with &
  pub name: String!
  pub data: String!
}

# unions — sum types (like TypeScript union, GraphQL union, Rust enum)
union Pet = Cat | Dog
```

## Enums

```dang
# enum declaration (like GraphQL, Rust, TypeScript)
enum Color { RED GREEN BLUE }

# access values as module fields
let c = Color.RED
assert { c == Color.RED }
```

## Other Features

```dang
# assertions (testing)
assert { 1 + 1 == 2 }
assert("custom message") { x > 0 }

# comments — # to end of line (like Python, Ruby, Shell)

# imports — bring a module's types and values into scope
import SomeModule

# docstrings (like Python)
"""Describe the thing below."""
pub myFun: String! { "hi" }

# object selection — GraphQL-style field picking
obj.{name, age}
obj.{... on User { name }, ... on Post { title }}

# scalar types — opaque string wrappers (like GraphQL custom scalars)
scalar Timestamp

# string methods
"hello".toUpper       # "HELLO"
"a,b,c".split(",")   # ["a", "b", "c"]
"  hi  ".trim         # "hi"

# list methods
[1, 2, 3].length      # 3
[1, 2, 3].contains(2) # true
[1, 2].join(", ")     # "1, 2"
```

## Key Principles

| Concept | Behavior | Similar to |
|---|---|---|
| Immutability | Copy-on-write; originals never mutated | Clojure, Elixir, Haskell |
| Type inference | Hindley-Milner; rarely need annotations | Haskell, OCaml, Rust |
| Null safety | `T` = nullable, `T!` = non-null | GraphQL (exact), Kotlin (inverted) |
| Auto-calling | Zero-arg functions called on access | Ruby methods, Scala def |
| Expressions | `if`, `case`, `try` all return values | Kotlin, Rust, Scala, Ruby |
| GraphQL backend | Types derived from GraphQL schema | _(unique to Dang)_ |
| `self` | Dynamic scope; needed for writes | Python, Ruby, Swift |
| Block args | Trailing `{ x => ... }` syntax | Ruby blocks, Kotlin trailing lambdas |
