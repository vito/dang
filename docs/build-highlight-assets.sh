#!/bin/bash
# Prepares the playground editor's syntax-highlighting assets in docs/js/.
#
# Assets involved:
#   - tree-sitter-dang.wasm  the Dang grammar, COMPILED FROM treesitter/.
#   - tree-sitter-{toml,yaml,ruby,bash,go}.wasm  embedded-language grammars for
#                            injected code blocks (```toml … ``` inside a Dang
#                            string).
#     Building any grammar wasm needs the tree-sitter CLI, which can't run on
#     the Cloudflare Pages build image (its prebuilt binaries require a newer
#     glibc). So these are NOT committed: they're built once with --grammar-only
#     (which the publishGrammars Dagger function runs), uploaded to an R2
#     bucket, and fetched back here at build time with --runtime-only (curl).
#   - dang-highlights.scm    the highlight query (lives in the editors/zed
#                            submodule); fetched here, gitignored.
#   - dang-injections.scm    the injection query (same submodule); fetched here.
#   - {lang}-highlights.scm  embedded-language queries copied from docs/go/queries.
#   - tree-sitter.js/.wasm   the web-tree-sitter runtime; fetched here via curl,
#                            gitignored.
#
# Modes:
#   --grammar-only  build the grammar wasms only (needs the tree-sitter CLI).
#                   Run by the publishGrammars Dagger function before it uploads
#                   them to R2, and by hand for local grammar development. Not
#                   run on the docs build.
#   --runtime-only  fetch everything the browser needs — queries, the
#                   web-tree-sitter runtime, and the grammar wasms from R2 (any
#                   not already built locally). Pure curl, run by build.sh on
#                   every build, so it works on the Cloudflare build image.
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

TS_VERSION="0.26.9"    # tree-sitter CLI (only used to build the grammar wasms)
WTS_VERSION="0.25.10"  # web-tree-sitter runtime; keep ABI-compatible with the CLI

# Public base URL of the R2 bucket the grammar wasms are published to (see the
# publishGrammars Dagger function). Override with DANG_GRAMMAR_R2_BASE.
R2_BASE="${DANG_GRAMMAR_R2_BASE:-https://pub-d343f2329f2c45c7a2b9d51700c39cdf.r2.dev}"

# Every grammar wasm: the Dang grammar plus the embedded languages.
GRAMMARS="dang toml yaml ruby bash go"

if $do_grammar; then
  # Build the Dang grammar wasm. Fetch a prebuilt tree-sitter CLI if one isn't
  # already on PATH.
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

  # Embedded-language grammars, for highlighting injected code blocks
  # (```toml … ``` inside a Dang string) in the playground editor. Built the
  # same way as the Dang grammar, for publishing to R2. Sourced from the docs Go
  # module's grammar deps (the module cache is read-only, so build from a copy).
  for pair in \
    "github.com/tree-sitter-grammars/tree-sitter-toml:toml" \
    "github.com/tree-sitter-grammars/tree-sitter-yaml:yaml" \
    "github.com/tree-sitter/tree-sitter-ruby:ruby" \
    "github.com/tree-sitter/tree-sitter-bash:bash" \
    "github.com/tree-sitter/tree-sitter-go:go"; do
    mod="${pair%%:*}"; lang="${pair##*:}"
    # Extract the grammar module into the cache if it isn't already: a fresh
    # build env (e.g. the publishGrammars container) only has it as a go.sum
    # entry, and `go list -m -f {{.Dir}}` reports an empty dir until it's
    # downloaded, which would silently skip the grammar.
    go mod download "$mod" 2>/dev/null || true
    dir="$(go list -m -f '{{.Dir}}' "$mod" 2>/dev/null || true)"
    if [ -z "$dir" ]; then echo "warning: $lang grammar not found; skipping" >&2; continue; fi
    work="$(mktemp -d)"; cp -r "$dir"/. "$work/"; chmod -R u+w "$work"
    echo "==> $lang grammar wasm"
    ( cd "$work" && "$ts" build --wasm -o "$root/docs/js/tree-sitter-$lang.wasm" . ) \
      || echo "warning: $lang wasm build failed" >&2
    chmod 644 "$root/docs/js/tree-sitter-$lang.wasm" 2>/dev/null || true
    rm -rf "$work"
  done
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

  echo "==> embedded-language highlight queries"
  # The same queries docs/go embeds (queries/*.scm), copied for the browser so
  # client-side injection colours match the build-time highlighting.
  for lang in bash toml go ruby yaml; do
    cp "$root/docs/go/queries/$lang.scm" "js/$lang-highlights.scm"
  done

  echo "==> web-tree-sitter@$WTS_VERSION runtime (no npm needed)"
  tmp="$(mktemp -d)"
  curl -fsSL "https://registry.npmjs.org/web-tree-sitter/-/web-tree-sitter-${WTS_VERSION}.tgz" | tar -xz -C "$tmp"
  cp "$tmp/package/tree-sitter.js" js/tree-sitter.js
  cp "$tmp/package/tree-sitter.wasm" js/tree-sitter.wasm
  rm -rf "$tmp"

  echo "==> grammar wasms (from R2)"
  # The grammar wasms are built by --grammar-only and published to R2 (they
  # can't be built on the Cloudflare image). Fetch any not already present — a
  # local --grammar-only build takes precedence, so a grammar change can be
  # previewed before it's published.
  for lang in $GRAMMARS; do
    out="js/tree-sitter-$lang.wasm"
    [ -s "$out" ] && continue
    curl -fsSL "$R2_BASE/grammars/tree-sitter-$lang.wasm" -o "$out" \
      || { rm -f "$out"; echo "warning: could not fetch $lang grammar wasm from $R2_BASE; $lang injection highlighting unavailable" >&2; }
  done
fi

echo "==> highlight assets ready"
