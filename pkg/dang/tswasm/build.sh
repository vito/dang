#!/bin/sh
# Build pkg/dang/tswasm/dang.wasm: the C tree-sitter runtime (the one
# go-tree-sitter vendors, at the version pinned below), the Dang grammar's
# parser.c and scanner.c, and csrc/shim.c, compiled to wasm32-wasi with zig.
# (shim.c lives apart from the Go files: Go refuses C files in a package that
# doesn't use cgo.)
#
# Run from the repository root. The treesitter-wasm Dagger generator runs it
# with a pinned zig, so the output is reproducible; `dagger generate` is the
# usual way in. To run it by hand, set ZIG to a zig of the version pinned in
# .dagger/main.dang.
set -eu

zig=${ZIG:-zig}
runtime=github.com/tree-sitter/go-tree-sitter@v0.25.0
ts=$(go mod download -json "$runtime" | sed -n 's/.*"Dir": "\(.*\)".*/\1/p')

"$zig" cc -target wasm32-wasi -Oz -s -mexec-model=reactor \
  -DHAVE_ENDIAN_H -Wl,-z,stack-size=1048576 \
  -I"$ts/include" -I"$ts/src" -Itreesitter/src \
  "$ts/src/lib.c" treesitter/src/parser.c treesitter/src/scanner.c pkg/dang/tswasm/csrc/shim.c \
  -o pkg/dang/tswasm/dang.wasm
