\use-plugin{dang}

# Fields {#fields}

> Meta: introduce the *field* concept here. Lead with the shape and let the examples carry it — the whole rule is just: `let` makes a field local, a type makes it public, and a bare `name = value` reassigns. Don't mention `pub` (it's legacy and formatted away). Object-context behavior lives in [#objects] — link, don't duplicate.

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
let secret = "shhh"       # local field
```

## Visibility

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

## Declaration vs. reassignment

A bare `name = value` reassigns an existing field — see [#mutation]. To declare a
new field instead, give it a type or introduce it with `let`:

```dang
total: Int! = 0   # declares a public field
let total = 0     # declares a local field
total = 5         # reassigns the existing field
```

## What a field is

- a field is a named, typed thing — value, function, or computed expression
- fields can carry an explicit type annotation
- fields without a value (`name: Type`) act as required constructor parameters
  (in objects) or unresolved declarations
- a `let` (private) required field with no default still becomes a required
  constructor parameter; a `let` field *with* a default does not
- private fields with defaults are preferred over outer-scope bindings of the
  same name inside the type

## Forms

```dang
x: Int! = 42              # explicit type with default
y: Int! = 100             # explicit type
maybe: String = null      # nullable
let secret = "shhh"       # local field (untyped is fine)
```

## Forward references

tl;dr: they work.

`.dang` files within a directory share a common scope, like in Go
* field declarations may forward-reference fields later in the same file
* field declarations may cross-reference fields in sibling files
* types may forward-reference types declared later in the file
* forward reads hidden behind function calls / computed defaults resolve via lazy module slots
* a *direct* initializer cycle (`a = b`, `b = a`) is rejected statically: `circular module variable initializer: a -> b -> a`
* a cycle hidden behind an auto-called function or constructor default is caught at runtime when the variable is forced: `initialization cycle while evaluating variable "..."`

## Docstrings

- a `"""..."""` literal immediately before a declaration attaches as documentation
- works on modules, types, fields, functions, function parameters, directives, directive args

```dang
"""
Greets the named user.
"""
greet(name: String!): String! {
  `hi, ${name}`
}
```

## Reassignment

- `name = newValue` mutates an existing field (or local/arg of that name)
- `+=` for compound update (Int add, String/List concatenation)
- type must remain assignable to the field's declared type
- assigning a function-valued field a bare function name *calls* it; use `&name` to assign the function itself — see [#functions]
- nested-path mutation is copy-on-write: copying an object then writing `m.a.b.c = 2` leaves the original unchanged
- inside a `type`, bare `name = ...` resolves to the field when nothing shadows it; if a parameter (or local) shadows the field name, **field** mutation requires `self.name = ...` — see [#mutation]
