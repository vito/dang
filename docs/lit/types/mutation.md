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

```dang
# every `{{ }}` evaluates its fields concurrently, each in its own fork:
# `b` can't see `a`'s write, and the outer `counter` stays untouched
let counter = 0
let pair = {{
  a: { counter += 1; counter },
  b: { counter += 10; counter }
}}
[pair.a, pair.b, counter]
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
