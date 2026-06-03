\use-plugin{dang}

# Getting started {#getting-started}

> Meta: keep this page small. The promise is "running code in 5 minutes." Anything that risks shaving 30 seconds off should be moved out. Show one Dagger example and one plain-GraphQL example so readers see the dual nature early.

## Install

- `go install github.com/vito/dang/cmd/dang@latest` (or `go install ./cmd/dang` from a checkout)
- editor support: VS Code, Zed, Neovim (see `editors/`)

## Hello, world

```dang
pub message(who: String!): String! {
  `hello, ${who}`
}

print(message("world"))
```

- `dang hello.dang` to run

## A first GraphQL call

Dang is designed for GraphQL APIs. To configure them, add a [#dang.toml]:

```toml
[imports.GitHub]
endpoint = "https://api.github.com/graphql"
authorization = "Bearer ${GITHUB_TOKEN}"
```

Now you can run `dang` in REPL mode and explore the GitHub API:

```sh
$ export GITHUB_TOKEN="$(gh auth token)"
$ dang
Welcome to Dang REPL v0.1.0
Imports: GitHub, Dagger

Type :help for commands, Tab for completion, Alt+Enter for multiline, Ctrl+D to exit

dang> GitHub.user("vito").databaseId
=> 1880
```

> Tip: To start an interactive schema browser, try `:doc`.

For a more elaborate GitHub demo, check out [`demos/github/main.dang`][github-demo].

[github-demo]: https://github.com/vito/dang/blob/main/demos/github/main.dang

## A Dagger module in 10 lines

Dang is a natural fit for writing and consuming [Dagger](https://dagger.io)
modules.

> TODO: update this for Dagger 1.0

```sh
$ dagger init --sdk=dang
```

```dang
type Greeter {
  pub message(target: String!): String! {
    container
      .from("ubuntu")
      .withExec(["apt-get", "update", "-y"])
      .withExec(["apt-get", "install", "-y", "cowsay"])
      .withExec(["/usr/games/cowsay", `Hello, ${target}!`])
      .stdout
  }
}
```

- show the README's `Dang { source, build, test }` example
- explain what each line is doing in a sentence

## Where next

- [#overview] for the mental model
- [#graphql] once you want to do real work
- [#objects] for `type` declarations and methods (like the Dagger module above)
- [#modules] for splitting a program across files / directory modules
