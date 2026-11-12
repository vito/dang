#!/bin/sh
# Build the wasm probe: the C tree-sitter runtime (vendored by go-tree-sitter)
# plus treesitter/src compiled to wasm32-wasi with zig, embedded in a Go
# program that runs it with wazero. Run from the repository root with zig
# and Go installed (e.g. alpine:edge with `apk add zig go`):
#
#   sh hack/wasm-probe/build.sh && QUIET=1 ./hack/wasm-probe/wasm-probe $(find . -name '*.dang')
set -eu
cd "$(dirname "$0")"
ts=$(go -C ../.. mod download -json github.com/tree-sitter/go-tree-sitter | sed -n 's/.*"Dir": "\(.*\)".*/\1/p')
zig cc -target wasm32-wasi -Oz -s -mexec-model=reactor -DHAVE_ENDIAN_H \
  -I"$ts/include" -I"$ts/src" -I../../treesitter/src \
  "$ts/src/lib.c" ../../treesitter/src/parser.c ../../treesitter/src/scanner.c shim.c \
  -o dang.wasm
echo "dang.wasm: $(wc -c < dang.wasm) bytes, $(gzip -9 -c dang.wasm | wc -c) gzipped"
[ -f go.sum ] || go mod tidy
CGO_ENABLED=0 go build -tags wasmprobe -ldflags='-s -w' -o wasm-probe .
echo "wasm-probe: $(wc -c < wasm-probe) bytes"
