\use-plugin{dang}

# Getting started {#getting-started}

> Meta: keep this page small. The promise is "running code in 5 minutes." Anything that risks shaving 30 seconds off should be moved out. Show one Dagger example and one plain-GraphQL example so readers see the dual nature early.

## Install

- `go install ./cmd/dang` (or whatever the eventual release channel is)
- editor support: VS Code, Zed, Neovim (see `editors/`)

## Hello, world

```dang
print("hello, world")
```

- `dang hello.dang` to run

## A first GraphQL call

- minimal `dang.toml` pointing at a schema
- a one-liner that queries it
- show both *qualified* (`Test.User`) and *unqualified* (`User`) access

> Meta: mention `DANG_GRAPHQL_ENDPOINT` here only as a forward reference; full table lives in [GraphQL configuration](./graphql-config.md).

## A Dagger module in 10 lines

- show the README's `Dang { source, build, test }` example
- explain what each line is doing in a sentence

## Where next

- [Language overview](./language/overview.md) for the mental model
- [GraphQL interop](./language/graphql.md) once you want to do real work
