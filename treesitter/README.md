# tree-sitter-bass

[![Build Status](https://github.com/vito/tree-sitter-bass/actions/workflows/ci.yml/badge.svg)](https://github.com/vito/tree-sitter-bass/actions/workflows/ci.yml)
[![Discord](https://img.shields.io/discord/1063097320771698699?logo=discord)](https://discord.gg/w7nTvsVJhm)

[Bass](https://bass-lang.org/) grammar for [Tree-sitter](https://tree-sitter.github.io).

## Generating `highlights.scm`

Prerequisites: `go` and `make`

```sh
make
```

This will generate the following files:

- `queries/vim/highlights.scm` — suitable for [Neovim] highlighting
- `queries/helix/highlights.scm` — suitable for [Helix] highlighting

## A Quick Note on Precedence

`tree-sitter test`, Helix, and Neovim disagree on the precedence for
overlapping queries.

With `tree-sitter test` and Helix, the first matching query takes precedence,
whereas in Neovim the last matching query supersedes the ones before it.

To handle this the query template just conditionally switches the order of
the queries.

[Neovim]: https://github.com/neovim/neovim
[Helix]: https://github.com/helix-editor/helix
