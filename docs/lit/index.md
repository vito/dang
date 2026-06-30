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
