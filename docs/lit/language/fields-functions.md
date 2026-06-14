\use-plugin{dang}

# Fields and functions {#fields-functions}

> Meta: introduce the *field* concept first and let the examples carry it — the whole rule is just: `let` makes a field local, a type makes it public, and a bare `name = value` reassigns. Then the four "feels weird at first" function points: zero-arity has no parens; the body has no `return`; positional args can mix with named; `&fn` is for references. Don't mention `pub` (it's legacy and formatted away). Object-context behavior lives in [#objects] — link, don't duplicate.

## Fields {#fields}

A **field** is a named, typed thing — a value, a function, or a computed
expression — declared in the current scope:

* Top-level fields, declared across `.dang` files in the same directory
* Type-level fields declared within an [#objects] or [#interfaces-unions]
* Block-level fields declared within the nearest enclosing `{` and `}`

A field is recognized by its **shape** — a name followed by a type, an argument
list, or a block body:

```dang
name: String!             # typed field
greet: String! { "hi!" }  # computed field / method
add(x: Int!): Int! { x }  # method with args
y: Int! = 100             # typed field with default
maybe: String = null      # nullable
let secret = "shhh"       # local field (untyped is fine)
```

- fields without a value (`name: Type`) act as required constructor parameters
  (in objects) or unresolved declarations
- a `let` (private) required field with no default still becomes a required
  constructor parameter; a `let` field *with* a default does not
- private fields with defaults are preferred over outer-scope bindings of the
  same name inside the type

### Visibility

A field is **public** when it declares a type, and **local** when it starts with
`let`:

```dang
name: String!              # public field
greet: String! { "hi!" }   # public method
count: Int! = 0            # public field with a default
let secret = "shhh"        # local field
```

`let` introduces a local field. A *type-level* `let` is readable only inside that
type's own methods and defaults; a *block-level* `let` is a fresh local; a
*top-level* `let` is module-scoped — visible to every `.dang` file in the
directory but not exported, which is how you share helpers across a module (see
[#modules]).

### Declaration vs. reassignment

A bare `name = value` reassigns an existing field — see [#mutation]. To declare a
new field instead, give it a type or introduce it with `let`:

```dang
total: Int! = 0   # declares a public field
let total = 0     # declares a local field
total = 5         # reassigns the existing field
```

- `name = newValue` mutates an existing field (or local/arg of that name)
- `+=` for compound update (Int add, String/List concatenation)
- type must remain assignable to the field's declared type
- assigning a function-valued field a bare function name *calls* it; use `&name`
  to assign the function itself — see the function references section below
- nested-path mutation is copy-on-write: copying an object then writing
  `m.a.b.c = 2` leaves the original unchanged
- inside a `type`, bare `name = ...` resolves to the field when nothing shadows
  it; if a parameter (or local) shadows the field name, **field** mutation
  requires `self.name = ...` — see [#mutation]

### Docstrings

- a `"""..."""` literal immediately before a declaration attaches as documentation
- works on modules, types, fields, functions, function parameters, directives,
  directive args

```dang
"""
Greets the named user.
"""
greet(name: String!): String! {
  `hi, ${name}`
}
```

— and on parameters:

```dang
greet(
  """name of the person to greet"""
  name: String!
): String! { "hey, ${name}!" }
```

## Functions {#functions}

A function is a field with an argument list:

```dang
add(a: Int!, b: Int!): Int! { a + b }
```

- name, parameter list, return type, body
- last expression is the result — no `return` keyword needed for the normal result
- `return expr` is available for *early* exit and unwinds through enclosing blocks/loops; also valid in `new(...)` constructors
- `return` outside any function/method/constructor errors: `return outside of function`
- multi-statement bodies separate forms with newlines or `;` (commas are for collections and argument lists, not statements)

### Zero-arity and auto-calling

```dang
motd: String! { "hello" }
```

- omit the parentheses; the function is a *field* with a function body
- callers also omit the parens: `motd`, not `motd()`
- a zero-arity function/method *invokes* on reference, like a property
- `&name` (see below) suppresses invocation
- the same rule applies to GraphQL fields with no required args

### Arguments

```dang
greet(name: "Alice")   # named
greet("Alice")         # positional
add(10, b: 20)         # mixed
```

- positional args come first, then named; `add(a: 10, 20)` is an error:
  `positional arguments must come before named arguments` (the same rule
  applies to directive applications)

Defaults:

- declared on the parameter: `name: String! = "world"`
- a default may reference *earlier parameters* in the same list; the param shadows any outer binding of the same name
- in a free function a default may reference enclosing-scope names
- in a method a default may reference fields of the same type
- a nullable arg passed `null` falls back to its default; a nullable arg with no default stays `null`
- same default rules apply to `new(...)` constructor params

A non-null parameter *with* a default (`name: String! = "world"`) is **nullable on the
caller's side but non-null on the receiver's side**. Callers may omit it, pass `null`, or
pass a nullable `String`; every such case falls back to the default. Inside the body the
parameter is a plain `String!`, so no null checks or assertions are needed. This lets an API
excise null at the boundary — prefer a non-null-with-default parameter over a nullable one
whenever a sensible default (including a sentinel like `""`) exists, keeping both the caller
(who can omit the argument) and the body (which never sees null) happy.

```dang
greet(name: String! = "world"): String! { "hi " + name }
greet                      # "hi world"  (omitted)
greet(null)                # "hi world"  (explicit null falls back)
greet(someNullableString)  # falls back to "world" when the value is null
```

### Function references: `&fn`

- the `&` prefix operator (see [#operators]) yields the function itself without calling it
- `&greet` — captures a zero-arity function/method without auto-calling it; it stays live and re-reads its closure each call
- needed for assignment to a function-typed field, passing as an arg, etc.
- combined with `.method` selection: `&user.greet`
- a captured ref must still satisfy the target's block-parameter signature

### Nested functions

- functions declared inside method bodies can capture enclosing scope
- captured `self` works — nested function still acts as a method on the receiver

> Meta: link forward to [#blocks] — block arguments are the more common form of "pass code." Function refs are for the cases where you need a true callable to store or rebind.

## Forward references

tl;dr: they work.

`.dang` files within a directory share a common scope, like in Go
* field declarations may forward-reference fields later in the same file
* field declarations may cross-reference fields in sibling files
* types may forward-reference types declared later in the file
* forward reads hidden behind function calls / computed defaults resolve via lazy module slots
* a *direct* initializer cycle (`a = b`, `b = a`) is rejected statically: `circular module variable initializer: a -> b -> a`
* a cycle hidden behind an auto-called function or constructor default is caught at runtime when the variable is forced: `initialization cycle while evaluating variable "..."`
