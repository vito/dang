package dang

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// initTestGitRepo creates a git repo at dir with a single commit containing the
// given files (relative path -> contents) and an optional tag, returning the
// commit SHA. Identity comes from env vars so the test needs no global git
// config.
func initTestGitRepo(t *testing.T, dir, tag string, files map[string]string) string {
	t.Helper()
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
		return out
	}
	require.NoError(t, os.MkdirAll(dir, 0o755))
	run("init", "-q", "-b", "main")
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	if tag != "" {
		run("tag", tag)
	}
	return strings.TrimSpace(string(run("rev-parse", "HEAD")))
}

// TestFetchGitRepo exercises resolve -> shallow-fetch -> content-addressed cache
// against a local file:// repository, so the whole fetch mechanism is covered
// hermetically (no network, no GitHub). modref.Parse of real github refs is
// covered separately by the extracted gitref tests.
func TestFetchGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()

	src := t.TempDir()
	sha := initTestGitRepo(t, src, "v1.0.0", map[string]string{
		"main.dang":       "pub let answer = 42\n",
		"lib/helper.dang": "pub let two = 2\n",
	})
	repoURL := "file://" + src

	cache := t.TempDir()
	t.Setenv("DANG_MODCACHE", cache)

	const modPath = "example.com/test/mod"

	// Fetch by tag: the tag resolves to its commit, checked out into a dir keyed
	// by that commit.
	dir, err := fetchGitRepo(ctx, repoURL, modPath, "v1.0.0")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(cache, "example.com", "test", "mod@"+sha), dir)

	// The whole repo is checked out, so both the root file and a subdir file land
	// on disk — which is what makes a subpath ref (filepath.Join(dir, subdir))
	// resolvable.
	body, err := os.ReadFile(filepath.Join(dir, "main.dang"))
	require.NoError(t, err)
	require.Equal(t, "pub let answer = 42\n", string(body))
	sub, err := os.ReadFile(filepath.Join(dir, "lib", "helper.dang"))
	require.NoError(t, err)
	require.Equal(t, "pub let two = 2\n", string(sub))

	// Second call is a cache hit returning the same dir.
	dir2, err := fetchGitRepo(ctx, repoURL, modPath, "v1.0.0")
	require.NoError(t, err)
	require.Equal(t, dir, dir2)

	// A commit-pinned ref resolves with no network at all: delete the source and
	// confirm the cached checkout is still returned.
	require.NoError(t, os.RemoveAll(src))
	dirOffline, err := fetchGitRepo(ctx, repoURL, modPath, sha)
	require.NoError(t, err, "cached commit should resolve offline")
	require.Equal(t, dir, dirOffline)
}

func TestPickCommit(t *testing.T) {
	// Lightweight tag or branch: a single line.
	require.Equal(t, "aaaaaaa", pickCommit("aaaaaaa\trefs/tags/v1\n"))
	// Annotated tag: prefer the peeled commit (^{}) over the tag object.
	require.Equal(t, "2222222",
		pickCommit("1111111\trefs/tags/v1\n2222222\trefs/tags/v1^{}\n"))
	// HEAD query.
	require.Equal(t, "deadbee", pickCommit("deadbee\tHEAD\n"))
	// No refs matched.
	require.Equal(t, "", pickCommit(""))
}

// TestResolveImportSourceDangModule covers the dang.toml `ref` surface: a ref
// records a lazily-resolved DangModuleRef with no schema/client/fetch, and is
// mutually exclusive with `path` and the GraphQL fields.
func TestResolveImportSourceDangModule(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	ic, err := resolveImportSource(ctx, "Semver",
		&ImportSource{Ref: "github.com/vito/dang-semver/semver@v1.4.0"}, dir)
	require.NoError(t, err)
	require.Equal(t, "github.com/vito/dang-semver/semver@v1.4.0", ic.DangModuleRef)
	require.Empty(t, ic.DangModuleDir)
	require.Nil(t, ic.Schema)
	require.Nil(t, ic.Client)

	_, err = resolveImportSource(ctx, "X",
		&ImportSource{Ref: "github.com/x/y", Path: "./z"}, dir)
	require.ErrorContains(t, err, "cannot both be set")

	_, err = resolveImportSource(ctx, "X",
		&ImportSource{Ref: "github.com/x/y", Dagger: true}, dir)
	require.ErrorContains(t, err, "cannot be combined")
}
