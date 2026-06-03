\use-plugin{dang}

# Fields: `pub` and `let` {#fields}

> Meta: this is also the right place to introduce the *field* concept, since `pub` and `let` declare fields whether they hold a value or a function. The object-context behavior is covered in [#objects] — link there, don't duplicate.

The `pub` and `let` keywords declare fields in the current scope:

* Top-level fields, declared across `.dang` files in the same directory
* Type-level fields declared within an [#objects] or [#interfaces-unions]
* Block-level fields declared within the nearest enclosing `{` and `}`

These keywords distinguish the expression from [#mutation], which updates an
already-declared field.

## Two visibilities

- `pub name = value` — exported; visible to importers and outside the type
- `let name = value` — private to the file/type

## What a field is

- a field is a named, typed thing — value, function, or computed expression
- fields can carry an explicit type annotation
- fields without a value (`pub name: Type`) act as required constructor parameters (in objects) or unresolved declarations
- a `let` (private) required field with no default still becomes a required constructor parameter; a `let` field *with* a default does not
- private fields with defaults are preferred over outer-scope bindings of the same name inside the type

## Forms

```dang
pub x = 42                # inferred Int!
pub y: Int! = 100         # explicit type
pub maybe: String = null  # nullable
let secret = "shhh"
```

## Forward references

tl;dr: they work.

`.dang` files within a directory share a common scope, like in Go
* field declarations may forward-reference fields later in the same file
* field declarations may cross-reference fields in sibling files
* types may forward-reference types declared later in the file
* forward reads hidden behind function calls / computed defaults resolve via lazy module slots
* a *direct* initializer cycle (`pub a = b`, `pub b = a`) is rejected statically: `circular module variable initializer: a -> b -> a`
* a cycle hidden behind an auto-called function or constructor default is caught at runtime when the variable is forced: `initialization cycle while evaluating variable "..."`

## Docstrings

- a `"""..."""` literal immediately before a declaration attaches as documentation
- works on modules, types, fields, functions, function parameters, directives, directive args

```dang
"""
Greets the named user.
"""
pub greet(name: String!): String! {
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
