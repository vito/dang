# Dang for Visual Studio Code

Syntax highlighting and language support for [Dang](https://github.com/vito/dang).

## Features

- Syntax highlighting via TextMate grammar
- LSP support (hover, go-to-definition, completion, formatting, rename, etc.)
- Auto-closing brackets and strings
- Comment toggling (`#`)
- Indentation rules

## Configuration

| Setting | Default | Description |
|---|---|---|
| `dang.lsp.enabled` | `true` | Enable the Dang language server |
| `dang.lsp.path` | `"dang"` | Path to the `dang` binary |
| `dang.lsp.logFile` | `""` | Path to LSP log file (empty for no logging) |

## Installation

Until this extension is published, install from source:

```sh
cd editors/vscode
npm install
npm run compile
npx vsce package
code --install-extension dang-0.1.0.vsix
```
