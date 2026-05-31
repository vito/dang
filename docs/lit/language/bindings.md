\use-plugin{dang}

# Bindings: `pub` and `let` {#bindings}

> Meta: this is also the right place to introduce the *slot* concept, since `pub` and `let` declare slots whether they hold a value or a function. The class-context behavior is covered in [classes](./classes.md) — link there, don't duplicate.

## Two visibilities

- `pub name = value` — exported; visible to importers and outside the type
- `let name = value` — private to the file/type

## Slots

- a slot is a named, typed thing — value, function, or computed field
- slots can carry an explicit type annotation
- slots without a value act as required parameters (in classes) or unresolved declarations

## Forms

```dang
pub x = 42                # inferred Int!
pub y: Int! = 100         # explicit type
pub maybe: String = null  # nullable
let secret = "shhh"
```

## Hoisting and declaration order

- declarations are **hoisted** within a file
- mutual references between top-level declarations are fine
- defaults are evaluated lazily on first use

## Forward references

- a `pub` slot's default may reference a later-declared slot
- circular initializer chains are detected at type-check time (see `errors/module_variable_initializer_cycle.dang`)

## Docstrings

- a `"""..."""` literal immediately before a declaration attaches as documentation
- works on modules, types, slots, functions, function parameters, directives, directive args

```dang
"""
Greets the named user.
"""
pub greet(name: String!): String! { "hi, " + name }
```

> Meta: note that docstrings are stored on the AST but introspection isn't fully wired yet — flag as "subject to change."

## Reassignment

- `name = newValue` mutates an existing slot
- `+=` for compound update
- type must remain assignable to the slot's declared type
- inside a `type`, bare `name = ...` rebinds a local/arg; **field** mutation requires `self.name = ...` (see [mutation](./mutation.md))
