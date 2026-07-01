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
import Demo # configured in dang.toml below

# imports become globals
let u = user("1") # or Demo.user("1") to be explicit
print(`${u.name} is ${u.age} (${u.status})`)

# use sub-selections to avoid N+1 queries
users.{{ name, age, status }}.map { user =>
  `${user.name} (${user.age}): ${user.status}`
}

# each forced selection echoes the GraphQL query it compiled to (the → lines)
}}}{
`Demo` is a small schema bundled into this page and resolved in-process — [see its SDL](https://github.com/vito/dang/blob/main/tests/gqlserver/schema.graphqls).
}
}{
\dang-feature{Parallel selection}{{{
import Demo

# against a GraphQL schema, .{{ }} compiles to a SINGLE request whose fields
# resolve in parallel — here, each post with its author, in one query:
print(posts.{{ title, author.{{ name }} }}.map {
  # _ inside a block is shorthand for the first argument
  `${_.title} — ${_.author.name}`
})

# the same .{{ }} works on native objects too — a parallel map over the list,
# each sub-selection also evaluated in parallel by the Dang runtime:
type City {
  name: String!
  code: String! { name.toUpper }
}
[City("portland"), City("austin")].{{ name, code }}.map { `${_.name} → ${_.code}` }
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
Sign in and then click **Run**. Introspection and queries go straight to
`api.github.com` from your browser, and the token stays in this tab. In
a project you'd wire it up in `dang.toml`:

```toml
[imports.GitHub]
endpoint = "https://api.github.com/graphql"
authorization = "Bearer ${GITHUB_TOKEN}"
```

This `.envrc` might help too:

```sh
export GITHUB_TOKEN="$(gh auth token)"
```
}
}{
\dang-feature{Copy-on-write}{{{
type Counter {
  n: Int!
  bump: Counter! {
    n += 1 # this LOOKS like it mutates, but it doesn't!
    self   # this `self` is actually a clone with n += 1 applied
  }
}

let c = Counter(0)
assert("changes accumulate")  { c.bump.bump.n == 2 }
assert("original unmodified") { c.n == 0 }
}}}{
See [#mutation] for more details.
}
}{
\dang-feature{Block arguments}{{{
# any function can take a trailing block &body is a block parameter.
# it's what powers .map and loop, and lets you build little DSLs:
tag(name: String!, &body: String!): String! {
  `<${name}>${body()}</${name}>`
}

tag("ul") {
  tag("li") { "one" } + tag("li") { "two" }
}
}}}
}{
\dang-feature{HTML DSL}{{{
# just for fun, let's write a DSL for generating HTML

interface Content {
  render: String!
}

type Element implements Content {
  tag: String!
  attributes: Map[String!]! = [:]
  children: [Content!]! = []

  element(
    tag: String!
    attributes: Map[String!]! = [:]
    &body(root: Element!): Content!
  ): Element! {
    self.children += [body(Element(tag, attributes))]
    self
  }

  text(text: String!): Content! {
    self.children += [Text(text)]
    self
  }

  render: String! {
    # buckle up
    `<${tag}${attributes.reduce("") { sofar, name, val =>
      `${sofar} ${name}=${JSON.encode(val)}`
    }}>${children.map { _.render }.join("")}</${tag}>`
  }

  # DSL helpers
  ul(&body(root: Element!): Content!): Element! { element("ul") { body(_) } }
  li(&body(root: Element!): Content!): Element! { element("li") { body(_) } }
  a(href: String!, &body(root: Element!): Content!): Element! {
    element("a", ["href": href]) { body(_) }
  }
}

type Text implements Content {
  text: String!
  render: String! {
    # escape & first, so we don't double-escape the entities below
    text
      .replace("&", "&amp;")
      .replace("<", "&lt;")
      .replace(">", "&gt;")
  }
}

html(&body(root: Element!): Content!): Content! {
  body(Element("html", [:], []))
}

# there's no mutation, so we chain _ to build up the content
html {
  _.ul {
    _
      .li {
        _.a(href: "https://danglang.org") {
          _.text("Dang")
        }
      }
      .li {
        _.a(href: "https://bass-lang.org") {
          _.text("Bass")
        }
      }
  }
}.render
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

speak(p: Pet!): String! {
  # case exprs uses exhaustiveness to determine nullability;
  # if you add a Mouse without a Mouse branch here, you'll get a
  # String vs. String! type error thanks to the return type
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

# instead of crashing, chaining from a nullable type just propagates null
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

# type is widened to [Shape!]! during inference
[Square(3.0), Circle(2.0)].map { _.area }
}}}
}{
\dang-feature{Auto-calling}{{{
coin: String! { "heads" }

# zero-arg functions are indistinguishable from fields, as in GraphQL;
# references auto-call without requiring parentheses
let called = coin
let fn = &coin
{{
  called: [called, fn(), fn]
  # a bit like a grenade, you have to keep using & to prevent it
  # from being called.
  notCalled: [&coin, &fn]
}}
}}}
}{
\dang-feature{Records}{{{
# records are anonymous structs (double braces); they compare by value,
# regardless of field order
{{ x: 1, y: 2 }} == {{ y: 2, x: 1 }}
}}}
}{
\dang-feature{Multi-line strings}{{{
# triple-quoted """ is raw; triple-backtick interpolates and takes an
# optional, cosmetic language tag
let daggerBin = "dagger-dev"

# the toml body is highlighted as TOML (tree-sitter injection):
```toml
[imports.Dagger]
dagger = true
service = ["${daggerBin}", "session"]
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
