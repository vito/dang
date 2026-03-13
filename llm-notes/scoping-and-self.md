# Scoping, Self, and Copy-on-Write Semantics

## Variable Declarations

### `pub` — Public Slot

Declares a value visible outside its enclosing type or module:

```dang
pub x = 42
pub name: String! = "hello"
```

### `let` — Private Slot

Declares a value only visible within its enclosing scope:

```dang
let secret = "hidden"
```

Inside a type, `let` fields are accessible to the type's own methods but
not to external code:

```dang
type Counter {
  let count = 0         # private

  pub getCount: Int! {  # public
    count               # can read private fields
  }
}

Counter.count     # error: private field
Counter.getCount  # ok: 0
```

## Lexical Scoping

Variables resolve outward through the scope chain. Blocks inherit the
enclosing scope:

```dang
pub x = 100
{
  x = 200         # updates the outer x (no local declaration)
}
assert { x == 200 }
```

A `let` inside a block shadows the outer variable:

```dang
pub x = 100
{
  let x = 999     # shadows outer x
  x = 1000        # updates the local shadow
}
assert { x == 100 }  # outer unchanged
```

This extends to nested blocks — reassignment walks outward until it finds
the declaration, but stops at `Fork` boundaries (see below).

## Self and Dynamic Scope

### What `self` Is

`self` is a **dynamically scoped** reference to the current receiver
object. It is available inside type methods and `new()` constructors.
Unlike lexical variables, `self` doesn't resolve through the scope chain —
it resolves through the **dynamic scope**, a separate slot on the
environment that tracks the current receiver.

```dang
type Greeter {
  pub name: String!

  pub greet: String! {
    "Hello, " + self.name   # self = the Greeter instance
  }
}
```

### Reading Fields Without `self`

Inside a method, bare field names resolve against the receiver via the
lexical scope chain (the method body's environment includes the receiver's
fields). So `name` and `self.name` are equivalent for **reads** when there
is no shadowing:

```dang
type Greeter {
  pub name: String!

  pub greet: String! {
    "Hello, " + name    # equivalent to self.name for reads
  }
}
```

### Why `self` Matters for Writes

Reassignment of a field requires either `self.field = value` or a bare
`field = value` (which writes through to the receiver). The distinction
matters when a parameter shadows a field name:

```dang
type Point {
  pub x: Int!

  pub withX(x: Int!): Point! {
    self.x = x     # self.x is the field, x is the parameter
    self
  }
}
```

Without `self`, `x = x` would be a no-op (reassigning the parameter to
itself).

## Copy-on-Write Field Assignment

### The Core Mechanism

When you write `obj.field = value`, Dang does not mutate `obj` in place.
Instead it:

1. **Clones** the root object
2. Sets the field on the clone
3. **Reassigns** the variable (or dynamic scope) to point to the clone

The original is never modified:

```dang
let a = {{ x: 1 }}
let b = a
b.x = 2
assert { a.x == 1 }   # original unchanged
assert { b.x == 2 }   # b is a new copy
```

### Nested Field Assignment

For deep paths like `obj.a.b.c = value`, every object along the path is
cloned:

1. Clone `obj` → `obj'`
2. Clone `obj'.a` → `a'`, set `obj'.a = a'`
3. Clone `a'.b` → `b'`, set `a'.b = b'`
4. Set `b'.c = value`
5. Reassign `obj` to `obj'`

```dang
let data = {{ a: {{ b: {{ c: 42 }} }} }}
data.a.b.c = 100
assert { data.a.b.c == 100 }
```

This is a full structural clone along the path. Sibling fields that aren't
on the path are shared (not deep-copied).

### `self.field = value` in Methods

When the root is `self`, the cloned object replaces the **dynamic scope**
rather than a lexical variable. This is how methods "mutate" the receiver:

```dang
type Counter {
  pub value: Int!

  pub incr: Counter! {
    self.value += 1   # clones self, updates clone, sets dynamic scope
    self              # returns the (now-updated) dynamic scope
  }
}
```

The method must return `self` to pass the modified copy back to the caller.

