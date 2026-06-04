#!/bin/bash
# Regenerates the syntax-highlighting assets used by the front-page playground
# editor (docs/js/playground.js). These are committed (not built at deploy
# time) because the deploy environment only has Go, not the tree-sitter CLI.
# Run this whenever the grammar (treesitter/) or highlights.scm changes.
#
# Requires: tree-sitter CLI (>=0.25), npm.
set -euo pipefail
cd "$(dirname "$0")"
root="$(cd .. && pwd)"

WTS_VERSION="0.25.10" # keep in sync with the tree-sitter CLI's ABI

echo "==> building grammar wasm (tree-sitter build --wasm)"
( cd "$root/treesitter" && tree-sitter build --wasm -o "$root/docs/js/tree-sitter-dang.wasm" . )

echo "==> copying highlights query"
# highlights.scm lives in the editors/zed submodule; ensure it's checked out.
if [ ! -e "$root/treesitter/queries/highlights.scm" ] || [ ! -s "$(readlink -f "$root/treesitter/queries/highlights.scm")" ]; then
  ( cd "$root" && git submodule update --init editors/zed )
fi
cp "$(readlink -f "$root/treesitter/queries/highlights.scm")" js/dang-highlights.scm

echo "==> vendoring web-tree-sitter@$WTS_VERSION runtime"
tmp="$(mktemp -d)"
( cd "$tmp" && npm init -y >/dev/null && npm install "web-tree-sitter@$WTS_VERSION" >/dev/null )
cp "$tmp/node_modules/web-tree-sitter/tree-sitter.js" js/tree-sitter.js
cp "$tmp/node_modules/web-tree-sitter/tree-sitter.wasm" js/tree-sitter.wasm
rm -rf "$tmp"

echo "==> done. Updated:"
ls -lh js/tree-sitter.js js/tree-sitter.wasm js/tree-sitter-dang.wasm js/dang-highlights.scm
