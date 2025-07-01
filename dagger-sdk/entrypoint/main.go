package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"

	"dagger/dash/internal/dagger"
	"dagger/dash/internal/telemetry"

	"github.com/vito/dash/introspection"
	"github.com/vito/dash/pkg/dash"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
)

var dag = dagger.Connect()

const debug = false

const introspectionJSON = "/introspection.json"

func main() {
	ctx := context.Background()

	ctx = telemetry.InitEmbedded(ctx, resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String("dagger-dash-sdk"),
	))
	defer telemetry.Close()

	dagger.SetMarshalContext(ctx)

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

	// Introspect the GraphQL schema
	schema, err := Introspect(ctx, dag)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}

	// Check and evaluate the Dash file
	if err := dash.RunFile(dag.GraphQLClient(), schema, cfg.File, cfg.Debug); err != nil {
		// This is where the Dash evaluation will happen.
		// You will need to implement this part.
		// For now, we'll just log the error.
		slog.Error("failed to evaluate Dash file", "error", err)
		os.Exit(2)
	}

	fnCall := dag.CurrentFunctionCall()
	parentName, err := fnCall.ParentName(ctx)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}
	fnName, err := fnCall.Name(ctx)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}
	parentJson, err := fnCall.Parent(ctx)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}
	fnArgs, err := fnCall.InputArgs(ctx)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}

	inputArgs := map[string][]byte{}
	for _, fnArg := range fnArgs {
		argName, err := fnArg.Name(ctx)
		if err != nil {
			WriteError(ctx, err)
			os.Exit(2)
		}
		argValue, err := fnArg.Value(ctx)
		if err != nil {
			WriteError(ctx, err)
			os.Exit(2)
		}
		inputArgs[argName] = []byte(argValue)
	}

	slog.Debug("invoking", "parentName", parentName, "fnName", fnName, "inputArgs", inputArgs, "parentJson", parentJson)

	modSrcDir := os.Args[1]
	modName := os.Args[2]

	result, err := invoke(ctx, modSrcDir, modName, []byte(parentJson), parentName, fnName, inputArgs)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}

	slog.Debug("returning", "result", string(resultBytes))

	if err := fnCall.ReturnValue(ctx, dagger.JSON(resultBytes)); err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}
}

func Introspect(ctx context.Context, dag *dagger.Client) (*introspection.Schema, error) {
	introspectionJSONBytes, err := os.ReadFile(introspectionJSON)
	if err != nil {
		return nil, fmt.Errorf("introspection query: %w", err)
	}

	var schema introspection.Schema
	if err := json.Unmarshal(introspectionJSONBytes, &schema); err != nil {
		return nil, fmt.Errorf("failed to unmarshal introspection JSON: %w", err)
	}

	return &schema, nil
}

func invoke(ctx context.Context, modSrcDir string, modName string, parentJSON []byte, parentName string, fnName string, inputArgs map[string][]byte) (_ any, err error) {
	// This is where you will implement the logic to invoke the Dash function.
	// You will need to:
	// 1. Load the Dash module.
	// 2. Find the function to call.
	// 3. Convert the input arguments to Dash values.
	// 4. Call the function.
	// 5. Convert the return value to a Go value.
	// For now, we'll just return a placeholder value.
	return "hello from dash", nil
}

func WriteError(ctx context.Context, err error) {
	if err != nil {
		log.Println(err)
	}
}
