package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"dagger.io/dagger"
	"github.com/charmbracelet/fang"
	"github.com/dagger/dagger/codegen/introspection"
	"github.com/spf13/cobra"
	"github.com/vito/dash/pkg/dash"
)

// Config holds the application configuration
type Config struct {
	Debug bool
	File  string
}

func main() {
	var cfg Config

	// Create the root command
	rootCmd := &cobra.Command{
		Use:   "dash [flags] <file>",
		Short: "Dash language interpreter",
		Long: `Dash is a functional language for building Dagger pipelines.
It provides type-safe, composable abstractions for container operations.`,
		Example: `  # Run a Dash script
  dash script.dash

  # Run with debug logging enabled
  dash --debug script.dash
  dash -d script.dash`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.File = args[0]
			return run(cfg)
		},
	}

	// Add flags
	rootCmd.Flags().BoolVarP(&cfg.Debug, "debug", "d", false, "Enable debug logging")

	// Use fang for styled execution with enhanced features
	ctx := context.Background()
	if err := fang.Execute(ctx, rootCmd,
		fang.WithVersion("v0.1.0"),
		fang.WithCommit("dev"),
	); err != nil {
		os.Exit(1)
	}
}

func run(cfg Config) error {
	// Set up slog with appropriate level
	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	ctx := context.Background()

	// Connect to Dagger
	dag, err := dagger.Connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to Dagger: %w", err)
	}
	defer dag.Close()

	// Introspect the GraphQL schema
	schema, err := Introspect(ctx, dag)
	if err != nil {
		return fmt.Errorf("failed to introspect schema: %w", err)
	}

	// Check and evaluate the Dash file
	if err := dash.CheckFile(schema, dag, cfg.File); err != nil {
		return fmt.Errorf("failed to evaluate Dash file: %w", err)
	}

	fmt.Println("ok!")
	return nil
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