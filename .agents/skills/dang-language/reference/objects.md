# Objects, Mutation/CoW, Fields, Functions, Blocks, Interfaces, Unions

## Fields: `pub` and `let`

`pub`/`let` declare **fields** in the current scope: top-level (across `.dang` files in a directory), type-level (inside a `type`/`interface`), or block-level (inside the nearest `{ }`). A field is a named, typed thing — value, function, or computed expression.

```dang
pub x = 42                # public, inferred Int!
pub y: Int! = 100         # explicit type
pub maybe: String = null  # nullable
let secret = "shhh"       # private to the file/type
pub z: Int                # declaration without value
```

- `pub` — exported (visible to importers and outside the type). Default visibility for a `type`.
- `let` — private to the file/type.
- A field **without a value** (`pub name: Type`) acts as a required constructor parameter (in objects) or an unresolved declaration. A `let` required field with no default is *also* a required constructor param; a `let` field *with* a default is not.
- Private fields with defaults are preferred over outer-scope bindings of the same name inside the type.

### Forward references (they work)
`.dang` files in a directory share one scope (like Go). Fields may forward-reference fields later in the same file, cross-reference sibling files, and types may forward-reference later types.
- A *direct* initializer cycle (`pub a = b`, `pub b = a`) is rejected statically: `circular module variable initializer: a -> b -> a`.
- A cycle hidden behind an auto-called function/constructor default is caught at runtime: `initialization cycle while evaluating variable "..."`.

### Reassignment vs. declaration
- `name = newValue` mutates an existing field/local/arg. `+=` for compound update.
- Type must stay assignable to the declared type.
- Assigning a function-valued field a bare function name *calls* it; use `&name` to assign the function itself.
- Inside a `type`, bare `name = ...` resolves to the field when nothing shadows it; if a parameter/local shadows the name, **field** mutation requires `self.name = ...`.

## Functions

```dang
pub add(a: Int!, b: Int!): Int! { a + b }
```
- Name, params, return type, body. The **last expression is the result** — no `return` needed for the normal result.
- `return expr` is for *early* exit; unwinds through enclosing blocks/loops; valid in `new(...)` too. `return` outside any function → `return outside of function`.
- No `;` separator; separate forms with newlines or `,`.

### Zero-arity & auto-calling
```dang
pub motd: String! { "hello" }   # omit parens; it's a field with a function body
```
- Callers also omit parens: `motd`, not `motd()`.
- A zero-arity function/method **invokes on reference**, like a property. Same for GraphQL fields with no required args.
- `&name` suppresses invocation.

### Arguments
- Named: `greet(name: "Alice")`. Positional: `greet("Alice")`.
- **Mixed**: positional first, then named. `add(10, b: 20)` ✓; `add(a: 10, 20)` ✗ → `positional arguments must come before named arguments` (same rule for directive applications).
- **Defaults**: `name: String! = "world"`. A default may reference *earlier parameters* in the same list (the param shadows any outer binding). In a free function it may reference enclosing scope; in a method it may reference fields of the same type. A nullable arg passed `null` falls back to its default; a nullable arg with no default stays `null`. Same rules for `new(...)`.

### Function references: `&fn`
- `&` yields the function itself without calling it: `&greet`, `&user.greet`.
- Needed to assign to a function-typed field, pass as an arg, etc. Re-reads its closure each call. A captured ref must still satisfy the target's block-parameter signature.

### Nested functions
- Functions declared inside method bodies capture enclosing scope, including `self` (the nested function still acts as a method on the receiver).

## Blocks (the "lambda of Dang")

```dang
{ x => x + 1 }            # one param
{ item, index => ... }    # multiple params, comma-separated, before =>
{ 42 }                    # no =>; a block expression evaluating to its last form
```
- Body is a form sequence (newline/`,` separated); last form is the result.
- Blocks are the iteration protocol, the lambda-equivalent, AND the body of conditionals/loops.

