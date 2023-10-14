package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/vito/dash/pkg/dash"
)

func main() {
	ctx := context.Background()

	dag, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer dag.Close()

	schema, err := Introspect(ctx, dag)
	if err != nil {
		panic(err)
	}

	if err := dash.CheckFile(schema, os.Args[1]); err != nil {
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
