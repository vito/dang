package dang

//go:generate go run github.com/mna/pigeon -support-left-recursion -o dang.peg.go dang.peg

// Refresh the embedded highlight query (highlight_ts.go go:embeds it) from the
// canonical copy in the editors/zed submodule, symlinked into treesitter/queries.
// cp -L follows the symlink to capture the query's content. It's best-effort:
// if the submodule isn't checked out the symlink dangles, the copy is skipped,
// and the committed pkg/dang/highlights.scm stays as the fallback.
//go:generate sh -c "cp -L ../../treesitter/queries/highlights.scm highlights.scm 2>/dev/null || true"
