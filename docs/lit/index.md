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

\shell{go install github.com/vito/dang/cmd/dang@latest}

\dang-playground{{{
# Edit me, then hit Run — this evaluates in your browser.
type Greeter {
  pub name: String!
  pub greet: String! { `Hello, ${name}!` }
}

["world", "Dang", "you"].map { who => Greeter(who).greet }
}}}

> Meta: this is the landing page — pitch, install, first-look code. Skim-friendly. Real navigation lives in the sidebar; don't try to build a full TOC here.

\table-of-contents

\include-section{./getting-started.md}
\include-section{./language/overview.md}
\include-section{./language/syntax.md}
\include-section{./language/literals.md}
\include-section{./language/types.md}
\include-section{./language/operators.md}
\include-section{./language/fields.md}
\include-section{./language/functions.md}
\include-section{./language/blocks.md}
\include-section{./language/control-flow.md}
\include-section{./language/objects.md}
\include-section{./language/mutation.md}
\include-section{./language/interfaces-unions.md}
\include-section{./language/enums-scalars.md}
\include-section{./language/errors.md}
\include-section{./language/flow-typing.md}
\include-section{./language/modules.md}
\include-section{./language/directives.md}
\include-section{./language/graphql.md}
\include-section{./github-playground.md}
\include-section{./language/collections.md}
\include-section{./language/strings.md}
\include-section{./language/json-yaml.md}
\include-section{./reference/stdlib.md}
\include-section{./reference/grammar.md}
\include-section{./reference/cli.md}
