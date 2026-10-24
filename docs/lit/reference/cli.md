\use-plugin{dang}

# CLI reference {#cli}

> Meta: enumerated from `cmd/dang/` (root `main.go`, `repl_commands.go`). The binary is a single Cobra command (`dang`) with three subcommands (`fmt`, `init`, `migrate`); running scripts, directory modules, the REPL, and LSP are all modes of the root command — there is no `run`/`check` subcommand.

## Synopsis

```
dang [flags] [file|directory]
dang <file.dang>     # run a single script
dang <directory>     # run a directory as a module
dang                 # no path -> interactive REPL
dang fmt [flags] [path...]
dang init [dir]
dang migrate [flags] [dir...]
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

## `dang init`

- make a directory (default: `.`) a project whose `dang.toml` declares its Dang version (`dang = "X.Y"`; see [#dang.toml])
- with no `dang.toml`, creates one declaring the running Dang's version; a `dang.toml` without the key gets the version it already means (`"2.1"`), so nothing changes meaning
- refuses a directory that is already part of a project further up

## `dang migrate`

- move every project under the given directories (default: `.`; directories only) to a newer Dang version: each `.dang` file a `dang.toml` governs is rewritten in the new version's syntax, and the `dang.toml`'s `dang` key is updated with it
- only reports what it would change unless `-w` is given; files spelled the same in both versions are left untouched
- it rewrites syntax only: code a newer version rejects for other reasons (e.g. a stricter type check) is left for you to fix
- refuses a file whose meaning would change, e.g. old syntax inside a `#nofmt` region, which must be migrated by hand
- flags:
  - `-w, --write` — write the migrated files and `dang.toml`
  - `--to <X.Y>` — version to migrate to (default: the running Dang's version)
  - `--from <X.Y>` — version of source whose version no `dang.toml` declares: source no `dang.toml` governs (e.g. a Dagger module pinned by its engine version), or a project from an older Dang whose `dang.toml` predates the `dang` key; migrating records the new version, creating a `dang.toml` if there is none. It applies to the given directory's own project, not to projects nested below it: those without a `dang` key are reported, to migrate by naming their directories
  - `--skip-unparseable` — leave files that don't parse in their version as they are (otherwise `-w` refuses to write)

## REPL

Started by running `dang` with no path. Banner:

```
Welcome to Dang REPL v0.1.0
Imports: GitHub, Dagger

Type :help for commands, Tab for completion, Alt+Enter for multiline, Ctrl+D to exit
```

The `Imports:` line appears only when `dang.toml` configures imports.

REPL commands (prefix `:`):

- `:help` — list commands
- `:exit` / `:quit` — leave the REPL (also Ctrl+D)
- `:doc` — interactive API/schema browser. See [#interop].
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

- `dang.toml`'s top-level `dang = "X.Y"` key declares the Dang version the project is written for; see [#dang.toml].
- GraphQL connections are configured per-import in `dang.toml` under `[imports.<Name>]`. Keys: `dagger`, `schema` (local `.graphqls` SDL path), `endpoint`, `service` (command that prints its endpoint/session JSON), `authorization`, `headers`. A `path` key instead imports a native Dang module (a local directory of `.dang` files); see [#dang-imports].
- `endpoint`, `authorization`, and `headers` values support `${VAR}` environment expansion. `$(...)` command substitution is **not** supported (use an env var instead). See [#getting-started] and [#interop].
- config is discovered by walking up from the working directory, stopping at a `.git` boundary.

## Exit codes

- `0` — success
- `1` — any error (runtime error, assertion failure, type/parse error, or a CLI usage error); there is no distinct exit code per failure kind

## Editor integration

- LSP via `dang --lsp` (handler in `pkg/lsp`) — point your editor's LSP client at the `dang` binary with that flag
- hover shows a symbol's type; on a closure it also lists the effects that limit where it can go — what it writes outside itself and which block parameters it calls, e.g. `// Effects: writes n; calls block` (see [#impure-closures])
- editor configs under `editors/` (`nvim`, `vscode`, `zed`)
