\use-plugin{dang}

\styled{dang-home}

# Dang {#index}

A strongly typed scripting language whose types are backed by a GraphQL API.

\header-links{
  [GitHub](https://github.com/vito/dang)
}

\table-of-contents

## overview

Dang is a Hindley-Milner typed interpreted scripting language designed as a
lightweight SDK for GraphQL APIs. It features static type inference, pattern
matching, and a pipeline-oriented syntax.

## getting started

Clone the repository and build:

\shell{go install github.com/vito/dang@latest}

## features

- **Strong static typing** with Hindley-Milner type inference
- **GraphQL-backed types** — API types are first-class citizens
- **Pattern matching** with exhaustiveness checking
- **Pipeline syntax** for chaining method calls
- **Thunks** for lazy evaluation and deferred computation
