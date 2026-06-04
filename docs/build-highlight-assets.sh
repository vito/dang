#!/bin/bash
# Generates the playground editor's syntax-highlighting assets into docs/js/.
# Called by build.sh (and runnable standalone). All outputs are gitignored.
#
# Requires: tree-sitter CLI, curl, tar, git. The first `tree-sitter build
# --wasm` downloads a wasi-sdk toolchain (~114MB), cached thereafter. The
# playground editor degrades to plain (uncolored) text if these assets are
# missing, so a failure here is non-fatal to the docs build.
set -euo pipefail
cd "$(dirname "$0")"
root="$(cd .. && pwd)"

WTS_VERSION="0.25.10" # web-tree-sitter; keep ABI-compatible with the tree-sitter CLI

echo "==> grammar wasm (tree-sitter build --wasm)"
( cd "$root/treesitter" && tree-sitter build --wasm -o "$root/docs/js/tree-sitter-dang.wasm" . )

echo "==> highlight query"
# highlights.scm lives in the editors/zed submodule; ensure it's present.
if [ ! -s "$(readlink -f "$root/treesitter/queries/highlights.scm" 2>/dev/null || true)" ]; then
  ( cd "$root" && git submodule update --init editors/zed )
fi
cp "$(readlink -f "$root/treesitter/queries/highlights.scm")" js/dang-highlights.scm

echo "==> web-tree-sitter@$WTS_VERSION runtime (no npm needed)"
tmp="$(mktemp -d)"
curl -fsSL "https://registry.npmjs.org/web-tree-sitter/-/web-tree-sitter-${WTS_VERSION}.tgz" | tar -xz -C "$tmp"
cp "$tmp/package/tree-sitter.js" js/tree-sitter.js
cp "$tmp/package/tree-sitter.wasm" js/tree-sitter.wasm
rm -rf "$tmp"

echo "==> highlight assets ready"
