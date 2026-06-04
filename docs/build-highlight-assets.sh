#!/bin/bash
# Generates the playground editor's syntax-highlighting assets into docs/js/.
# Called by build.sh (and runnable standalone). All outputs are gitignored.
#
# Cloudflare Pages' build image ships neither the tree-sitter CLI nor npm, so
# this script self-provisions: it downloads a pinned prebuilt tree-sitter
# binary when one isn't on PATH, and fetches the web-tree-sitter runtime with
# curl. The first `tree-sitter build --wasm` also downloads a wasi-sdk
# toolchain (~114MB), cached thereafter.
#
# The playground editor degrades to plain (uncolored) text if these assets are
# missing, so a failure here is non-fatal to the docs build.
set -euo pipefail
cd "$(dirname "$0")"
root="$(cd .. && pwd)"

TS_VERSION="0.26.9"    # tree-sitter CLI
WTS_VERSION="0.25.10"  # web-tree-sitter runtime; keep ABI-compatible with the CLI

# Ensure the tree-sitter CLI is available, fetching a prebuilt binary if not.
ts="$(command -v tree-sitter || true)"
if [ -z "$ts" ]; then
  case "$(uname -s)-$(uname -m)" in
    Linux-x86_64)  asset="tree-sitter-linux-x64.gz" ;;
    Linux-aarch64) asset="tree-sitter-linux-arm64.gz" ;;
    Darwin-arm64)  asset="tree-sitter-macos-arm64.gz" ;;
    Darwin-x86_64) asset="tree-sitter-macos-x64.gz" ;;
    *) echo "no prebuilt tree-sitter for $(uname -s)-$(uname -m)" >&2; exit 1 ;;
  esac
  echo "==> installing tree-sitter $TS_VERSION ($asset)"
  bindir="$(mktemp -d)"
  curl -fsSL "https://github.com/tree-sitter/tree-sitter/releases/download/v${TS_VERSION}/${asset}" | gunzip > "$bindir/tree-sitter"
  chmod +x "$bindir/tree-sitter"
  ts="$bindir/tree-sitter"
fi

echo "==> grammar wasm (tree-sitter build --wasm)"
( cd "$root/treesitter" && "$ts" build --wasm -o "$root/docs/js/tree-sitter-dang.wasm" . )

echo "==> highlight query"
# highlights.scm lives in the editors/zed submodule. Use the checked-out copy
# if present (local dev); otherwise fetch the exact pinned revision from GitHub,
# so this works even when the build env doesn't initialize submodules.
hl="$(readlink -f "$root/treesitter/queries/highlights.scm" 2>/dev/null || true)"
if [ -n "$hl" ] && [ -s "$hl" ]; then
  cp "$hl" js/dang-highlights.scm
else
  sha="$(git -C "$root" rev-parse HEAD:editors/zed)"
  curl -fsSL "https://raw.githubusercontent.com/vito/zed-dang/${sha}/languages/dang/highlights.scm" -o js/dang-highlights.scm
fi

echo "==> web-tree-sitter@$WTS_VERSION runtime (no npm needed)"
tmp="$(mktemp -d)"
curl -fsSL "https://registry.npmjs.org/web-tree-sitter/-/web-tree-sitter-${WTS_VERSION}.tgz" | tar -xz -C "$tmp"
cp "$tmp/package/tree-sitter.js" js/tree-sitter.js
cp "$tmp/package/tree-sitter.wasm" js/tree-sitter.wasm
rm -rf "$tmp"

echo "==> highlight assets ready"
