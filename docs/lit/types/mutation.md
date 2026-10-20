\use-plugin{dang}
\literate-fences

# Mutation and copy-on-write {#mutation}

```dang
type Counter {
  value: Int! = 0
  incr: Counter! {
    value += 1   # bare field write forks the receiver; `self.value += 1` is the equivalent explicit form
    self
  }
  # a method can recurse: the naked `incrBy` call forks self just like `self.incrBy` would
  incrBy(n: Int!): Counter! { if (n <= 0) { self } else { incr.incrBy(n - 1) } }
}
```

```dang
# a "mutating" method returns a fork — the original is untouched
let answer = Counter(42)
[answer.incr.value, answer.value]
```

```dang
# recursion forks per call too: it accumulates into the returned fork and
# leaves the original untouched
let start = Counter(0)
[start.incrBy(3).value, start.value]
```

```dang
type Image {
  packages: [String!]! = []

  # chainable methods return the concrete type name — there is no `Self` keyword
  with(pkg: String!): Image! {
    packages += [pkg]
    self
  }

  withAll(pkgs: [String!]!): Image! {
    pkgs.each { p => packages += [p] }   # bare field write works inside a closure too
    self
  }
}
```

```dang
# each call forks from the receiver, so forks of one base never compound,
# and `base` itself is never modified
let base = Image
[base.with("git").packages, base.with("curl").packages, base.packages]
```

```dang
# within a single call, writes accumulate onto one fork
Image.withAll(["git", "curl", "tini"]).packages
```

```dang
# pure functions have no `self`, so nothing forks — they just compute
double(x: Int!): Int! { x * 2 }
double(21)
```

```dang
# plain bindings are not copy-on-write: a bare write to a local just rebinds it
let n = 1
n = 2
n
```

```dang
type Greeting {
  text: String!
  new(text: String!) {
    # `self.` is needed only to disambiguate from a same-named arg/local;
    # bare `text = ...` would rebind the arg, not the field
    self.text = "hello, " + text
    self
  }
}
```

```dang
Greeting("world").text
```

```dang
type Leaf { c: Int! }
type Node { leaf: Leaf! }
type Tree {
  node: Node!
  # assignment through a path forks each step; subtrees off the path are shared
  bump: Tree! { self.node.leaf.c += 100; self }
}
```

```dang
# the original is untouched at any depth
let tree = Tree(Node(Leaf(42)))
[tree.bump.node.leaf.c, tree.node.leaf.c]
```

```dang
# a loop threads one shared accumulator: writes are visible to the next
# iteration and outlive the loop
let total = 0
[1, 2, 3].each { x => total += x }
total
```

Copy-on-write is Dang's only kind of mutation: a write changes the writer's
own copy of `self`, or a local of the scope it runs in, never a value someone
else holds. Three compile-time rules close the loopholes around that.

## Top-level bindings are immutable {#top-level-bindings}

A top-level binding is immutable once initialized. Top-level script code
(top-level forms and the blocks inside them, like the loop above) may still
reassign it: that reassignment is part of initializing the package, and it
keeps one-off scripts and the REPL working.

Nothing else can write one. Functions, methods, `new()`, type bodies, and the
closures inside them are all rejected, path writes such as `shared.x = …`
included:

```dang-failure
count: Int! = 0

bump: Int! {
  count += 1
  count
}
```

So a function or method has no side effects beyond its own copy of `self`.
To change something, return the new value, as `incr` does on `Counter` above.

