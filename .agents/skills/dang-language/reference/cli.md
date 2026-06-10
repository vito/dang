# CLI and REPL

The `dang` binary is a single command with one subcommand (`fmt`). Running scripts, directory modules, the REPL, and the LSP are all modes of the root command — there is **no** `run`/`check` subcommand.

## Synopsis
```
dang [flags] [file|directory]
dang <file.dang>     # run a single script
dang <directory>     # run all .dang files in the dir as one module
dang                 # no path -> interactive REPL (TUI)
dang fmt [flags] [path...]
```
Accepts at most one positional path argument.

## Install
```sh
go install github.com/vito/dang/v2/cmd/dang@latest    # or: go install ./cmd/dang
```

## Root flags
- `-d, --debug` — debug logging (slog at debug level)
- `--debug-addr <addr>` — serve debug/pprof handlers (e.g. `localhost:6060`)
- `--clear-cache` — clear the GraphQL schema cache and exit (cache under `$XDG_CACHE_HOME/dang/schemas` or `~/.cache/dang/schemas`)
- `--lsp` — run as a Language Server (JSON-RPC over stdio)
- `--lsp-log-file <path>` — LSP log file (defaults to stderr)
- `--cpuprofile <file>` — write a CPU profile
- `--version` — print version
- `-h, --help`

## `dang fmt`
- Format Dang source to the canonical style. Args: files/directories (directories scanned for `*.dang`, non-recursively).
- `-w, --write` — write result back to source (default: print to stdout)
- `-l, --list` — list files that would be formatted (or, with `-w`, that were changed)

The formatter: strips trailing commas, keeps comments attached to following/preceding code.

## REPL
Started by running `dang` with no path. Banner:
```
Welcome to Dang REPL v0.1.0
Imports: GitHub, Dagger

Type :help for commands, Tab for completion, Alt+Enter for multiline, Ctrl+D to exit
```
The `Imports:` line appears only when `dang.toml` configures imports.

Commands (prefix `:`):
- `:help` — list commands
- `:exit` / `:quit` — leave (also Ctrl+D)
- `:doc` — interactive API/schema browser
- `:env` — show environment bindings
- `:type <expr>` — show the inferred type of an expression
- `:find` / `:search <pattern>` — find functions/types by pattern
- `:reset` — rebuild the environment from imports
- `:clear` — clear the screen
- `:debug` — toggle debug mode
- `:version` — show version + configured imports
- `:history` — show recent input history

Input keys: Tab completion, Up/Down history, Alt+Enter (or Shift+Enter under a Kitty-protocol terminal) for multiline, Ctrl+L to clear.

## Exit codes
- `0` — success
- `1` — any error (runtime, assertion failure, type/parse error, or CLI usage error); no distinct code per failure kind

## Editor integration
- Dang ships a language server: run `dang --lsp` and point your editor's LSP client at the `dang` binary with that flag (autocomplete, type-on-hover, diagnostics).
- Ready-made editor configs ship for Neovim, VS Code, and Zed (see `editors/` in the Dang repo).
