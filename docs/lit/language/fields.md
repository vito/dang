\use-plugin{dang}

# Fields {#fields}

> Meta: this is the right place to introduce the *field* concept. A field is declared by its shape, not by a keyword — lead with that, then visibility, then the declaration-vs-reassignment rule. Frame the keyword as a *binding introducer* (needed only when a line would otherwise read as reassignment), not as a visibility annotation that's "usually optional" — the untyped value field isn't an exception, it's just the one declaration shape that names no type. The tell for "declaration, no keyword needed" is a type in type position (a field annotation or method return type), never args or a block alone — those without a return type are calls. The object-context behavior is covered in [#objects] — link there, don't duplicate.

A **field** is a named, typed thing — a value, a function, or a computed
expression — declared in the current scope:

* Top-level fields, declared across `.dang` files in the same directory
* Type-level fields declared within an [#objects] or [#interfaces-unions]
* Block-level fields declared within the nearest enclosing `{` and `}`

A field is recognized by its **shape**, not by a keyword: a name followed by a
type annotation, a value, an argument list, or a block body.

```dang
name: String!             # typed field
greet: String! { ... }    # computed field / method
add(x: Int!): Int! { x }  # method with args
y: Int! = 100             # typed field with default
```

## Visibility

Fields are **public by default** — just write the declaration. The `let`
keyword marks the non-default case:

- a bare declaration is **public** — visible to importers and outside the type
- `let name = value` is **private/local**. A *type-level* `let` is readable only
  inside that type's own methods/defaults. A *block-level* `let` declares a
  fresh local. A *top-level* `let` is module-scoped: since a directory is one
  module, it is visible from every `.dang` file in that directory, just not
  exported — the way to share private helpers across a directory module (see
  [#modules]).

`pub name = value` is still accepted as an explicit public marker, but it is
redundant with the default. `dang fmt` and the LSP remove it, and it will
eventually be retired — leaving bare declarations for public fields and `let`
for local/private ones.

## Declaration vs. reassignment

A keyword here isn't really a visibility marker — it **introduces a binding**.
You reach for one only when a line would otherwise read as a [#mutation]
(`name = newValue`, which updates an already-declared field). What sets a
declaration apart is a **type in type position** — a field's `name: Type`
annotation, or a method's `): Type` return type:

- anything that names a type is a declaration, so no keyword is needed (public
  by default; `let` to opt out). Arguments or a block are *not* enough on their
  own — `add(x)` or `foo { ... }` without a return type read as **calls**, not
  declarations; the return type is the tell
- a bare, *untyped* `name = value` names no type, so on its own it is plain
  assignment syntax — a **reassignment** of an existing field. To *introduce* a
  new one, give it a keyword (`let x = 42`, or legacy `pub x = 42`) — or annotate
  it (`x: Int! = 42`), which makes it a declaration like the rest

This is the same rule that governs locals: you never bring a local into being
without `let` either. A value field is no different — bare `name = value` always
means "assign to what's already there," so declaring a fresh one always takes an
introducer. Nothing about the untyped form is special-cased; it's simply the one
declaration shape that carries no type, so it's the one that still needs a word.

Rule of thumb: **if it names a type, you never need a keyword.**

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
let secret = "shhh"       # private (untyped is fine for let)
pub count = 0             # introducer required: untyped value field, else reassignment
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