Static members (the `self { }` block of a type, see [#static-members]) are
immutable once initialized too, and from everywhere, top-level script code
included. A static method's writes to its own `self` stay in that call's copy,
just like an instance method's:

```dang-failure
type Registry {
  self { size: Int! = 0 }
}

Registry.size = 5
```

## Closures that write can't escape {#impure-closures}

A block or nested function can write the bindings of the scope it was written
in: the loop above writes `total`, and a nested function in a method can write
the method's locals or `self`. Such a closure (or a `&ref` to one) is
*impure*. It may only be called or passed as an argument. Storing it in a
`let` or field, returning it, or putting it in a list or record is a compile
error: whoever ended up holding it could change that scope's state from
anywhere, a shared mutable cell of exactly the kind copy-on-write rules out.

```dang-failure
counter: Int! {
  let n = 0
  bump(x: Int!): Int! { n += x; n }
  bump(1)             # calling it is fine
  let saved = &bump   # storing it is not
  n
}
```

A closure that only reads its scope is pure and can go anywhere:

```dang
scaled: [Int!]! {
  let k = 3
  scale(x: Int!): Int! { x * k }
  let f = &scale
  [f(1), f(2)]
}
scaled
```

The same goes for block parameters. A function that stores, returns, or
collects its block parameter (`self.saved = cb`) can only be given a pure
block:

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

The check happens where the block is passed:

```dang-failure
type Guest {
  n: Int! = 10
  escape: Host! {
    Host.store { x =>
      n += x
      n
    }
  }
}
```

### Field defaults only see values {#field-defaults}

A type's field defaults run once, while the object is still being built:
before `new()` assigns its fields, and before any later write makes a new
copy. Without an explicit `new()` they are the constructor's parameter
defaults (`combined: String! = prefix + suffix` reads whatever `prefix` the
caller passed), and they are scoped like them: a default sees the other
fields' values and the enclosing scope, but not `self`, and not the type's
methods or computed fields, which belong to the finished object. That holds
anywhere in the default, blocks and nested functions included:

```dang-failure
type Gauge {
  level: Int! = 1
  read(x: Int!): Int! { level + x }
  let reader = &read
}
```

With an explicit `new()`, a field without a default has no value until
`new()` assigns it, after the defaults have run, so a default can't read it
either:

```dang-failure
type Dial {
  level: Int!
  let doubled = level * 2
  new(level: Int!) {
    self.level = level
    self
  }
}
```

And like a parameter default, a field default can't assign other fields,
and neither can anything else in the type body outside methods, computed
fields, and `new()`: a bare `level = 5` would override the field's default and
any constructor argument after other defaults had already read it. Give the
field a different default, or set it in `new()`:

```dang-failure
type Knob {
  level: Int! = 1
  let doubled = level * 2
  level = 5
}
```

`new()`'s own parameter defaults follow the same rule. They run after the
field defaults and before `new()`'s body, so they read the fields as the
defaults leave them, and the earlier parameters:

```dang
type Dimmer {
  level: Int! = 1
  out: Int!
  new(k: Int! = level, j: Int! = k * 10) {
    self.out = k + j
    self
  }
}

[Dimmer.out, Dimmer(2).out]
```

So whatever a default reads is a value the object is built with, and a
closure created there is like any closure that only reads its scope: it can
go anywhere, and neither `new()` nor any later write reaches it. To work with
the finished object instead, use a method or computed field, which runs
against the current receiver, or `new()`:

```dang
type Meter {
  level: Int! = 1
  read(x: Int!): Int! { level + x }
  let held = Host.store { x => level + x }        # level as the object is built
  reader: Host! { Host.store { x => read(x) } }   # level as it is now
}

let m = Meter
m.level = 10
[m.held.saved(1), m.reader.saved(1)]
```

## `{{ }}` fields can't write outside themselves {#concurrent-fields}

Every `{{ }}` evaluates its fields concurrently, so fields communicate only
by producing values. Inside a field, these are compile errors:

- writing a binding declared outside the field, including the enclosing
  method's locals and `self`. Each field works on a private copy, so the
  write would otherwise be silently discarded.
- calling a closure created outside the field that writes anything
- calling a block parameter whose block isn't pure. This is checked where
  the block is passed.

```dang-failure
let counter = 0
{{
  a: { counter += 1; counter },   # writing `counter` is an error...
  b: counter * 10                 # ...reading it is fine
}}
```

```dang-failure
type Tally {
  run: Int! {
    let n = 0
    bump: Int! { n += 1; n }
    let r = {{ a: bump, b: n }}
    r.a + r.b
  }
}
```

```dang
# method calls that return updated copies are fine: each field forks its
# own receiver, and `ctr` itself is untouched
let ctr = Counter(0)
{{ a: ctr.incr.value, b: ctr.incr.incr.value, orig: ctr.value }}
```

```dang
# state declared inside a field belongs to that field
{{ sum: { let n = 0; [1, 2, 3].each { x => n += x }; n } }}.sum
```

```dang
# a block parameter called from several fields at once...
both(&block(x: Int!): Int!): Int! {
  let r = {{ a: block(1), b: block(2) }}
  r.a + r.b
}

# ...works with a pure block
both { x => x * 10 }
```

```dang-failure
# ...but rejects a block that writes outer state
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
