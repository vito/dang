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

# Fetch the editor's highlight query, the web-tree-sitter runtime, and the
# grammar wasms (from R2 — the build image can't run the tree-sitter CLI to
# build them, so publishGrammars uploads them to R2 and we fetch them here).
# Pure curl; best-effort: the editor just renders uncolored text if this fails.
chmod +x build-highlight-assets.sh
./build-highlight-assets.sh --runtime-only || echo "warning: highlight assets unavailable; playground editor will not be colored" >&2

# Generate the per-scheme CSS from the tinted-theming/schemes submodule
# (pinned at docs/schemes). The submodule is initialized here rather than
# assumed so that checkouts made without --recurse-submodules (and CI clones
# that skip submodules) still build.
if [ ! -d schemes/base16 ]; then
  git -C .. submodule update --init docs/schemes
fi
go run ./cmd/genthemes

# Regenerate the theme switcher's <option> list from the generated base16
# schemes. Committed (it only changes when schemes are added) so the booklit
# templates also work without this script.
{
  for scheme in css/base16/*.css; do
    name="$(basename "$scheme" .css)"
    name="${name#base16-}"
    echo "<option value=\"${name}\">${name}</option>"
  done
} > html/base16-options.tmpl

# CGO_ENABLED=1: code blocks are highlighted by tree-sitter grammars (Dang
# uses the same parser and highlight query as the editors and the playground;
# sh/toml fences use their upstream grammars), which bind the generated C
# parsers via cgo. The build image needs a C compiler.
CGO_ENABLED=1 go run . -i lit/index.md -o . --html-templates html --save-search-index "$@"
