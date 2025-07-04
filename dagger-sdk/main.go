package main

import (
	"context"
	"fmt"
	"path"

	"dagger/bind/internal/dagger"
)

const Golang = "golang:1.24"

type BindSdk struct {
	BindRoot *dagger.Directory
}

const (
	ModSourceDirPath         = "/src"
	EntrypointExecutableFile = "/bind"
	EntrypointExecutablePath = "src/" + EntrypointExecutableFile
	codegenBinPath           = "/codegen"
)

func New(
	// +defaultPath="/"
	bindRoot *dagger.Directory,
) *BindSdk {
	return &BindSdk{
		BindRoot: bindRoot,
	}
}

// ModuleRuntime returns a container with the node entrypoint ready to be called.
func (t *BindSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJson *dagger.File,
) (*dagger.Container, error) {
	return t.CodegenBase(ctx, modSource, introspectionJson)
}

// Codegen returns the generated API client based on user's module
func (t *BindSdk) Codegen(
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

func (t *BindSdk) CodegenBase(
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
		WithEntrypoint([]string{"/bind", modSrcDir, modName}), nil
}

func (t *BindSdk) Base() *dagger.Container {
	return dag.Container().
		From("busybox").
		WithFile("/bind", t.Entrypoint()).
		WithEntrypoint([]string{"/bind"})
}

func (t *BindSdk) Entrypoint() *dagger.File {
	return t.goBase().
		WithWorkdir("./entrypoint").
		WithDirectory("/bind", t.BindRoot).
		WithExec([]string{"go", "mod", "edit", "-replace", "github.com/vito/bind=/bind"}).
		WithExec([]string{"go", "build", "-o", "/entrypoint"}).
		File("/entrypoint")
}

func (t *BindSdk) Repl() *dagger.Container {
	return t.Base().
		WithDefaultTerminalCmd([]string{"/bind"}).
		WithMountedCache("/xdg/home", dag.CacheVolume("bind-repl-home")).
		WithEnvVariable("XDG_DATA_HOME", "/xdg/home").
		Terminal(dagger.ContainerTerminalOpts{
			ExperimentalPrivilegedNesting: true,
		})
}

func (t *BindSdk) goBase() *dagger.Container {
	return dag.Container().From(Golang).
		WithEnvVariable("CGO_ENABLED", "0").
		WithDirectory("/src", dag.CurrentModule().Source()).
		WithWorkdir("/src").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", dag.CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache")
}
