\use-plugin{dang}
\split-sections

# dang {#index}

A statically typed scripting language for GraphQL, where the types and
functions are loaded directly from the schema.


\header-links{
  [GitHub](https://github.com/vito/dang)
}{
  [pkg.go.dev](https://pkg.go.dev/github.com/vito/dang)
}

\shell{go install github.com/vito/dang/v2/cmd/dang@latest}

\dang-carousel{
\dang-feature{Hello, world}{{{
type Greeter {
  name: String!
  greet: String! { `Hello, ${name}!` }
}

["world", "Dang", "you"].map { who => Greeter(who).greet }
}}}
}{
\dang-feature{Schema-native types}{{{
# Dang's types and root fields come straight from a GraphQL schema:
import Demo

# every schema type and Query field is now part of the language —
# select fields and map over the result
users.{{ name, status }}.map { u => `${u.name}: ${u.status}` }
}}}{
`Demo` is a small schema bundled into this page and resolved in-process — [see its SDL](https://github.com/vito/dang/blob/main/tests/gqlserver/schema.graphqls).
}
}{
\dang-feature{One query, in parallel}{{{
import Demo

# .{{ }} selects fields across the graph and compiles to a SINGLE
# GraphQL request whose fields are resolved in parallel
posts.{{ title, author.{{ name }} }}.map { p => `${p.title} — ${p.author.name}` }
}}}
}{
\dang-feature{Arguments & nullability}{{{
import Demo

# root fields take the schema's arguments; results carry its
# nullability — name is String!, age is Int (may be null)
let u = user("1")
`${u.name} is ${u.age} (${u.status})`
}}}
}{
\dang-github-feature{import GitHub}{{{
import GitHub

# the same idea against a real schema: `viewer` is GitHub's
# authenticated user, and this is one query
viewer.{{
  login
  name
  repositories(first: 3).{{ nodes.{{ name, stargazerCount }} }}
}}
}}}{
Sign in above, then **Run** — introspection and queries go straight to `api.github.com` from your browser, and the token stays in this tab. In a project you'd wire it up in `dang.toml`:

```toml
[imports.GitHub]
endpoint = "https://api.github.com/graphql"
authorization = "Bearer ${GITHUB_TOKEN}"
```
}
}{
\dang-feature{Copy-on-write}{{{
type Counter {
  n: Int!
  bump: Counter! { n += 1; self }
}

let c = Counter(0)
# values are immutable — methods fork the receiver, so c never changes
[c.bump.bump.n, c.n]
}}}
}{
\dang-feature{Everything is an expression}{{{
# case yields a value, like if/loop/try — assign it, return it, pass it
classify(n: Int!): String! {
  case (n) {
    0 => "zero"
    else => if (n > 0) "positive" else "negative"
  }
}

[classify(0), classify(7), classify(-3)]
}}}
}{
\dang-feature{Block arguments}{{{
# any function can take a trailing block — &body is a block parameter.
# it's what powers .map and loop, and lets you build little DSLs:
tag(name: String!, &body: String!): String! { `<${name}>${body()}</${name}>` }

tag("ul") {
  tag("li") { "one" } + tag("li") { "two" }
}
}}}
}{
\dang-feature{Type switching}{{{
type Cat {
  name: String!
  sound: String! { "meow" }
}
type Dog {
  name: String!
  sound: String! { "woof" }
}
union Pet = Cat | Dog

# case switches on the runtime type; each arm narrows p to that type
speak(p: Pet!): String! {
  case (p) {
    c: Cat => `${c.name} says ${c.sound}`
    d: Dog => `${d.name} says ${d.sound}`
  }
}

[speak(Cat("Tom")), speak(Dog("Rex"))]
}}}
}{
\dang-feature{Errors are values}{{{
# raise for failures; try/catch is an expression that recovers them
half(n: Int!): Int! { if (n % 2 != 0) raise `${n} is odd` else n / 2 }

try { half(7) } catch { e => e.message }
}}}
}{
\dang-feature{Null propagation}{{{
type Author { name: String! }
type Post {
  title: String!
  author: Author        # nullable — may be absent
}

# the chain short-circuits to null if any link is null — no crash.
# (Haskell folks: it's the Maybe monad, woven into member access.)
Post("Hello", null).author.name
}}}
}{
\dang-feature{Interfaces}{{{
interface Shape { area: Float! }

type Square implements Shape {
  side: Float!
  area: Float! { side * side }
}
type Circle implements Shape {
  r: Float!
  area: Float! { 3.14 * r * r }
}

# one list, two types; _ is the implicit block argument
[Square(3.0), Circle(2.0)].map { _.area }
}}}
}{
\dang-feature{Auto-calling}{{{
# a zero-arg function auto-calls when you name it — no parens needed
coin: String! { "heads" }

let called = coin     # auto-called, yields "heads"
let fn = &coin        # &coin grabs the function itself, uncalled
[called, fn()]
}}}
}{
\dang-feature{Records}{{{
# records are anonymous structs (double braces); they compare by value,
# regardless of field order
{{ x: 1, y: 2 }} == {{ y: 2, x: 1 }}
}}}
}{
\dang-feature{Regex}{{{
# rewriteMatches runs a block over each match — here, exclaim every word
"hello world".rewriteMatches(`\w+`) { _.string + "!" }
}}}
}{
\dang-feature{Functional pipelines}{{{
# blocks are Dang's lambdas; _ is the implicit arg ({ x, i => } also gives an index)
[1, 2, 3, 4, 5, 6].filter { _ % 2 == 0 }.map { _ * _ }.reduce(0) { acc, x => acc + x }
}}}
}{
\dang-feature{Multi-line strings}{{{
# triple-quoted """ is raw; triple-backtick interpolates and takes an
# optional, cosmetic language tag
let name = "world"
```sql
SELECT * FROM users WHERE name = '${name}'
```
}}}
}

> **NOTE FROM A HUMAN:** this is an AI-assisted draft, for now just
> establishing the concepts, framing, and facts. Everything here is correct and
> verifiable, and I do like the brevity, but there are probably better ways to
> explain things. I'll be improving them gradually and this notice will go away
> when it's in a state I'm proud of. Sorry for any nonsense. Every paragraph
> has a 'feedback' button so you can yell at me about it anonymously.

\table-of-contents

\include-section{./getting-started.md}
\include-section{./language.md}
\include-section{./types.md}
\include-section{./graphql.md}
\include-section{./data.md}
\include-section{./reference.md}
