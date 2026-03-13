package main

import (
	"context"
	"fmt"
	"path"
	"runtime"

	"dagger/dang/internal/dagger"
)

const Golang = "golang:1.25"

type DangSdk struct {
	// +private
	Source *dagger.Directory
}

func New(
	// +defaultPath="/"
	// +ignore=[
	//   "*",
	//   "!**/go.mod",
	//   "!**/go.sum",
	//   "!pkg",
	//   "!dagger-sdk",
	//   "!treesitter"
	// ]
	source *dagger.Directory,
) *DangSdk {
	return &DangSdk{
		Source: source,
	}
}

const (
	ModSourceDirPath = "/mod"
)

// ModuleRuntime returns a container with the node entrypoint ready to be called.
func (t *DangSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJson *dagger.File,
) (*dagger.Container, error) {
	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %v", err)
	}

	modSrcDir := path.Join(ModSourceDirPath, subPath)

	return t.Base().
		WithMountedDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		WithFile("/introspection.json", introspectionJson).
		WithWorkdir(modSrcDir).
		WithDefaultArgs([]string{"/dang", modSrcDir}), nil
}

func (t *DangSdk) Base() *dagger.Container {
	return dag.Container().
		WithFile("/dang", t.Entrypoint())
}

func (t *DangSdk) Entrypoint() *dagger.File {
	return t.goBase().
		WithExec([]string{"go", "build", "-o", "/entrypoint", "./entrypoint"}).
		File("/entrypoint")
}

func (t *DangSdk) Repl() *dagger.Container {
	return t.Base().
		WithDefaultTerminalCmd([]string{"/dang"}).
		WithMountedCache("/xdg/home", dag.CacheVolume("dang-repl-home")).
		WithEnvVariable("XDG_DATA_HOME", "/xdg/home").
		Terminal(dagger.ContainerTerminalOpts{
			Cmd:                           []string{"/dang"},
			ExperimentalPrivilegedNesting: true,
		})
}

func zigArch() string {
	if runtime.GOARCH == "arm64" {
		return "aarch64"
	}
	return "x86_64"
}

func zigTarget() string {
	return zigArch() + "-linux-musl"
}

func zigTarballName() string {
	return fmt.Sprintf("zig-%s-linux-0.16.0-dev.2670+56253d9e3.tar.xz", zigArch())
}

const zigMinisignPubKey = "RWSGOq2NVecA2UPNdBUZykf1CCb147pkmdtYxgb3Ti+JO/wCYvhbAb/U"

// fetchZig downloads a Zig tarball from community mirrors with minisign
// signature verification. It shuffles the mirror list and tries each one in
// turn, verifying the tarball signature and checking that the trusted comment
// references the expected tarball name (to prevent downgrade attacks).
func fetchZig() *dagger.Directory {
	tarballName := zigTarballName()

	script := fmt.Sprintf(`#!/bin/sh
set -eu

TARBALL_NAME="%s"
PUBKEY="%s"

# Fetch community mirror list
MIRRORS=$(wget -qO- "https://ziglang.org/download/community-mirrors.txt")

# Shuffle mirrors (shuf is provided by coreutils)
MIRRORS=$(printf '%%s\n' $MIRRORS | shuf)

for MIRROR_URL in $MIRRORS; do
  echo "Trying mirror: $MIRROR_URL"

  # Download tarball
  if ! wget -q -O "/tmp/$TARBALL_NAME" "$MIRROR_URL/$TARBALL_NAME?source=dang-dagger-sdk"; then
    echo "  Failed to download tarball"
    rm -f "/tmp/$TARBALL_NAME"
    continue
  fi

  # Download signature — NEVER SKIP THIS STEP
  if ! wget -q -O "/tmp/$TARBALL_NAME.minisig" "$MIRROR_URL/$TARBALL_NAME.minisig?source=dang-dagger-sdk"; then
    echo "  Failed to download signature"
    rm -f "/tmp/$TARBALL_NAME" "/tmp/$TARBALL_NAME.minisig"
    continue
  fi

  # Verify the signature against the public key — NEVER SKIP THIS STEP
  if ! minisign -Vm "/tmp/$TARBALL_NAME" -P "$PUBKEY" -x "/tmp/$TARBALL_NAME.minisig"; then
    echo "  Signature verification failed"
    rm -f "/tmp/$TARBALL_NAME" "/tmp/$TARBALL_NAME.minisig"
    continue
  fi

  # Verify the tarball name in the trusted comment to prevent downgrade attacks.
  # The ZSF minisign signatures include a "file:" field in the trusted comment
  # that must match the requested tarball name.
  TRUSTED_COMMENT=$(grep '^trusted comment:' "/tmp/$TARBALL_NAME.minisig")
  case "$TRUSTED_COMMENT" in
    *"file:$TARBALL_NAME"*) ;;
    *)
      echo "  Tarball name mismatch in trusted comment: $TRUSTED_COMMENT"
      rm -f "/tmp/$TARBALL_NAME" "/tmp/$TARBALL_NAME.minisig"
      continue
      ;;
  esac

  echo "Successfully fetched and verified $TARBALL_NAME from $MIRROR_URL"
  tar -xJf "/tmp/$TARBALL_NAME" -C /usr/local
  mv /usr/local/zig-*-linux-* /usr/local/zig
  rm -f "/tmp/$TARBALL_NAME" "/tmp/$TARBALL_NAME.minisig"
  exit 0
done

echo "ERROR: Failed to fetch Zig from any mirror"
exit 1
`, tarballName, zigMinisignPubKey)

	return dag.Container().From("alpine:latest").
		WithExec([]string{"apk", "add", "--no-cache", "minisign", "xz", "coreutils"}).
		WithNewFile("/fetch-zig.sh", script, dagger.ContainerWithNewFileOpts{Permissions: 0o755}).
		WithExec([]string{"/fetch-zig.sh"}).
		Directory("/usr/local/zig")
}

func (t *DangSdk) goBase() *dagger.Container {
	zig := fetchZig()

	target := zigTarget()
	return dag.Container().From(Golang).
		WithDirectory("/usr/local/zig", zig).
		WithEnvVariable("PATH", "/usr/local/zig:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin").
		WithEnvVariable("CC", fmt.Sprintf("zig cc -target %s -lc", target)).
		WithEnvVariable("CXX", fmt.Sprintf("zig c++ -target %s -lc", target)).
		WithDirectory("/src", t.Source).
		WithDirectory("/src/dagger-sdk", dag.CurrentModule().Source()).
		WithWorkdir("/src/dagger-sdk").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", dag.CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache")
}
