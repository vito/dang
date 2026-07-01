#!/bin/bash
# Prepares the playground editor's syntax-highlighting assets in docs/js/.
#
# Three assets are involved:
#   - tree-sitter-dang.wasm  the grammar, COMPILED FROM treesitter/. Building it
#                            needs the tree-sitter CLI, which can't run on the
#                            Cloudflare Pages build image (its prebuilt binaries
#                            require a newer glibc), so this file is committed.
#   - dang-highlights.scm    the highlight query (lives in the editors/zed
#                            submodule); fetched here, gitignored.
#   - tree-sitter.js/.wasm   the web-tree-sitter runtime; fetched here via curl,
#                            gitignored.
#
# Modes:
#   --grammar-only  rebuild the committed grammar wasm only (run by
#                   ./hack/generate, since it changes with the grammar).
#   --runtime-only  fetch the query + runtime only (run by build.sh on every
#                   build; pure curl, works anywhere).
#   (no argument)   both.
#
# Highlighting is best-effort: the editor degrades to plain text if any asset
# is missing, so failures here are non-fatal to the docs build.
set -euo pipefail
cd "$(dirname "$0")"
root="$(cd .. && pwd)"

do_grammar=true
do_runtime=true
case "${1:-}" in
  --grammar-only) do_runtime=false ;;
  --runtime-only) do_grammar=false ;;
  "") ;;
  *) echo "usage: ${0##*/} [--grammar-only|--runtime-only]" >&2; exit 2 ;;
esac

TS_VERSION="0.26.9"    # tree-sitter CLI (only used to regenerate the grammar wasm)
WTS_VERSION="0.25.10"  # web-tree-sitter runtime; keep ABI-compatible with the CLI

if $do_grammar; then
  # Rebuild the committed grammar wasm. Fetch a prebuilt tree-sitter CLI if one
  # isn't already on PATH.
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
  chmod 644 "$root/docs/js/tree-sitter-dang.wasm" # it's a served asset, not an executable
fi

if $do_runtime; then
  echo "==> highlight query"
  # highlights.scm lives in the editors/zed submodule. Use the checked-out copy
  # if present (local dev); otherwise fetch the exact pinned revision from
  # GitHub, so this works even when the build env doesn't init submodules.
  hl="$(readlink -f "$root/treesitter/queries/highlights.scm" 2>/dev/null || true)"
  if [ -n "$hl" ] && [ -s "$hl" ]; then
    cp "$hl" js/dang-highlights.scm
  else
    sha="$(git -C "$root" rev-parse HEAD:editors/zed)"
    curl -fsSL "https://raw.githubusercontent.com/vito/zed-dang/${sha}/languages/dang/highlights.scm" -o js/dang-highlights.scm
  fi

  echo "==> injection query"
  # injections.scm lives alongside highlights.scm; same checked-out-or-fetch
  # dance. Drives docs/go's embedded-language highlighting (```toml … ``` etc.).
  inj="$(readlink -f "$root/treesitter/queries/injections.scm" 2>/dev/null || true)"
  if [ -n "$inj" ] && [ -s "$inj" ]; then
    cp "$inj" js/dang-injections.scm
  else
    sha="$(git -C "$root" rev-parse HEAD:editors/zed)"
    curl -fsSL "https://raw.githubusercontent.com/vito/zed-dang/${sha}/languages/dang/injections.scm" -o js/dang-injections.scm
  fi

  echo "==> web-tree-sitter@$WTS_VERSION runtime (no npm needed)"
  tmp="$(mktemp -d)"
  curl -fsSL "https://registry.npmjs.org/web-tree-sitter/-/web-tree-sitter-${WTS_VERSION}.tgz" | tar -xz -C "$tmp"
  cp "$tmp/package/tree-sitter.js" js/tree-sitter.js
  cp "$tmp/package/tree-sitter.wasm" js/tree-sitter.wasm
  rm -rf "$tmp"
fi

echo "==> highlight assets ready"
