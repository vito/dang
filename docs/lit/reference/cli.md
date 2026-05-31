\use-plugin{dang}

# CLI reference {#cli}

> Meta: I haven't enumerated every flag from the source. Walk through `cmd/dang` and fill in subcommands and flags as they actually exist; treat the below as a placeholder.

## Synopsis

```
dang <command> [flags] [args]
dang <file.dang>          # shorthand for `dang run`
dang <directory>          # run a directory module
```

## Commands

### `dang run`

- run a `.dang` file or directory module
- exits non-zero on uncaught error or assertion failure

### `dang fmt`

- format a file or directory in place
- principles in [formatter examples](../formatter-examples.md)

### `dang check`

- type-check without executing
- useful for editor/CI integration

> Meta: add `dang init`, `dang doc`, completion subcommand, etc., once stable.

## Environment variables

- `DANG_GRAPHQL_ENDPOINT`, `DANG_GRAPHQL_AUTHORIZATION`, `DANG_GRAPHQL_HEADER_*` — see [GraphQL configuration](../graphql-config.md)

## Exit codes

- `0` — success
- `1` — runtime error / assertion failure
- `2` — type or parse error
- (TBD — confirm against `cmd/dang`)

## Editor integration

- LSP at `pkg/lsp` — point your editor's LSP client at the `dang` binary
- VS Code, Zed, Neovim configurations under `editors/`
