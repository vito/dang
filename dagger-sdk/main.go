package main

import (
	"context"
	"fmt"
	"path"

	"dagger/dang/internal/dagger"
)

const Golang = "golang:1.24"

type DangSdk struct {
	DangRoot *dagger.Directory
}

const (
	ModSourceDirPath         = "/src"
	EntrypointExecutableFile = "/dang"
	EntrypointExecutablePath = "src/" + EntrypointExecutableFile
	codegenBinPath           = "/codegen"
)

func New(
	// +defaultPath="/"
	dangRoot *dagger.Directory,
) *DangSdk {
	return &DangSdk{
		DangRoot: dangRoot,
	}
}

// ModuleRuntime returns a container with the node entrypoint ready to be called.
func (t *DangSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJson *dagger.File,
) (*dagger.Container, error) {
	return t.CodegenBase(ctx, modSource, introspectionJson)
}

// Codegen returns the generated API client based on user's module
func (t *DangSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJson *dagger.File,
) (*dagger.GeneratedCode, error) {
	ctr, err := t.CodegenBase(ctx, modSource, introspectionJson)
	if err != nil {
		return nil, err
	}

	return dag.GeneratedCode(ctr.Directory(ModSourceDirPath)).
		WithVCSGeneratedPaths([]string{}).
		WithVCSIgnoredPaths([]string{}), nil
}

func (t *DangSdk) CodegenBase(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJson *dagger.File,
) (*dagger.Container, error) {
	modName, err := modSource.ModuleOriginalName(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %v", err)
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %v", err)
	}

	modSrcDir := path.Join(ModSourceDirPath, subPath)

	return t.Base().
		WithMountedDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		WithFile("/introspection.json", introspectionJson).
		WithWorkdir(modSrcDir).
		WithEntrypoint([]string{"/dang", modSrcDir, modName}), nil
}

func (t *DangSdk) Base() *dagger.Container {
	return dag.Container().
		From("busybox").
		WithFile("/dang", t.Entrypoint()).
		WithEntrypoint([]string{"/dang"})
}

func (t *DangSdk) Entrypoint() *dagger.File {
	return t.goBase().
		WithWorkdir("./entrypoint").
		WithDirectory("/dang", t.DangRoot).
		WithExec([]string{"go", "mod", "edit", "-replace", "github.com/vito/dang=/dang"}).
		WithExec([]string{"go", "build", "-o", "/entrypoint"}).
		File("/entrypoint")
}

func (t *DangSdk) Repl() *dagger.Container {
	return t.Base().
		WithDefaultTerminalCmd([]string{"/dang"}).
		WithMountedCache("/xdg/home", dag.CacheVolume("dang-repl-home")).
		WithEnvVariable("XDG_DATA_HOME", "/xdg/home").
		Terminal(dagger.ContainerTerminalOpts{
			ExperimentalPrivilegedNesting: true,
		})
}

func (t *DangSdk) goBase() *dagger.Container {
	return dag.Container().From(Golang).
		WithEnvVariable("CGO_ENABLED", "0").
		WithDirectory("/src", dag.CurrentModule().Source()).
		WithWorkdir("/src").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", dag.CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache")
}
