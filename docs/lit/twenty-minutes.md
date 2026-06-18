\use-plugin{dang}

# Dang in twenty minutes {#twenty-minutes}

> Meta: a single top-to-bottom tour of the whole language — the replacement for
> the old philosophy page. Let the examples carry it; keep prose to a sentence
> or two per idea, and link out to the chapter that owns each topic rather than
> repeating it. The goal is "you've now seen everything you'll reach for daily."

Dang is a statically typed scripting language whose types and functions come
from a **GraphQL schema**. You point it at a schema — Dagger, GitHub, your own
API — and every type and root field in that schema becomes part of the
language. Inference is Hindley–Milner, so type annotations are usually optional,
and nullability is tracked in the types: `T!` is non-null, `T` may be null.

This page is the whole language in one sitting. Each section links to the
chapter that covers it in depth.

\table-of-contents

## The four ideas everything hangs on

- **Schema-driven types** — `import`ing a schema makes every type and root
  `Query`/`Mutation` field part of the language. Dang's "standard library" is
  whatever schema you connect ([#interop]).
- **Prototype objects** — `type Foo` declares both a type *and* its constructor
  function. Fields and methods are indistinguishable in syntax ([#objects]).
- **Immutability + copy-on-write** — values never change. A method that looks
  like it mutates actually returns a *forked copy* of the receiver
  ([#mutation]).
- **Null tracking** — `String` ≠ `String!` in the type system, with
  flow-sensitive narrowing so you rarely write a cast ([#nullability],
  [#flow-typing]).

Hold those four and the rest is detail.

## Running it

```sh
go install github.com/vito/dang/v2/cmd/dang@latest

dang hello.dang     # run a script
dang ./mymodule     # run every .dang file in a directory as one module
dang                # no path → interactive REPL
dang fmt -w .       # format to canonical style
```

`dang` is one command. There is no `run` or `check` subcommand — the path you
give decides the mode. A language server ships in the same binary (`dang
--lsp`), with editor configs for VS Code, Zed, and Neovim. Full CLI and REPL
reference: [#cli].

## Syntax basics

```dang
# comments start with '#', to end of line — there is no '//'

x: Int! = 42                    # a binding; the type makes it public
let secret = "hidden"           # 'let' keeps it private (and may be untyped)

add(a: Int!, b: Int!): Int! {
  a + b                         # last expression is the result — no 'return'
}
```

- Forms are separated by newlines or `;`. Indentation is conventional, not
  syntactic.
- Names are **public by default**; `let` makes a name local. A *typed*
  declaration is the public surface; `let` is for everything else ([#fields]).
- Declarations are **hoisted** — order within a file (or directory) doesn't
  matter, and forward references just work.
- Identifiers: `lowerCamel` for values and methods, `Capitalized` for types.
- More: [#syntax]. Reserved words, the grammar, and operator precedence live in
  [#syntax], [#operators], and [#grammar].

## Values and literals

```dang
1, -3, 1_000          # Int!  (signed 64-bit, decimal only)
3.14, 1.5e10          # Float!
true, false, null     # Boolean! and the null literal
[1, 2, 3], []         # lists; an empty list needs a type hint: [] :: [Int!]!
{{ name: "Ada", n: 1 }}   # a record — note the DOUBLE braces
```

Records use **double braces** `{{ ... }}` (the single-brace `{ ... }` is a
block — more below). Records are always non-null, and `{{ ... }}` is also a
record *type*: `x :: {{ name: String!, n: Int! }}!`.

Strings come in three flavors:

```dang
"hello\n"                  # double-quoted: single line, with escapes
"""                        # triple-quoted: multi-line, raw, auto-dedented
  raw text, no escapes
"""
`hi ${name}!`              # backtick template: single line, ${...} interpolation
```

Interpolated values are stringified like `toString` (non-strings JSON-encode).
Inside backticks `#` is *not* a comment and the only escape is `\${`. Triple-
and backtick forms also have multi-line fence-growing variants. Full rules:
[#literals]; string *methods* are in [#strings].

## Operators {#20m-operators}

```dang
a + b        # + - * /  on numbers; + also concatenates Strings and lists
a % b        # Int-only modulo
a == b       # == != are type-safe: mismatched types compare false, no coercion
a < b        # < <= > >= on numbers or strings
a and b      # and / or — keywords, short-circuiting, Boolean! only
!ok          # boolean negation
x ?? "fb"    # default: fallback when x is null; result takes the fallback's type
maybe!       # postfix '!': assert non-null (raises at runtime if actually null)
&greet       # prefix '&': a reference to a function, without calling it
x :: URL!    # '::' type hint / cast (see types)
total += 1   # compound assignment (+ only); also on String/list
```

There is **no truthiness**: `and`/`or`/`if` want a real `Boolean!`. `??` and the
postfix `!` are your two null tools — reach for narrowing first (below) and `!`
only when you know better than the checker. Precedence table: [#operators].

## Functions {#20m-functions}

A function is a field with an argument list. The body's last expression is its
value.

```dang
greet(name: String! = "world"): String! {
  `hi, ${name}`
}

greet("Ada")        # "hi, Ada"   — positional
greet(name: "Ada")  # same        — named
greet               # "hi, world" — omitted; the default fills in
```

Four things that surprise newcomers:

- **Zero-arg calls drop their parens.** `motd` below is invoked just by naming
  it — a field and a zero-arg method read identically. Use `&motd` to get the
  function *without* calling it.

  ```dang
  motd: String! { "be excellent" }
  motd          # "be excellent"  — not motd()
  ```

- **No `return` for the normal result** — it's the last expression. `return` is
  for *early* exit only.
- **Positional then named** — `add(10, b: 20)` is fine; named-before-positional
  is an error.
- **Non-null-with-a-default is optional to callers but non-null in the body.**
  `name: String! = "world"` lets a caller omit it (or even pass `null`) while
  the body never sees null — the idiomatic way to excise null at the boundary.

Defaults may reference earlier parameters and (in methods) sibling fields. Full
detail, plus `&fn` references and nested functions: [#functions].

## Blocks {#20m-blocks}

A block is braces around a sequence of forms; the last is its value. Blocks are
Dang's lambda — there's no separate arrow syntax — and they're the iteration
protocol, the body of every conditional and loop, and the hook for Ruby-style
DSLs.

```dang
[1, 2, 3].map { x => x * 2 }          # => [2, 4, 6]
[1, 2, 3].filter { x => x > 1 }       # => [2, 3]
["a", "b"].each { item, i => print(`${i}: ${item}`) }
```

- Parameters go before `=>`, comma-separated.
- A param-less block that mentions `_` gets one implicit parameter named `_`
  (Kotlin's `it`, not positional): `[1, 2, 3].map { _ * 2 }`. Every `_` in a
  block is the *same* argument.
- A function declares a block parameter with `&`; callers pass it as trailing
  braces:

  ```dang
  twice(&body: Int!): Int! { body + body }
  twice { 21 }                         # => 42
  ```

- `receiver.{ block }` is Dang's pipe (`41.{ inc(_) }` ≡ `inc(41)`) — note the
  **single** brace, distinct from `.{{ }}` selection below. Because it's
  application, not navigation, it passes a null receiver straight in, so a block
  can *handle* null.

Full block mechanics, the implicit `_`, and dot-block piping: [#blocks].

## Control flow {#20m-control-flow}

`if`, `case`, and `loop` are **expressions** — there are no control-flow
statements — so each yields the value of whichever branch ran.

```dang
if (ready) "on" else "off"

grade(score: Int!): String! {
  if (score >= 90) "A" else if (score >= 80) "B" else "C"
}
```

Braces aren't part of `if` — a braced branch is just a block. With no `else`,
the result is nullable (the false case is `null`).

`case` matches top-to-bottom, first match wins. It takes value patterns,
*type* patterns (which narrow), or — with no operand — acts as a cond-style
chain:

```dang
case (digit) { 1 => "one"; 2 => "two"; else => "?" }

case (pet) {                       # pet : Cat! | Dog!
  c: Cat => `${c.lives} lives`
  d: Dog => `woof, ${d.name}`
}

case {                             # no operand → each clause is a Boolean!
  temp < 0  => "freezing"
  temp > 30 => "hot"
  else      => "mild"
}
```

Type patterns covering every member of a union are *exhaustive* — no `else`
needed and the result stays non-null. `loop` is the only loop (and it's a
stdlib builtin, not a keyword); there is no `for`/`while`. It repeats until a
`break`, `return`, or `raise`:

```dang
let n = 1
loop { if (n >= 100) { break }; n = n * 2 }
n                                  # => 128
```

`break value` / `continue value` work in `.each`, `.map`, `loop`, and your own
block-taking functions; `return` inside a block unwinds the *enclosing
function*. Full rules: [#control-flow].

## Null narrowing {#20m-null-narrowing}

Because `String ≠ String!`, you check for null once and the checker remembers.
Narrowing applies to bare locals and bare `self`-fields:

```dang
let nickname = user.nickname        # nullable: String
if (nickname == null) {
  "anonymous"
} else {
  nickname.toUpper                  # here nickname is String!
}
```

Diverging guards (`return`, `raise`, `break`, `continue`) narrow the rest of the
scope: `if (x == null) { return "no value" }` leaves `x` as `T!` afterward.
Known gaps to know about: narrowing is intra-procedural, an `and`-guard does
*not* narrow, and a null check on a *field access* (`h.val`) doesn't narrow
later reads — bind to a local first. The full story, including these gaps:
[#flow-typing].

## Errors {#20m-errors}

An error is a value implementing the `Error` interface, and error handling is
expression-shaped. `raise` cuts a computation short; `try`/`catch` yields the
body's value, or a clause's on failure.

```dang
try { raise "out of coffee" } catch { err => "plan B: " + err.message }
```

Raising a `String!` wraps it in `BasicError`. Your own error types
`implements Error` (which requires a `message: String!`) and carry extra fields
along for a `catch` to read. `catch` clauses are *type* patterns over `Error`
implementers; an unmatched error re-raises rather than being swallowed.

The rule of thumb: **return `null` when absence is normal** (a search that comes
up empty); **`raise` when continuing would produce wrong results** or a boundary
is crossed (invalid input, a failed HTTP/GraphQL call). Don't use `raise` for
control flow — that's what `return` is for. Full treatment: [#errors].

## Objects and `type` {#20m-objects}

`type Foo` declares a type *and* its constructor. Members are fields or methods,
written the same way; a typed member is public, `let` keeps it internal to the
type.

```dang
type Person {
  name: String!
  age: Int! = 0
  greet: String! { `hi, I'm ${name}` }   # computed field — no parens, no 'self.'
}

Person("Ada", age: 36)      # positional follows declaration order
Person(name: "Ada")         # or named; defaulted fields are optional
```

- A member **with no default** becomes a required constructor parameter; a
  member with a default (or a `{ body }`) does not. Visibility doesn't change
  that — a `let` field with no default is still a required param.
- A type that needs no arguments constructs on bare reference: `Person` ≡
  `Person()`.
- Inside a method, bare names resolve against the receiver first: `name` reads
  `self.name`, `greet` calls `self.greet`.
- For a custom constructor, define `new(args) { ... self }` — it overrides the
  implicit one.

Computed fields (`{ body }`, no arg list) recompute on each access; a
defaulted-value field (`= expr`) is computed once at construction. More:
[#objects].

## Immutability and copy-on-write {#immutability}

Values never change. A method that assigns to a field actually **forks** the
receiver and returns the copy — so a "mutating" method must end in `self`:

```dang
type Counter {
  value: Int!
  incr: Counter! { value += 1; self }   # forks self, returns the fork
}

let c = Counter(0)
c.incr.incr.value     # => 2   (chaining threads the forks)
c.value               # => 0   (the original is untouched)
```

Each call forks from the *original*, not from the previous result — `c.incr`
twice gives two independent `1`s, never a `2`. Within a single method, though,
mutations accumulate into one fork, so `source.each { x => self.items += [x] }`
builds up as expected. Nested-path writes (`self.a.b.c = x`) clone every link on
the path — supported, but avoid deep nesting. This copy-on-write model is the
single biggest source of "wait, why" — the chapter is worth reading in full:
[#mutation].

## Interfaces, unions, enums

These map 1:1 to their GraphQL counterparts and all discriminate with `case`
type patterns.

```dang
interface Named { name: String! }
type Person implements Named { name: String!; age: Int! }

union Pet = Cat | Dog          # members must be object types

enum Color { RED GREEN BLUE }  # bare CAPS, whitespace-separated — NOT commas
Color.RED                      # always qualified by the enum name
```

There is **no inheritance** — only `implements` for interfaces. Implementing
types must provide every interface field (a method satisfies a field). Union
`case`s covering all members are exhaustive. Interface-implementation variance
(covariant returns, contravariant args) is a richer topic worth a look when you
define interfaces: [#interfaces-unions].

## Talking to GraphQL

This is what the language is *for*. Import a schema and its types and root
fields are simply in scope.

```dang
let u = GitHub.user("vito")       # a root field, called like a function
u.databaseId                      # a no-arg field auto-calls
```

The headline feature is **multi-field selection** with double braces, which
compiles to a *single* GraphQL query no matter how deep:

```dang
user.{{ name, email, posts.{{ title, createdAt }} }}
```

The result is a record; index or assign to read it. Selection is **lazy** — a
GraphQL value accumulates its query and nothing is sent until the value is
*forced* (printed, asserted, assigned to a typed slot, indexed). That's what
makes the obvious code also the efficient one round-trip. Selection on a null
receiver short-circuits to `null`.

Mutations are root fields too; their side effects fire when the value is forced.
Unions and interfaces select per-type with inline fragments:

```dang
node(id: "x").{{
  ... on User { name, email }
  ... on Post { title }
}}
```

Server errors and non-null violations `raise`, so `try`/`catch` works against
the network too. Lazy inline fragments (`... on User` with no field block, for
narrowing) and the laziness/forcing model in depth: [#interop].

## Modules and configuration

A single `.dang` file is a module; a **directory** of `.dang` files is *also*
one module — file boundaries are invisible, declarations are shared and
hoisted, so split code however you like. Typed declarations are exported; `let`
stays internal (a top-level `let` is shared across the directory but not
exported — that's how you write private helpers).

Schemas are wired up in `dang.toml`, discovered by walking up from the working
directory:

```toml
[imports.Dagger]
dagger = true

[imports.GitHub]
endpoint = "https://api.github.com/graphql"
authorization = "Bearer ${GITHUB_TOKEN}"
```

Each `[imports.<Name>]` becomes a qualified namespace (`GitHub.user`), and
`import GitHub` also brings the names in unqualified. `${ENV_VAR}` expands;
commit the `dang.toml` and keep credentials in env vars. Sources can be `dagger
= true`, a local `schema` SDL file, a remote `endpoint`, or a `service` command.
Full wiring, shadowing, and ambiguity rules: [#modules].

## The standard library

Most functionality is **methods on values**, not global functions. A quick map
(full signatures in [#stdlib]):

```dang
# top-level builtins
assert { c.incr.value == 1 }      # testing, no framework — raises on failure
print(value)                      # to stdout
toString(value)                   # strings pass through; everything else JSON-encodes
loop { ... }                      # the looping builtin

# strings (no .length — that's list-only)
"hi".toUpper                      # also .toLower .trim .split .replace .contains ...
"call 555-1212".matchAll(`\d+`)   # regex via backtick patterns; returns [Match!]!

# lists (the only collection today)
xs.map { ... }; xs.filter { ... }; xs.reduce(0) { acc, x => acc + x }
xs.length; xs.isEmpty; xs.contains(v); xs.uniq; xs.join(", ")
xs[0]                             # out-of-bounds yields null, not an error

# codecs — type-driven decode produces the EXPECTED type
let cfg: Settings! = JSON.decode(raw)     # also YAML.* and TOML.*
JSON.encode(value)                        # keys sorted alphabetically

Random.int(1, 7); UUID.v7                  # randomness, ids
```

`assert { ... }` deserves a callout: it's built in, takes a block, and its
failure message includes the source expression and sub-values — drop assertions
straight into a script and you have tests without a framework. JSON/YAML/TOML
decoding is *type-driven*: the target type comes from a `::` cast, an
annotation, or the parameter/return type, and the parser fills it in.

## Directives {#20m-directives}

A directive is a typed annotation — a real declaration with checked args and
locations, not a comment pragma. It attaches metadata for tooling or a host
(Dagger reads `@defaultPath`, for instance); it never runs code.

```dang
directive @cache(ttl: Int! = 300) on FIELD_DEFINITION

type Person {
  name: String! @deprecated
  avatar: String! @cache(ttl: 60)
}
```

Imported schemas bring their directives in too. Full reference: [#directives].

## Beyond the tour

You've now seen everything you'll reach for day to day. The corners that didn't
make the cut — reach for these when you hit them:

- **`::` casts and custom scalars** — coercion rules, and why a non-null `::`
  cast is a runtime assertion (a footgun): [#nullability], [#enums-scalars].
- **Interface variance** — covariant returns, contravariant args, invariant
  scalars: [#interfaces-unions].
- **Lazy inline fragments and forcing** — narrowing GraphQL values, when a
  request actually fires: [#interop].
- **Multiple endpoints** — same-named types from different schemas are distinct;
  qualify to disambiguate: [#modules].
- **Regex and `Match`** — named groups, `replaceMatches`, `rewriteMatches`:
  [#strings].
- **Nested copy-on-write** — deep-path mutation and its cost: [#mutation].
- **Forward-reference cycles** — what's caught statically vs. at runtime:
  [#functions].
- **Operator precedence and the grammar** — the exact parse: [#operators],
  [#grammar].
- **The CLI, REPL, and LSP** — `:doc`, `:type`, formatting, editor setup:
  [#cli].
