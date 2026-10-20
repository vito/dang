\use-plugin{dang}
\literate-fences

# Mutation and copy-on-write {#mutation}

Values in Dang never change. Code can still read as if it mutates
(`n += 1`, `self.items += [x]`), but a write only rebinds a name in the
writer's own scope, to a new value or an updated copy. A value someone else
holds stays as it was. The compiler checks it:

- **Immutable:** writing a field makes an updated copy; nothing changes in
  place.
- **Lexical:** blocks, nested functions and `&refs` use the variables, and the
  `self`, of the place they were written.
- **Checked:** a write can't reach past the scope that owns it.
  - Functions and methods can't write top-level bindings, and nothing can
    write statics. Functions return new values instead.
  - A closure that writes its scope can be called or passed as an argument,
    but not stored or returned.
  - `{{ }}` fields run concurrently, so they can't write outside themselves.
  - A type's defaults run while the object is being built, so they see only
    values: no `self`, no methods.

## Writes copy {#copy-on-write}

A method works on its own copy of `self`. Writing a field, bare
(`value += 1`) or explicit (`self.value += 1`), updates that copy. Return
`self` to hand it back:

```dang
type Counter {
  value: Int! = 0
  incr: Counter! {
    value += 1
    self
  }
}

# each call starts from the receiver, which is never modified
let answer = Counter(42)
[answer.incr.value, answer.incr.incr.value, answer.value]
```

Within one call, writes accumulate in that copy, including writes from blocks
and nested functions in the method:

```dang
type Image {
  packages: [String!]! = []

  # chainable methods return the type by name; there is no `Self` keyword
  with(pkg: String!): Image! {
    packages += [pkg]
    self
  }

  withAll(pkgs: [String!]!): Image! {
    pkgs.each { p => packages += [p] }
    self
  }
}

let base = Image
[base.with("git").packages, base.withAll(["curl", "tini"]).packages, base.packages]
```

A value someone already holds keeps its fields, including another name for
`self`:

```dang
type Snap {
  n: Int! = 0
  both: [Int!]! {
    let before = self
    n += 1
    [before.n, n]
  }
}

Snap.both
```

A write through a path copies each object along it, and shares the rest:

```dang
type Leaf { c: Int! }
type Node { leaf: Leaf! }
type Tree {
  node: Node!
  bump: Tree! { self.node.leaf.c += 100; self }
}

let tree = Tree(Node(Leaf(42)))
[tree.bump.node.leaf.c, tree.node.leaf.c]
```

A plain variable is simply rebound, and a block can rebind the variables of
the scope it's written in:

```dang
let total = 0
[1, 2, 3].each { x => total += x }
total
```

## Names are lexical {#lexical-scope}

A block, a nested function, or a `&ref` to a method runs against the scope it
was written in, with that scope's variables and `self`, no matter who calls
it:

```dang
type Runner {
  n: Int! = 100
  hook(x: Int!): Int! { x }
  run(&cb(x: Int!): Int!): Int! { cb(1) }
  fire(x: Int!): Int! { hook(x) }
}

type Guest {
  n: Int! = 10
  helper(x: Int!): Int! { n + x }

  # Runner calls the block, but `n` is still Guest's
  viaBlock: Int! { Runner.run { x => n + x } }

  # the same for a ref stored on a Runner
  viaRef: Int! {
    let r = Runner
    r.hook = &helper
    r.fire(1)
  }
}

[Guest.viaBlock, Guest.viaRef]
```

They see that scope as it is when they run, not a snapshot: a ref or block
created in a method sees the method's later writes. Within a scope, a name
resolves to its nearest declaration, so when a parameter or local shadows a
field, write the field as `self.name = …`.

## Functions return, they don't write {#top-level-bindings}

A function or method writes only its own locals and its own copy of `self`.
To change anything else, return the new value, as `incr` does above.

So top-level bindings are immutable once initialized. Only top-level script
code, like the loop above, may reassign them, because it is still
initializing the module:

```dang-failure
count: Int! = 0

bump: Int! {
  count += 1
  count
}
```

Statics, the members of a type's `self { }` block (see [#static-members]), are
immutable from everywhere. A static method's writes to its own `self` stay in
its copy, like any method's.

## Closures that write can't escape {#impure-closures}

A block or nested function can write the variables of the scope it was
written in. Such a closure can only run while that scope does: it can be
called or passed as an argument, but storing it, returning it, or putting it
in a list or record is a compile error. A closure that writes nothing outside
itself can go anywhere, and so can named functions and methods, and refs to
them, since they only write their own locals and their own copy of `self`.

```dang-failure
counter: Int! {
  let n = 0
  bump(x: Int!): Int! { n += x; n }
  bump(1)             # calling it is fine
  let saved = &bump   # storing it is not
  n
}
```

