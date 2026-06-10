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

# Render the syntax-highlight palettes to chroma.css. Highlighting emits chroma
# CSS classes (not inline styles), so this stylesheet supplies their colors and
# the light/dark theming. Build artifact, ignored by git.
CGO_ENABLED=0 go run ./gen-chroma-css chroma.css

# CGO_ENABLED=1: code blocks are tokenized by the Dang tree-sitter grammar
# (the same parser and highlight query the editors and the playground use),
# which binds the generated C parser via cgo. The build image needs a C
# compiler; chroma stays on purely as the HTML/CSS formatting layer.
CGO_ENABLED=1 go run . -i lit/index.md -o . --html-templates html --save-search-index "$@"
