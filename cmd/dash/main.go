package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/vito/dash/pkg/dash"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "enable debug logging")
	flag.Parse()

	// Set up slog with appropriate level
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	ctx := context.Background()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [--debug] <dash-file>\n", os.Args[0])
		os.Exit(1)
	}

	dag, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer dag.Close()

	schema, err := Introspect(ctx, dag)
	if err != nil {
		panic(err)
	}

	if err := dash.CheckFile(schema, dag, args[0]); err != nil {
		panic(err)
	}

	fmt.Println("ok!")
}

func Introspect(ctx context.Context, dag *dagger.Client) (*introspection.Schema, error) {
	var introspectionResp introspection.Response
	err := dag.Do(ctx, &dagger.Request{
		Query:  introspection.Query,
		OpName: "IntrospectionQuery",
	}, &dagger.Response{
		Data: &introspectionResp,
	})
	if err != nil {
		return nil, fmt.Errorf("introspection query: %w", err)
	}

	return introspectionResp.Schema, nil
}