A function that keeps its block parameter accepts only a block that writes
nothing outside itself:

```dang
type Host {
  saved(x: Int!): Int! { x }
  store(&cb(x: Int!): Int!): Host! {
    self.saved = cb
    self
  }
}

Host.store { x => x * 2 }.saved(21)
```

```dang-failure
type Visitor {
  n: Int! = 10
  escape: Host! {
    Host.store { x =>
      n += x
      n
    }
  }
}
```

## `{{ }}` fields only produce values {#concurrent-fields}

Every `{{ }}` evaluates its fields concurrently, so a field can't write
anything declared outside it, whether directly, through a closure, or
through a block parameter. That includes the enclosing method's locals and
`self`. Fields communicate by producing values:

```dang-failure
let counter = 0
{{
  a: { counter += 1; counter },   # writing `counter` is an error...
  b: counter * 10                 # ...reading it is fine
}}
```

```dang
# method calls that return updated copies are fine: each field gets its own
# copy, and `ctr` itself is untouched
let ctr = Counter(0)
{{ a: ctr.incr.value, b: ctr.incr.incr.value, orig: ctr.value }}
```

```dang
# state declared inside a field belongs to that field
{{ sum: { let n = 0; [1, 2, 3].each { x => n += x }; n } }}.sum
```

A block parameter called from a field must be given a block that writes
nothing outside itself. That's checked where the block is passed:

```dang
both(&block(x: Int!): Int!): Int! {
  let r = {{ a: block(1), b: block(2) }}
  r.a + r.b
}

both { x => x * 10 }
```

```dang-failure
type Acc {
  total: Int! = 0
  runBoth: Acc! {
    both { x => total += x; total }
    self
  }
}
```

```dang
# fields coordinate by value in any order (a field naming a sibling waits for it)
{{ total: a + b, a: 1, b: 2 }}.total
```

```dang-failure
# a genuine cycle between fields is a compile error
{{ x: y, y: x }}
```

```dang-failure
# concurrent fields fail fast, but the reported error is always the
# lowest in source order — never whichever lost the race
{{ a: raise "first", b: raise "second" }}
```

```dang-failure
# deterministic even when the lower field finishes last:
# `slow` (field 0) surfaces though `fast` raises first
{{
  slow: { [1, 2, 3, 4].each { _ => 0 }; raise "from slow" },
  fast: raise "from fast"
}}
```

```dang
# a selection over a list fans out across its elements (also concurrent);
# the result is a list of records, compared by value
type User { name: String! }
[User("Alice"), User("Bob")].{{ name }} == [{{ name: "Alice" }}, {{ name: "Bob" }}]
```

```dang-static
# over a GraphQL receiver the same `{{ }}` concurrency is batched I/O:
# a multi-field selection is one round trip; a record of selections runs its
# queries in parallel
{{ users: users.{{ name }}, repos: repos.{{ name }} }}
```

## Defaults see only values {#field-defaults}

A type's field defaults and its `new()`'s parameter defaults are the same
kind of code. Without an explicit `new()`, the field defaults *are* the
constructor's parameter defaults. Both run while the object is being built,
so both follow one rule: a default sees the values that exist when it runs
(the other fields, earlier parameters, and the enclosing scope), and nothing
that needs the finished object. That rules out `self` and the type's methods
and computed fields, anywhere in the default:

```dang-failure
type Gauge {
  level: Int! = 1
  read(x: Int!): Int! { level + x }
  let reader = &read
}
```

In order:

1. Field defaults run first, and may name each other in any order. With an
   explicit `new()`, a field without a default has no value yet, so a
   default can't read it.
2. `new()`'s parameter defaults run next, and see the fields as the defaults
   left them, plus the earlier parameters.
3. `new()`'s body runs last, with `self`.

```dang
type Dimmer {
  level: Int! = 1
  scale: Int! = level * 10
  out: Int!
  new(k: Int! = scale, j: Int! = k + 1) {
    self.out = k + j
    self
  }
}

[Dimmer.out, Dimmer(2).out]
```

The type body can't reassign the type's fields either. Give the field a
different default, or set it in `new()`:

```dang-failure
type Knob {
  level: Int! = 1
  let doubled = level * 2
  level = 5
}
```

So a closure created in a default captures values, like any closure that only
reads, and can go anywhere. To work with the finished object, use a method or
computed field instead:

```dang
type Meter {
  level: Int! = 1
  read(x: Int!): Int! { level + x }
  let held = Host.store { x => level + x }        # level as the object was built
  reader: Host! { Host.store { x => read(x) } }   # level as it is now
}

let m = Meter
m.level = 10
[m.held.saved(1), m.reader.saved(1)]
```

Method argument defaults aren't construction code: they run when the method
is called, against its receiver. Statics aren't either: they're evaluated
once and may use `self` and each other.
