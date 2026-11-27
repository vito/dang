# Dang for IntelliJ IDEA

Language support for [Dang](https://github.com/vito/dang) in IntelliJ IDEA
2026.3+ (and other IntelliJ-based IDEs on the same platform version).

## Features

- A **Dang** file type for `*.dang`, with its own icon
- Syntax highlighting, customisable under *Settings | Editor | Color Scheme |
  Dang*
- Brace matching and auto-closing, quote pairing, comment toggling (`#`), and
  indentation on Enter (2 spaces by default, under *Code Style | Dang*)
- Completion, hover, go to definition, find usages, rename, formatting, code
  actions, workspace symbols and diagnostics — via IntelliJ's built-in LSP
  client talking to `dang --lsp`

The LSP server is started for `.dang` files inside the project's content
roots, in a trusted project.

## Requirements

- IntelliJ IDEA 2026.3 or newer. The plugin uses the platform LSP API as
  introduced in 2026.3 (`LspIntegrationProvider` / `LspClient`), which is
  available without an Ultimate subscription.
- The `dang` binary:

  ```sh
  go install github.com/vito/dang/v2/cmd/dang@latest
  ```

  The plugin looks for it on `PATH`, then in `$GOBIN`, `$GOPATH/bin` and
  `~/go/bin`. The server runs with your login-shell environment, so `dagger`,
  `direnv` and `DAGGER_*` variables are picked up even when the IDE was
  started from a desktop launcher.

## mise

If the project root has a mise config (`mise.toml`, `.mise.toml`,
`.config/mise.toml`, … — anywhere mise reads one from), the server is started
as `mise exec -- dang --lsp` in that root, so it runs with the project's
pinned tools and environment. This matters for Dagger: `dang` starts
`dagger session`, and the `dagger` CLI version (or `DAGGER_X_RELEASE`)
decides which engine runs — the wrong one replaces the project's engine and
kills builds in progress.

Without mise installed, or with *Start through mise* turned off, the server
starts from `PATH` as usual; if the project has a mise config but mise wasn't
found, a notification says so. The mise config must be trusted
(`mise trust`), or `mise exec` refuses to run.

## Settings

*Settings | Languages & Frameworks | Dang*:

| Setting | Default | Description |
|---|---|---|
| Enable language server | on | Start `dang --lsp` for `.dang` files |
| Path to dang | empty (search `PATH`) | Explicit path to the binary |
| Start through mise | on | Use `mise exec` when the project has a mise config |
| Path to mise | empty (search `PATH`, `~/.local/bin`) | Explicit path to the binary |
| LSP log file | empty | Passed as `--lsp-log-file` |

Changes restart the server. To restart it yourself (say, after upgrading
`dang` or `dagger`, since the server caches import failures), use *Tools |
Restart Dang Language Server* (or find it with Ctrl+Shift+A). The server's
status and a restart button are also in the language services widget (`{}`)
in the status bar.

## Building

Requires JDK 21.

```sh
cd editors/intellij
./gradlew buildPlugin       # build/distributions/dang-intellij-<version>.zip
./gradlew runIde            # sandbox IDE with the plugin installed
./gradlew verifyPlugin      # JetBrains Plugin Verifier against platformVersion
```

Install the zip with *Settings | Plugins | ⚙ | Install Plugin from Disk…*,
then restart the IDE.

Local builds are versioned with the commit they were built from, e.g.
`0.1.0+77e01fb` (`.dirty` with uncommitted changes), so *Settings | Plugins*
shows which build is installed. Pass `-Prelease` for the plain version.

## Tests

```sh
go build -o /tmp/dang ./cmd/dang          # from the repo root
cd editors/intellij
DANG_BINARY=/tmp/dang ./gradlew test
```

The lexer tests check tokenization (strings, nested interpolation, escapes,
heredocs), that re-lexing from any token's saved state gives the same tokens
(the editor relies on this while you type), and that the keyword lists match
the VS Code grammar. The plugin tests run a headless IDE: file type and
highlighting, comment toggling, Enter between braces, and a real language
server round trip (hover and completion, both raw over LSP and through the
IDE's completion). Without `DANG_BINARY` the language server test is skipped.

## Notes

- Highlighting is lexer-based (`DangLexer`), mirroring
  `editors/vscode/syntaxes/dang.tmLanguage.json`. When the language changes,
  update both; a test fails if their keyword lists drift apart.
- The PSI is flat: completion, navigation, references and diagnostics all
  come from the language server.
