package main

import (
	"context"
	"fmt"
	"path"

	"dagger/dash/internal/dagger"
)

const Golang = "golang:1.24"

type DashSdk struct{}

const (
	ModSourceDirPath         = "/src"
	EntrypointExecutableFile = "/dash"
	EntrypointExecutablePath = "src/" + EntrypointExecutableFile
	codegenBinPath           = "/codegen"
)

// ModuleRuntime returns a container with the node entrypoint ready to be called.
func (t *DashSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJson string,
) (*dagger.Container, error) {
	return t.CodegenBase(ctx, modSource, introspectionJson)
}

// Codegen returns the generated API client based on user's module
func (t *DashSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJson string,
) (*dagger.GeneratedCode, error) {
	ctr, err := t.CodegenBase(ctx, modSource, introspectionJson)
	if err != nil {
		return nil, err
	}

	return dag.GeneratedCode(ctr.Directory(ModSourceDirPath)).
		WithVCSGeneratedPaths([]string{}).
		WithVCSIgnoredPaths([]string{}), nil
}

func (t *DashSdk) CodegenBase(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJson string,
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
		WithWorkdir(modSrcDir).
		WithEntrypoint([]string{"/dash", modSrcDir, modName}), nil
}

func (t *DashSdk) Base() *dagger.Container {
	return dag.Container().
		From("busybox").
		WithFile("/dash", t.Entrypoint()).
		WithEntrypoint([]string{"/dash"})
}

func (t *DashSdk) Entrypoint() *dagger.File {
	return t.goBase().
		WithExec([]string{"go", "build", "-o", "/dash", "./entrypoint"}).
		File("/dash")
}

func (t *DashSdk) Repl() *dagger.Container {
	return t.Base().
		WithDefaultTerminalCmd([]string{"/dash"}).
		WithMountedCache("/xdg/home", dag.CacheVolume("dash-repl-home")).
		WithEnvVariable("XDG_DATA_HOME", "/xdg/home").
		Terminal(dagger.ContainerTerminalOpts{
			ExperimentalPrivilegedNesting: true,
		})
}

func (t *DashSdk) goBase() *dagger.Container {
	return dag.Container().From(Golang).
		WithEnvVariable("CGO_ENABLED", "0").
		WithDirectory("/src", dag.CurrentModule().Source()).
		WithWorkdir("/src").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", dag.CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache")
}
