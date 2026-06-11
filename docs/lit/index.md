\use-plugin{dang}
\split-sections

# dang {#index}

> NOTE FROM A HUMAN: these docs are a rough AI-assisted draft. For this first
> phase I'm just establishing the concepts, framing, and facts. I'm publishing
> this ASAP to unblock users and LLMs on learning the language, and I'll
> gradually humanize and de-slop them over time. This notice will go away when
> it's in a state I'm proud of.
>
> Apologies in advance for any nonsense. Every paragraph has a 'feedback'
> button so you can yell at me about it anonymously.

A statically typed scripting language whose types and root functions come from a
GraphQL schema.

\header-links{
  [GitHub](https://github.com/vito/dang)
}{
  [pkg.go.dev](https://pkg.go.dev/github.com/vito/dang)
}

\shell{go install github.com/vito/dang/v2/cmd/dang@latest}

\dang-playground{{{
# Edit me, then hit Run — this evaluates in your browser.
type Greeter {
  name: String!
  greet: String! { `Hello, ${name}!` }
}

["world", "Dang", "you"].map { who => Greeter(who).greet }
}}}

> Meta: this is the landing page — pitch, install, first-look code, then a curated contents block. The TOC is intentional but stays at two levels (parts and their chapters — toc.tmpl caps the depth); deeper navigation lives in the sidebar and on the pages themselves.

\table-of-contents

\include-section{./getting-started.md}
\include-section{./language.md}
\include-section{./types.md}
\include-section{./graphql.md}
\include-section{./data.md}
\include-section{./reference.md}