### Block arguments to functions
A block parameter is declared with the `&` sigil; its type is a function type:
```dang
pub twice(&body: Int!): Int! { body + body }          # zero-arg block returning Int!
pub myFun(&block(x: Int!): String!): String! { block(42) }   # block taking args
pub do(&yield: b): b { yield * 2 }                    # arg type can be a type variable
```
- At most **one** block parameter per function/constructor; it must come **last**.
- Callers pass a trailing brace block:
```dang
twice { 21 }                  # ⇒ 42
list.map { x => x * 2 }
withArg("Number: ") { x => toJSON(x) }   # args then block
list.each { item, index => ... }
```
- A block whose body ignores its params can omit `param =>`: `[1,2,3].map { "whee" }`, `numbers.filter { true }`.

### Scoping
- A block is a lexical scope; `let` declares a fresh local that shadows an outer field; mutating the local leaves the outer untouched.
- Reassignment **without** a shadowing `let` mutates the enclosing field — across nested blocks too. `+=` works on the outer field from inside a block.
- Hoisting: a mutation inside a `for` loop is visible after the loop in the same scope.
- Closures inside a method/constructor share `self` across iterations, so `source.each { item => self.items += [item] }` accumulates.

### Control-flow handoff
- `return` inside a block unwinds the enclosing **function**, not just the block.
- `break value` / `continue value` work inside `.each`, `.map`, `for`, and user-defined block-arg calls. `break value` becomes the loop/call result; bare `break` yields `null`. `continue value` flows into `.map`'s result (bare → `null`, e.g. `[null]`); in `.each`/`for` it just advances.
- `break`/`continue` target the **innermost** loop/block-call. An ordinary nested function declared inside a block does NOT inherit the break/continue target → `... outside of loop or block-taking call`.
- Escaped blocks (stored via `&block`, called after the receiving call returned) error at runtime: `break from expired block call` / `return from expired function`.

## Objects (`type`)

A `type` declares both a **type** and its **prototype constructor**.

```dang
type Person {
  pub name: String!
  pub age: Int! = 0
  pub greet: String! { "hi, I'm " + name }   # zero-arg method / computed field
}
```
- Members are fields or methods, indistinguishable in syntax (`pub`/`let` + name + optional `: Type`, `= default`, or `{ body }`).
- `pub` is readable outside the type (default); `let` is readable only inside the type's own methods/defaults.

### Constructor parameters
Whether a member is a constructor param depends on having **NO default**, not on visibility:
- `pub x: T!` (no default) → required positional param
- `pub x: T! = d` / `pub x = d` → optional param (default `d`)
- `let x: T!` (no default) → required positional param too
- `let x: T! = d` → NOT a param; the default is used
- members with a `{ body }` are never constructor params

### Implicit constructor
- One positional param per non-default field, in **declaration order** (not required-first). A defaulted field may precede a required one; positional args still line up by declaration order.
- Also callable with named args. Field defaults evaluate with `self` bound, so a default may reference earlier/sibling fields (`combined = prefix + "_" + suffix`).

```dang
Person("Alice", age: 30)
Person(name: "Alice")
```

### Zero-arg auto-construction
- A type whose constructor needs nothing constructs on bare reference: `let p = Person` ≡ `Person()`.
- Exception: a constructor requiring a **block argument** is NOT auto-called by a bare reference (`pub loop: Loop! = Loop` is an error).