## Method Call Isolation

Each method call operates on an **isolated copy** of the receiver. The
caller's reference is never affected:

```dang
let c = Counter(0)
let d = c.incr
assert { c.value == 0 }   # original unchanged
assert { d.value == 1 }   # d is the returned copy
```

Internally, `BoundMethod.Call` does:

1. `recv = Receiver.Fork()` — creates a fork of the receiver
2. Creates a fresh dynamic scope cell via `NewDynamicScope(recv)`
3. The method's mutations go into the fork; the original is untouched

The **fresh cell** is key. It means the method has its own independent
`self` that doesn't leak back to the caller. This is what makes
copy-on-write work.

### Chaining

Because each method returns its modified `self`, chaining works naturally:

```dang
let result = Counter(0).incr.incr.incr
assert { result.value == 3 }
```

Each `.incr` receives the previous call's return value as its receiver.

### Multiple Calls on the Same Receiver

Calling the same method twice on the same receiver produces two
**independent** results:

```dang
let base = Counter(0)
let a = base.incr
let b = base.incr
assert { a.value == 1 }
assert { b.value == 1 }   # not 2 — each starts from base
```

## Closures and the Shared Dynamic Scope Cell

When a closure (block argument) runs inside a method or constructor, it
needs to see accumulated mutations to `self`. This applies anywhere a
block mutates `self` — `.each`, `.map`, or any user-defined method that
takes a block.

### The Problem Without Sharing

When a block like `{ item => self.items += [item] }` is called repeatedly
(e.g. by `.each`), each call clones the block's captured environment.
Without sharing, each clone gets its own snapshot of `self`, so mutations
in one iteration are invisible to the next.

### How It Works

Clone and Fork **share** the dynamic scope cell (a `*DynamicScope`
pointer). When `self.field = value` updates the dynamic scope via
`SetDynamicScope`, it writes through the shared cell, so the next
iteration's clone sees the updated `self`.

The isolation boundary is `NewDynamicScope`, which creates a **fresh**
cell. This is called when entering a new method (`BoundMethod.Call`) or
constructing a new instance (`ConstructorFunction.Call`). Everything
inside that scope — including closures, blocks, and nested calls to
`.each` — shares the same cell.

### In Methods

```dang
type Builder {
  pub items: [String!]! = []

  pub addAll(source: [String!]!): Builder! {
    source.each { item =>
      self.items += [item]   # updates shared cell each iteration
    }
    self
  }
}

let b = Builder.addAll(["a", "b", "c"])
assert { b.items == ["a", "b", "c"] }

# Copy-on-write still works — original is unchanged
let original = Builder
let modified = original.addAll(["x", "y"])
assert { original.items == [] }
assert { modified.items == ["x", "y"] }
```

### In Constructors

```dang
type Collector {
  pub items: [String!]!

  new(source: [String!]!) {
    self.items = []
    source.each { item =>
      self.items += [item]
    }
    self
  }
}

assert { Collector(["a", "b", "c"]).items == ["a", "b", "c"] }
```

### The Two Operations

| Operation | What it does | When used |
|---|---|---|
| `SetDynamicScope(v)` | Updates the existing shared cell | `self.field = value` (COW assignment) |
| `NewDynamicScope(v)` | Creates a fresh, unshared cell | Entering a method or constructor |

This means:
- Within a single method or constructor body, all closures share the same
  `self` and see each other's mutations.
- Across method calls, each method gets its own `self` that doesn't leak
  back to the caller.

## Bare vs `self.` in Methods

Both styles work for field mutation inside methods:

```dang
type Foo {
  pub a: Int!

  pub incr: Foo! {
    a += 1      # bare — writes through Fork to receiver
    self
  }

  pub incrExplicit: Foo! {
    self.a += 1   # explicit — clones self, updates dynamic scope
    self
  }
}
```

The bare form works because the method body's environment includes the
receiver's fields (via Fork), and `a += 1` reassigns through the fork.
The `self.` form takes the copy-on-write path (clone → modify → set
dynamic scope). Both produce the same result.
