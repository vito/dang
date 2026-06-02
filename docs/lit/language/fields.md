\use-plugin{dang}

# Fields: `pub` and `let` {#fields}

> Meta: this is also the right place to introduce the *field* concept, since `pub` and `let` declare fields whether they hold a value or a function. The object-context behavior is covered in [objects](./objects.md) — link there, don't duplicate.

The `pub` and `let` keywords declare fields in the current scope:

* Top-level fields, declared across `.dang` files in the same directory
* Type-level fields declared within an [#objects] or [#interfaces-unions]
* Block-level fields declared within the nearest enclosing `{` and `}`

These keywords distinguish the expression from [#mutation], which updates an
already-declared field.

## Two visibilities

- `pub name = value` — exported; visible to importers and outside the type
- `let name = value` — private to the file/type

## Fields

- a field is a named, typed thing — value, function, or computed expression
- fields can carry an explicit type annotation
- fields without a value act as required parameters (in objects) or unresolved declarations

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
* field declarations may forward-reference fields later the same file
* field declarations may cross-reference fields in sibling files
* circular field assignments fail typechecking

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

- `name = newValue` mutates an existing field
- `+=` for compound update
- type must remain assignable to the field's declared type
- inside a `type`, bare `name = ...` rebinds a local/arg (a value *binding*, not a field); **field** mutation requires `self.name = ...` (see [mutation](./mutation.md))
