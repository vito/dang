package dang

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vito/dang/v2/pkg/modref"
)

// fetchDangModuleRef resolves a Dagger-style module ref
// (host/user/repo[/subpath]@version) to a local directory, shallow-fetching the
// repository into a content-addressed module cache on a miss. The returned path
// is the module directory (the repo root joined with the ref's subpath), ready
// to hand to loadDangModule exactly like a local `path` import. Everything
// downstream — the per-module cache, the compile-stack cycle detection, the
// cross-module visibility boundary — already works from a directory, so remote
// imports reuse the entire Phase 1 loader without change.
//
// Ref parsing is delegated to the extracted modref package (Dagger's exact
// syntax + host resolution), so github.com and the other statically-known hosts
// resolve without any network round trip; only the fetch touches the network.
func fetchDangModuleRef(ctx context.Context, ref string) (string, error) {
	parsed, err := modref.Parse(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("parse module ref %q: %w", ref, err)
	}
	if parsed.RepoRoot == nil {
		return "", fmt.Errorf("module ref %q did not resolve to a git repository", ref)
	}
	repoDir, err := fetchGitRepo(ctx, parsed.RepoRoot.Repo, parsed.RepoRoot.Root, parsed.ModVersion)
	if err != nil {
		return "", fmt.Errorf("fetch module ref %q: %w", ref, err)
	}
	// RepoRootSubdir is "/" for a whole-repo ref; filepath.Join cleans that back
	// to repoDir. A nested subpath ("ci", "lib/x") lands under the checkout.
	return filepath.Join(repoDir, filepath.FromSlash(parsed.RepoRootSubdir)), nil
}

// shaRE recognises a raw commit SHA (abbreviated or full). A version matching
// it needs no ls-remote to resolve, which is what lets a SHA-pinned ref hit the
// cache fully offline; a tag or branch still costs one ls-remote until dang.lock
// records the resolved commit.
var shaRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// fetchGitRepo returns the local checkout directory for repoURL at version,
// fetching it on a cache miss. modPath (the repo root, e.g.
// "github.com/vito/dang-semver") plus the resolved commit form the content-
// addressed cache key, so a moved tag resolves to a different dir and two refs
// pinning the same commit share one checkout on disk.
func fetchGitRepo(ctx context.Context, repoURL, modPath, version string) (string, error) {
	sha, fetchRef, err := resolveCommit(ctx, repoURL, version)
	if err != nil {
		return "", err
	}

	cacheDir := filepath.Join(moduleCacheDir(), filepath.FromSlash(modPath)+"@"+sha)
	// The cache dir is immutable once written and content-addressed to the
	// commit, so its mere presence means a complete checkout — no fetch needed.
	if fi, statErr := os.Stat(cacheDir); statErr == nil && fi.IsDir() {
		return cacheDir, nil
	}

	if err := gitShallowCheckout(ctx, repoURL, fetchRef, sha, cacheDir); err != nil {
		return "", err
	}
	return cacheDir, nil
}

// resolveCommit turns a ref's version into a concrete commit SHA plus the ref to
// fetch. A raw commit SHA resolves offline; a tag or branch — or an empty
// version, meaning the default branch — is resolved with a single `git
// ls-remote`. Annotated tags are dereferenced to the commit they point at (the
// peeled "^{}" entry), so the cache key is always a commit, never a tag object.
func resolveCommit(ctx context.Context, repoURL, version string) (sha, fetchRef string, err error) {
	if version != "" && shaRE.MatchString(version) {
		return version, version, nil
	}

	ref := version
	if ref == "" {
		ref = "HEAD"
	}
	out, err := runGit(ctx, "", "ls-remote", repoURL, ref)
	if err != nil {
		return "", "", err
	}
	sha = pickCommit(string(out))
	if sha == "" {
		return "", "", fmt.Errorf("could not resolve %q in %s", ref, repoURL)
	}
	return sha, ref, nil
}

// pickCommit extracts the commit SHA from `git ls-remote` output, preferring a
// peeled annotated-tag entry ("<sha>\trefs/tags/x^{}") over the tag object
// itself so the result is the underlying commit.
func pickCommit(out string) string {
	var first, peeled string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sha, name := fields[0], fields[1]
		if strings.HasSuffix(name, "^{}") {
			peeled = sha
		} else if first == "" {
			first = sha
		}
	}
	if peeled != "" {
		return peeled
	}
	return first
}

// gitShallowCheckout shallow-fetches fetchRef from repoURL and checks it out
// into dir. It builds the checkout in a sibling temp dir and atomically renames
// it into place, so a concurrent fetch of the same commit (another import in
// this process, or another dang invocation) either wins the rename or discards
// its copy — the cache dir only ever becomes visible fully populated. When the
// ref resolved to a specific commit, wantSHA is verified after checkout.
func gitShallowCheckout(ctx context.Context, repoURL, fetchRef, wantSHA, dir string) (rerr error) {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".fetch-*")
	if err != nil {
		return err
	}
	// On success the checkout is renamed out of tmp, so this cleans up only on
	// error (or the leftover empty parent after a successful rename: gone).
	defer os.RemoveAll(tmp)

	for _, args := range [][]string{
		{"init", "--quiet"},
		{"remote", "add", "origin", repoURL},
		{"fetch", "--quiet", "--depth", "1", "origin", fetchRef},
		{"checkout", "--quiet", "--detach", "FETCH_HEAD"},
	} {
		if _, err := runGit(ctx, tmp, args...); err != nil {
			return err
		}
	}

	if wantSHA != "" {
		out, err := runGit(ctx, tmp, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		got := strings.TrimSpace(string(out))
		// wantSHA may be abbreviated (a short SHA in the ref); accept either as a
		// prefix of the other.
		if !strings.HasPrefix(got, wantSHA) && !strings.HasPrefix(wantSHA, got) {
			return fmt.Errorf("fetched commit %s does not match resolved %s", got, wantSHA)
		}
	}

	if err := os.Rename(tmp, dir); err != nil {
		// A concurrent fetch may have populated the cache dir first. It is
		// content-addressed to the same commit, so that checkout is equivalent.
		if fi, statErr := os.Stat(dir); statErr == nil && fi.IsDir() {
			return nil
		}
		return err
	}
	return nil
}

// runGit runs git in dir (cwd when empty) and returns its stdout. Credential
// prompting is disabled so a private or nonexistent repo fails fast with the
// git error rather than blocking on a terminal that a compile has no business
// waiting on.
func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// moduleCacheDir is the root of the content-addressed module cache. Override
// with $DANG_MODCACHE; it otherwise lives under the user cache dir (XDG on
// Linux, e.g. ~/.cache/dang/modcache).
func moduleCacheDir() string {
	if dir := os.Getenv("DANG_MODCACHE"); dir != "" {
		return dir
	}
	if cache, err := os.UserCacheDir(); err == nil {
		return filepath.Join(cache, "dang", "modcache")
	}
	return filepath.Join(os.TempDir(), "dang-modcache")
}
