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
	// +ignore=["mod", ".dagger", ".claude", "bin"]
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
		WithDirectory("/dang", t.DangRoot).
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
	return dag.Container().From(Golang).
		WithEnvVariable("CGO_ENABLED", "0").
		WithDirectory("/src", dag.CurrentModule().Source()).
		WithWorkdir("/src").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", dag.CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache")
}
