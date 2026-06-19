\use-plugin{dang}
\literate-fences

# Mutation and copy-on-write {#mutation}

> Meta: this is the page that prevents a *lot* of confusion. The thesis: methods look mutating but return a forked copy. Show the canonical `Foo(42).incr.a == 43` example up front.

Dang code is written as if values mutate, and runs as if they don't. A method
body assigns to fields, pushes onto lists, bumps counters — all the moves of
imperative code — but the value it started from never changes. What the method
hands back is a modified *copy*. That's the whole bargain: mutable syntax,
immutable semantics.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser.

## The shape of it

Here is the example to keep in your head. `incr` reads like it bumps a field;
read it that way:

```dang
type Counter {
  value: Int!

  incr: Counter! {
    value += 1
    self
  }

  add(n: Int!): Counter! {
    value += n
    self
  }
}

Counter(42).incr.value
```

`incr` increments `value` and returns `self`, so `Counter(42).incr` is a
`Counter` whose `value` is `43`. Nothing surprising yet — the surprise is what
*didn't* happen. Bind a counter to a name, call `incr` on it, and the original
is exactly where you left it:

```dang
let start = Counter(0)
let bumped = start.incr

[start.value, bumped.value]
```

`bumped` is a fresh `Counter`, forked off `start` with the increment applied;
`start` never moved. The `value += 1` inside `incr` didn't write through to the
counter you were holding — it forked a copy and wrote *that*.

## Each call forks from its receiver

Because every call forks, calling the same method twice on the same value
doesn't compound — both calls branch off the same starting point:

```dang
let base = Counter(0)

[base.incr.value, base.incr.value, base.value]
```

Two `1`s and a `0`: each `base.incr` forked independently from `base`, and
`base` itself stayed put. That's what makes a value safe to reuse as a
fixed point — you can branch off it as many times as you like without any
branch disturbing the next.

A *chain* is the other face of the same rule. Each step forks from its own
receiver, and in `x.incr.incr` the receiver of the second `incr` is the result
of the first — so a chain does accumulate:

```dang
Counter(10).incr.incr.add(5).value
```

Ten, plus one, plus one, plus five: `17`. Nothing here contradicts
fork-per-call; the receivers are simply different values.

## Writing a field forks `self`

Inside a method, assigning to a field is what triggers the fork. `value += 1`,
with no `self.` prefix, forks the current receiver and writes the field on the
copy; any later reference to `self` in the same method sees that copy. The
explicit form `self.value += 1` does exactly the same thing — the prefix is
only there to disambiguate when a local or argument shadows the field name (see
[#name-resolution]).

Those forks compose. A loop that writes a field on every pass builds the
result up one copy at a time, then returns the last:

```dang
type Builder {
  items: [String!]!

  addAll(sources: [String!]!): Builder! {
    sources.each { item => self.items += [item] }
    self
  }
}

Builder([]).addAll(["a", "b", "c"]).items
```

The `.each` block runs three times, each appending to `self.items`. A field
write always forks, so this isn't one value mutated in place: every `+=` reads
the current copy's `items`, builds the longer list, and binds it on a *fresh*
copy, with `self` rebound to follow. Three passes leave a chain of three
generations, each reaching back through the one before it, and the method hands
back the last — `["a", "b", "c"]`. (The return type is the concrete `Builder!`,
not a `Self`: Dang has no `Self` keyword, only the lowercase `self` value.)

## Deep paths copy the whole spine {#deep-paths}

> Meta: a diagram (boxes-and-arrows) would help a lot here. Even ASCII would do.

Assignment reaches through nested fields, and the copy goes as deep as the
write. Take a value three levels deep, bind a second name to the same value,
and write the innermost field through the second name:

```dang
let tree = {{ a: {{ b: {{ c: 1 }} }} }}
let twig = tree
twig.a.b.c = 2

[tree.a.b.c, twig.a.b.c]
```

`tree` still reads `1`. The assignment `twig.a.b.c = 2` doesn't reach into
structure shared with `tree` — it clones every link from the root down to the
leaf it writes, and only the clone carries the new value:

```
tree              twig
 └ a               └ a       ← fresh copy
    └ b               └ b    ← fresh copy
       └ c = 1           └ c = 2
```

Compound assignment (`twig.a.b.c += 10`) behaves the same, and so does the
method-body form: a line like `self.config.contents.packages += [...]` copies
each link along that path. It's fully supported — and it's the one corner of
copy-on-write with a real cost, since the work scales with the depth of the
path. Prefer shallow structures where you have the choice.

## What forks and what doesn't {#name-resolution}

Whether a write forks anything comes down to what the name on the left
resolves to:

- A **local or argument** in scope — a `let` binding, a constructor parameter
  — is a plain mutable slot. `x = value` rebinds it in place; nothing forks,
  because no receiver is involved.
- A **field** of the current receiver — `x = value` where `x` is a field and
  no local shadows it — forks `self` and writes the field on the copy.
- `self.x = value` is the field case spelled out. You only *need* it when a
  same-named local or argument would otherwise win the name lookup.

That last case shows up most in a constructor, where a parameter and the field
it initializes often share a name:

```dang
type Greeting {
  text: String! = "hi"
  shout: String!

  new(text: String!) {
    text = text.toUpper
    self.shout = text + "!"
    self
  }
}

let g = Greeting("hello")
[g.text, g.shout]
```

`text = text.toUpper` rebinds the *parameter* — it shadows the field, so the
bare name is the local. The field `text` is never assigned, so it keeps its
default `"hi"`; only `self.shout` writes a field. Mutating an argument, in
other words, doesn't quietly write the field that happens to share its name.

## When copy-on-write isn't in play

Forking is a property of `self` — of methods on an object. Plenty of code never
touches it:

```dang
let n = 0
n += 5

n
```

A top-level binding is just a variable. `n += 5` rebinds `n`; there's no
receiver to fork and nothing to copy. The same holds inside [#blocks] and in
pure functions that take no `self`. And underneath all of it the values are
immutable anyway — `n += 5` doesn't alter the `0`, it binds `n` to a new
number.

See [#objects] for the `type`, `self`, and constructor mechanics this page
leans on.
