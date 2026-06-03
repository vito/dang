\use-plugin{dang}

# CLI reference {#cli}

> Meta: enumerated from `cmd/dang/` (root `main.go`, `repl_commands.go`). The binary is a single Cobra command (`dang`) with one subcommand (`fmt`); running scripts, directory modules, the REPL, and LSP are all modes of the root command — there is no `run`/`check` subcommand.

## Synopsis

```
dang [flags] [file|directory]
dang <file.dang>     # run a single script
dang <directory>     # run a directory as a module
dang                 # no path -> interactive REPL
dang fmt [flags] [path...]
```

## Root command

- `dang <file.dang>` — run a single `.dang` script (`RunFile`)
- `dang <directory>` — run all `.dang` files in the directory as one module (`RunDir`). See [#modules].
- `dang` (no args) — start the interactive REPL (TUI)
- accepts at most one positional path argument

### Root flags

- `-d, --debug` — enable debug logging (slog at debug level)
- `--debug-addr <addr>` — serve debug/pprof handlers on this address (e.g. `localhost:6060`)
- `--clear-cache` — clear the GraphQL schema cache and exit. Cache lives under `$XDG_CACHE_HOME/dang/schemas` (or `~/.cache/dang/schemas`).
- `--lsp` — run as a Language Server (JSON-RPC over stdio)
- `--lsp-log-file <path>` — LSP log file (defaults to stderr)
- `--cpuprofile <file>` — write a CPU profile to file
- `--version` — print version (`v0.1.0`, commit `dev`) — provided by the `fang` wrapper
- `-h, --help`

## `dang fmt`

- format Dang source according to the canonical style. See [#syntax].
- args: one or more files/directories (directories are scanned for `*.dang`, non-recursively)
- flags:
  - `-w, --write` — write the result back to the source file (default: print to stdout)
  - `-l, --list` — list files that would be formatted (or, with `-w`, that were changed)

## REPL

Started by running `dang` with no path. Banner:

```
Welcome to Dang REPL v0.1.0
Imports: GitHub, Dagger

Type :help for commands, Tab for completion, Alt+Enter for multiline, Ctrl+D to exit
```

> verify: the `Imports:` line is only shown when a `dang.toml` configures imports; version string `v0.1.0` is hardcoded in `cmd/dang` and may drift.

REPL commands (prefix `:`):

- `:help` — list commands
- `:exit` / `:quit` — leave the REPL (also Ctrl+D)
- `:doc` — interactive API/schema browser. See [#graphql].
- `:env` — show environment bindings
- `:type <expr>` — show the inferred type of an expression
- `:find` / `:search <pattern>` — find functions/types by pattern
- `:reset` — rebuild the environment from imports
- `:clear` — clear the screen
- `:debug` — toggle debug mode
- `:version` — show version + configured imports
- `:history` — show recent input history

Input keys: Tab completion, Up/Down history, Alt+Enter (or Shift+Enter under a Kitty-protocol terminal) for multiline, Ctrl+L to clear.

## Configuration

- GraphQL connections are configured per-import in `dang.toml` under `[imports.<Name>]`. Keys: `dagger`, `schema` (local `.graphqls` SDL path), `endpoint`, `service` (command that prints its endpoint/session JSON), `authorization`, `headers`.
- `endpoint`, `authorization`, and `headers` values support `${VAR}` environment expansion. `$(...)` command substitution is **not** supported (use an env var instead). See [#getting-started] and [#graphql].
- config is discovered by walking up from the working directory, stopping at a `.git` boundary.

## Exit codes

- `0` — success
- `1` — any error (runtime error, assertion failure, type/parse error, or a CLI usage error)

> verify: `cmd/dang/main.go` calls `os.Exit(1)` for every failure path via `fang.Execute`; there is no distinct exit code for type/parse vs runtime errors today.

## Editor integration

- LSP via `dang --lsp` (handler in `pkg/lsp`) — point your editor's LSP client at the `dang` binary with that flag
- editor configs under `editors/` (`nvim`, `vscode`, `zed`)
