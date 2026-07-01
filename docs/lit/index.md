\use-plugin{dang}
\split-sections

> **NOTE FROM A HUMAN:** this is an AI-assisted draft, for now just
> establishing the concepts, framing, and facts. Everything here is correct and
> verifiable, and I do like the brevity, but there are probably better ways to
> explain things. I'll be improving them gradually and this notice will go away
> when it's in a state I'm proud of. Sorry for any nonsense. Every paragraph
> has a 'feedback' button so you can yell at me about it anonymously.

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
\dang-feature{Hello, world!}{{{
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

# fields that return scalars query on access
print(`${u.name} is ${u.age} (${u.status})`)

# use sub-selections to avoid spamming queries
users.{{ name, age, status }}.each {
  print(`${_.name} (${_.age}): ${_.status}`)
}
}}}{
`Demo` is a [small schema](https://github.com/vito/dang/blob/main/tests/gqlserver/schema.graphqls) bundled into this page and resolved in-process.

Normally it would be defined with a `dang.toml` like this:

```toml
[imports.Demo]
schema = "./tests/gqlserver/schema.graphqls"
service = ["go", "run", "./tests/gqlserver/service"]
```
}
}{
\dang-feature{Parallel selection}{{{
import Demo

# .{{ }} is parallel selection

# for GraphQL, it queries for all fields at once
Demo.posts.{{ title, author.{{ name }} }}.each {
  print(`${_.title} — ${_.author.name}`)
}

# for native values, it parallelizes across lists and fields
type City {
  name: String!
  code: String! {
    # click Run again and the log order might* change
    print(`${Demo.hello(name)}`)
    name.toUpper
  }
}
[City("portland"), City("austin")].{{ name, code }}.each {
  print(`${_.name} → ${_.code}`)
}

# * Turns out WASM is deterministic, but it might change
#   when switching from server-rendered to client-rendered.
}}}{
}
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
GitHub's GraphQL API explorer was sadly [removed][GH-Explorer] -- so here's
something kind of close.

[GH-Explorer]: https://github.blog/changelog/2025-08-21-graphql-explorer-removal-from-api-documentation-on-november-1-2025/

To try it, sign in with GitHub and hit **Run**.

> **NOTE:** this will ask for read-only access (`read:user`). The
> token only ever exists client-side and expires with the tab.

In a project you'd wire it up in `dang.toml`:

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
## shared state changes are copy-on-write

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

# as a result of this truce between mutable and immutable,
# we gain trivial syntax for deep structural updates
type Tree {
  node: Node!
  bump: Tree! {
    # under the hood this is something like:
    #   self = clone(self)
    #   self.node = clone(self.node)
    #   self.node.leaf = clone(self.node.leaf)
    #   self.node.leaf.c = self.node.leaf.c + 100
    self.node.leaf.c += 100
    self
  }
}
type Node { leaf: Leaf! }
type Leaf { c: Int! }
let t = Tree(Node(Leaf(42)))
[t.bump.bump.node.leaf.c, t.node.leaf.c]
}}}{
See [#mutation] for more details.
}
}{
\dang-feature{Block arguments}{{{
## &block args are Dang's closures

"""
`if` but implemented with blocks.
"""
when(condition: Boolean!, &body: a): a {
  if (condition) {
    # body is a zero-arity function, so it gets auto-called
    # like any other field
    body
    # use &body to grab the function without calling it.
    # a bit like keeping the pin in the grenade.
  }
}

when(false) { raise "i died" }
when(true) { "i lived" }
}}}{
See [#blocks] for more details.
}
}{
\dang-feature{HTML DSL}{{{
## a DSL for generating HTML

"""
Anything that can be rendered to a string.
"""
interface Content {
  render: String!
}

"""
An HTML element.
"""
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
      `${sofar} ${name}="${escapeHTML(val)}"`
    }}>${children.map { _.render }.join("")}</${tag}>`
  }

  # DSL helpers
  ul(&body(root: Element!): Content!): Element! { element("ul") { body(_) } }
  li(&body(root: Element!): Content!): Element! { element("li") { body(_) } }
  a(href: String!, &body(root: Element!): Content!): Element! {
    element("a", ["href": href]) { body(_) }
  }
}

"""
Plain text to embed.
"""
type Text implements Content {
  text: String!
  render: String! { escapeHTML(text) }
}

# a shared HTML escaper for text and (double-quoted) attribute values —
# ampersand first, so we don't double-escape the entities below
let escapeHTML(s: String!): String! {
  s
    .replace("&", "&amp;")
    .replace("<", "&lt;")
    .replace(">", "&gt;")
    .replace(`"`, "&quot;")
}

# a function to kick off the DSL
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
## `case` uses exhaustiveness to determine nullability

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
## plain old try/catch/raise

type OddError implements Error {
  number: Int!
  message: String! {
    `${number} is odd`
  }
}

half(n: Int!): Int! {
  if (n % 2 != 0) {
    raise OddError(n)
  } else {
    n / 2
  }
}

## `catch` is like `case` - you can match on error types
try { half(7) } catch { err => err.message }
}}}{
See [#errors] for more details.
}
}{
\dang-feature{Null propagation}{{{
type Post {
  title: String! # non-null (!) - Post requires it
  author: Author # nullable — may be absent
}

type Author {
  name: String!
}

# chaining from a null value just propagates null
# coming from Haskell, it's like the Maybe monad
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
\dang-feature{Multi-line strings}{{{
## triple-quoted strings trim leading whitespace
print("""
  hey there
    im indented
      me too
""")
print("---")


## backtick-fenced strings support interpolation
let daggerBin = "dagger-dev"
let daggerVersion = [1, 0, 0]
print(```
$ ${daggerBin} version
${daggerVersion.join(".")}
```)
print("---")

# backtick-fences support language tags, like Markdown
# why? because it looks nice.
print(```ruby
  class << self
    puts 'wahh'
  end
```)
}}}
}

\table-of-contents

\include-section{./getting-started.md}
\include-section{./language.md}
\include-section{./types.md}
\include-section{./graphql.md}
\include-section{./data.md}
\include-section{./reference.md}
