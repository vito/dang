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
\dang-feature{Prototype objects}{{{
# `type` declares a type AND its constructor in one go
type User {
  name: String!
  greeting: String! { `Hi, I'm ${name}` }
}

User("Ada").greeting
}}}
}{
\dang-feature{Multi-field selection}{{{
type Repo {
  name: String!
  stars: Int! { 1000 }
}

# select many fields at once; against a GraphQL schema this is
# ONE query whose fields resolve in parallel
[Repo("dang"), Repo("booklit")].{{ name, stars }}.map { r => `${r.name} ★${r.stars}` }
}}}
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
\dang-feature{Null tracking}{{{
# String may be null; String! cannot — the type system tracks the gap
let name: String = "Dang"

# inside the guard, name narrows from String to String!
if (name != null) { name.toUpper } else { "(none)" }
}}}
}{
\dang-feature{Optional parens}{{{
type Circle {
  r: Float!
  area: Float! { 3.14159 * r * r }
}

let c = Circle(2.0)
# r is stored, area is computed — but they're accessed identically
[c.r, c.area]
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
\dang-feature{Testing built in}{{{
# assert is a builtin — high-level testing, no framework
assert { [1, 2, 3].map { x => x * 2 } == [2, 4, 6] }
"tests pass"
}}}
}

\dang-playground{{{
# Edit me, then hit Run — this evaluates in your browser.
type Greeter {
  name: String!
  greet: String! { `Hello, ${name}!` }
}

["world", "Dang", "you"].map { who => Greeter(who).greet }
}}}

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
