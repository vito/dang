package main

import (
	"context"
	"fmt"
	"path"
	"runtime"

	"dagger/dang/internal/dagger"
)

const (
	Alpine = "alpine:3.23"
	Golang = "go=~1.25"
	Zig    = "zig=~0.15"
)

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

func (t *DangSdk) goBase() *dagger.Container {
	target := zigTarget()
	return dag.Container().
		From(Alpine).
		WithExec([]string{"apk", "add", Golang, Zig}).
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

func zigArch() string {
	if runtime.GOARCH == "arm64" {
		return "aarch64"
	}
	return "x86_64"
}

func zigTarget() string {
	return zigArch() + "-linux-musl"
}
