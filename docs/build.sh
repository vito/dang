#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"

# Build the WebAssembly playground module (cmd/dang-playground lives in the
# root module) and stage Go's wasm support shim alongside it. Both are
# build artifacts, ignored by git.
( cd .. && GOOS=js GOARCH=wasm go build -o docs/js/dang.wasm ./cmd/dang-playground )
wasm_exec="$(go env GOROOT)/lib/wasm/wasm_exec.js"
if [ ! -f "$wasm_exec" ]; then
  wasm_exec="$(go env GOROOT)/misc/wasm/wasm_exec.js" # older Go layout
fi
cp "$wasm_exec" js/wasm_exec.js

# Fetch the editor's highlight query + web-tree-sitter runtime (pure curl; the
# grammar wasm is committed since the build image can't run the tree-sitter
# CLI). Best-effort: the editor just renders uncolored text if this fails.
chmod +x build-highlight-assets.sh
./build-highlight-assets.sh --runtime-only || echo "warning: highlight assets unavailable; playground editor will not be colored" >&2

go run . -i lit/index.md -o . --html-templates html --save-search-index "$@"
