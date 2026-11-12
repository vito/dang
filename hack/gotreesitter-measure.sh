#!/bin/sh
# Reproduce the gotreesitter spike's measurements: binary sizes for each
# tree-sitter configuration, CST parity with the C runtime, and parse speed.
#
# Needs Go and a C compiler (for the cgo builds), e.g.:
#
#   docker run --rm -v "$PWD:/src" -w /src golang:1.26 sh hack/gotreesitter-measure.sh
set -eu

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

size() { stat -c %s "$1" 2>/dev/null || stat -f %z "$1"; }

build() { # name, CGO_ENABLED, tags, package
  CGO_ENABLED=$2 go build -tags "$3" -ldflags='-s -w' -o "$out/$1" "$4"
  printf '%-44s %12s\n' "$1" "$(size "$out/$1")"
}

echo "== stripped binary sizes (bytes)"
for pkg in dang:./cmd/dang sizeprobe:./hack/sizeprobe; do
  name=${pkg%%:*} path=${pkg#*:}
  build "$name cgo (C tree-sitter)" 1 "" "$path"
  build "$name cgo + dang_gotreesitter" 1 dang_gotreesitter "$path"
  build "$name nocgo (gotreesitter)" 0 "" "$path"
  build "$name nocgo + gts_no_parsercorephase0" 0 gts_no_parsercorephase0 "$path"
done
printf '%-44s %12s\n' "gotreesitter blob (pkg/dang/tsgo/dang.bin)" "$(size pkg/dang/tsgo/dang.bin)"
printf '%-44s %12s\n' "treesitter/src/parser.c" "$(size treesitter/src/parser.c)"
echo "(nocgo before this branch: build the parent commit with CGO_ENABLED=0)"

echo
echo "== CST parity with the C runtime"
go test -run TestParityWithC -v ./pkg/dang/tsgo | grep 'parse identically'

echo
echo "== parse speed over the repository's .dang files and tree-sitter corpus"
go test -run '^$' -bench . -benchtime 3s ./pkg/dang/tsgo | grep Benchmark