### Explicit constructor: `new`
```dang
type Greeter {
  pub greeting: String!
  new(name: String!) {
    self.greeting = "hello, " + name
    self
  }
}
```
- `new(args) { body }` or `new { body }` (no parens when no args). No `pub`, no return-type annotation (both errors: `'new' is a constructor, not a method`). Only valid inside a `type` body.
- Overrides the implicit constructor (fields no longer auto-become params; `new`'s arg list defines the signature).
- Constructor args are *local* bindings, distinct from fields even when same-named: `foo = foo + 10` rebinds the arg, does NOT touch `self.foo`; shadows same-named fields; NOT visible in method bodies (only in `new`).
- Must return the constructed type (`self`, or a method chain returning it) — last expression must be `Foo!`. Returning another type → `new() must return Wrong!, got String!`; returning `null` errors.
- May chain other methods (propagating their forked `self`); self-field mutation inside a loop accumulates into one fork. Can accept block args: `new(&condition: Boolean!) { ... }`.

### `self`
- Bound during constructor, method, field-default, and computed-field execution.
- Bare names resolve against the receiver first: bare `name` reads `self.name`; bare `incrBy(1)` calls `self.incrBy(1)`.
- Field **reads** never need `self.`. For **assignment**: bare `a += 1` / `value = v` forks `self` and sets the field; `self.field = ...` is the explicit form, required only to disambiguate from a same-named local/arg.
- `self` is the value returned by chainable methods.

### Computed fields
```dang
pub fullName: String! { firstName + " " + lastName }
```
- A member with a type and a body but no arg list — a zero-arg function evaluated on `self` each access (no call parens). Recomputes against the current receiver.
- A defaulted-value member (`pub x = config.name + "_computed"`) is computed once at construction; a `{ body }` computed field is re-evaluated per access.

## Mutation and copy-on-write

**Values are immutable.** "Mutation" inside a method creates a **forked copy** of the receiver.

```dang
type Foo {
  pub a: Int!
  pub incr: Foo! { a += 1; self }
}
Foo(42).incr.a == 43          # original Foo(42) untouched
```

- `self.field = value` forks the receiver, substitutes the field, and subsequent `self` references see the fork. The forked instance is what the method returns (typically `self`). Methods that mutate `self` **must return `self`** to surface the updated copy.
- **Fork-per-call**: two calls on the same receiver don't compound — each forks from the original, not the previous result:
  ```dang
  let c1 = Counter(0)
  let c2 = c1.incr     # 1
  let c3 = c1.incr     # 1, and c1.value still 0
  ```
- **Within a method, mutations accumulate inside one fork**: `source.each { item => self.items += [item] }; self` builds up a single forked value. Return type is the concrete type name (`Builder!`) — there is no `Self` keyword.
- **Nested field assignment** `self.a.b.c = x` clones every link on the path, leaving the original tree untouched. Compound forms (`data.a.b.c += 10`) work. Supported but expensive — avoid deep nesting.
- **Bare reassignment vs. field mutation**: name resolution at the write site decides the target. A local/arg in scope → `x = value` rebinds it, no fork. A field (not shadowed) → bare `x = value` forks `self`. `self.x = value` always forks and sets the field.
- No CoW for pure functions (no `self`) or top-level bindings (no `self` to fork). Chaining is the natural pattern: `Counter(0).incr.incr.incr.value == 3`.

## Interfaces and unions

Map 1:1 to their GraphQL counterparts. The interface/union type itself is also a runtime value (`Named != null`). Both discriminate with `case` (see control-flow.md) and, for GraphQL values, with inline fragments (see graphql.md).

### Interfaces
```dang
interface Named { pub name: String! }
type Person implements Named { pub name: String!; pub age: Int! }
type Book implements Named & Serializable { ... }   # implements A & B for multiple
```
- A type implementing an interface must provide all interface fields. A method field also satisfies an interface field.
- Missing field → `object X is missing \`f(): T\`, required by interface I`. Incompatible type → `field "f": type ... is not compatible with interface type ...`.
- Interface inheritance: `interface User implements Named { pub email: String! }` — the child must re-declare (cover) every parent field with compatible types. A child-interface value widens to the parent (and lists: `[User!]!` → `[Named!]!`).

### Variance (interface implementation)
- **Return types** covariant (may be more specific): interface `getData: String` (nullable) can be implemented as `getData: String!`. Weakening is rejected: `return type String is not compatible with interface return type String! (covariance required)`.
- **Argument types** contravariant (may be more general): accept nullable where the interface wants non-null. Extra *optional* args allowed.
- **List elements** covariant in nullability and type: `[String!]` satisfies `[String]`; `[Dog!]` satisfies `[Animal!]`.
- **Scalar fields** invariant: `id: String!` does not satisfy `id: ID!`.

### Unions
```dang
union Pet = Cat | Dog
```
- Members must be **object types** (no scalars/interfaces/enums) → `union member X must be an object type, got enum`. Flat unions only.
- Members must exist; only members may be matched in `case` (`type X is not a member of union Pet`).
- NOT statically exhaustive — a `case` missing members type-checks fine; an unmatched value is a *runtime* error `no case clause matched the value`. Add `else => ...`.

### Comparison
| | what it is | members | discriminate with |
|---|---|---|---|
| interface | shared field contract | types that *implement* it | `case` type patterns, inline fragments |
| union | closed set of object types | object types only, listed explicitly | `case` type patterns, inline fragments |
| enum | closed set of named constants | bare identifiers (`RED`) | `case` value patterns |
