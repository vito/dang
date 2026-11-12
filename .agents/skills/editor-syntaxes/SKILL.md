---
name: editor-syntaxes
description: How to maintain editor syntax definitions for Dang. Use when adding or removing keywords, tokens, or language constructs.
---

# Editor Syntax Maintenance

Dang has syntax definitions for three editors. When the language grammar
changes (keywords added/removed, tokens renamed, new constructs), all
three must be updated in sync.

## Locations

### VSCode
- `editors/vscode/syntaxes/dang.tmLanguage.json` — TextMate grammar
- Lives in the main repo (not a submodule)
- Keywords are in the `keyword-control` pattern as a `\b(...)\b` regex

### Zed
- `editors/zed/languages/dang/highlights.scm` — tree-sitter highlights
- **Submodule** pointing to `https://github.com/vito/zed-dang`
- Commit inside the submodule first, then update the submodule pointer
  in the parent repo

### Neovim
- `editors/nvim/queries/dang/highlights.scm` — tree-sitter highlights
- **Submodule** pointing to `https://github.com/vito/dang.nvim`
- Commit inside the submodule first, then update the submodule pointer
  in the parent repo

## Tree-sitter highlights (Zed + Neovim)

The two `.scm` files are nearly identical but use different capture
names (Zed uses `@keyword.control`, `@variable.special`, etc.; Neovim
uses `@keyword`, `@variable.builtin`, etc.). Edit both when making
changes.

Keywords are listed as `(foo_token)` nodes inside `[ ... ] @keyword`
blocks. To add or remove a keyword, add or remove the corresponding
`(foo_token)` line in both files.

## TextMate grammar (VSCode)

Keywords live in the `keyword-control` repository entry as a single
regex. To add or remove a keyword, edit the `\b(...)\b` alternation.

## Committing

Since Zed and Neovim are submodules:

```bash
# 1. Commit in each submodule
cd editors/zed && git add -A && git commit -m "..." && cd ../..
cd editors/nvim && git add -A && git commit -m "..." && cd ../..

# 2. Commit submodule pointers + VSCode changes in parent
git add editors/zed editors/nvim editors/vscode/syntaxes/dang.tmLanguage.json
git commit -m "chore(editors): ..."
```

## Embedded highlight query (REPL)

The REPL highlights its input with the same Zed `highlights.scm`, embedded
into the binary at `pkg/dang/highlights.scm` (a generated copy, since
`go:embed` can't follow the `treesitter/queries/highlights.scm` symlink into
the submodule). `./hack/generate` (via `go generate ./...`) refreshes it with
`cp`, so it tracks the query automatically — just re-run generation after
editing the Zed query. New capture names also need a case in `captureClass`
in `pkg/dang/highlight_common.go` (kept in lockstep with
`docs/go/highlight.go`).

## WebAssembly parser

Dang itself (error recovery, completion, REPL highlighting) and the docs
site's Dang highlighting run the C tree-sitter runtime and grammar compiled
to WebAssembly: `pkg/dang/tswasm/dang.wasm`, run by wazero, so nothing needs
cgo. It is checked in and built by the `treesitter-wasm` Dagger generator
(`pkg/dang/tswasm/build.sh`, with a pinned zig), whose stale check fails when
`treesitter/src` changes without it. Regenerate it with
`dagger generate treesitter-wasm` after `tree-sitter generate`. Until then,
the embedded highlight query can fail to compile against the old grammar
("node type" error) and the REPL silently renders plain text
(`TestHighlightCodeStylesAndPreservesText` fails).

## Checklist

When a language keyword or token changes (and if old code used the old form,
also follow the `language-versions` skill to keep it parsing; a new keyword
follows its "adding a keyword" checklist for the grammar and tests):

1. [ ] Update `editors/zed/languages/dang/highlights.scm`
2. [ ] Update `editors/nvim/queries/dang/highlights.scm`
3. [ ] Update `editors/vscode/syntaxes/dang.tmLanguage.json`
4. [ ] Re-run `./hack/generate` to refresh the embedded `pkg/dang/highlights.scm`,
   and the `treesitter-wasm` generator for the parser Dang runs
5. [ ] Add/refresh `treesitter/test/corpus/` and `editors/highlights/corpus/`
       cases for the new construct (`tree-sitter test --update`,
       `go test ./editors/ -run TestNeovimHighlights -update`)
6. [ ] Commit submodules, then parent repo
