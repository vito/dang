package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/charmbracelet/fang"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/spf13/cobra"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/ioctx"
	"github.com/vito/dang/pkg/lsp"
)

// Config holds the application configuration
type Config struct {
	Debug      bool
	ClearCache bool
	File       string
	LSP        bool
	LSPLogFile string
}

func main() {
	var cfg Config

	// Create the root command
	rootCmd := &cobra.Command{
		Use:   "dang [flags] [file|directory]",
		Short: "Dang language interpreter",
		Long: `Dang is a functional language for building Dagger pipelines.
It provides type-safe, composable abstractions for container operations.`,
		Example: `  # Run a Dang script
  dang script.dang

  # Run all .dang files in a directory as a module
  dang ./my-module

  # Start interactive REPL
  dang

  # Run with debug logging enabled
  dang --debug script.dang
  dang -d ./my-module`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Handle LSP mode first
			if cfg.LSP {
				return runLSP(cmd.Context(), cfg)
			}

			// Handle cache clearing first
			if cfg.ClearCache {
				if err := dang.ClearSchemaCache(); err != nil {
					return fmt.Errorf("failed to clear cache: %w", err)
				}
				fmt.Println("Schema cache cleared successfully")
				return nil
			}

			if len(args) == 1 {
				cfg.File = args[0]
				return run(cmd.Context(), cfg)
			} else {
				return runREPL(cmd.Context(), cfg)
			}
		},
	}

	// Add flags
	rootCmd.Flags().BoolVarP(&cfg.Debug, "debug", "d", false, "Enable debug logging")
	rootCmd.Flags().BoolVar(&cfg.ClearCache, "clear-cache", false, "Clear GraphQL schema cache and exit")
	rootCmd.Flags().BoolVar(&cfg.LSP, "lsp", false, "Run in Language Server Protocol mode")
	rootCmd.Flags().StringVar(&cfg.LSPLogFile, "lsp-log-file", "", "Path to LSP log file (stderr if not specified)")

	// Add fmt subcommand
	rootCmd.AddCommand(fmtCmd())

	// Use fang for styled execution with enhanced features
	ctx := context.Background()
	ctx = ioctx.StdoutToContext(ctx, os.Stdout)
	ctx = ioctx.StderrToContext(ctx, os.Stderr)
	if err := fang.Execute(ctx, rootCmd,
		fang.WithVersion("v0.1.0"),
		fang.WithCommit("dev"),
		fang.WithErrorHandler(func(w io.Writer, styles fang.Styles, err error) {
			_, _ = fmt.Fprintln(w, err.Error())
		}),
	); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg Config) error {
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

	// Check if the path is a directory or file
	fileInfo, err := os.Stat(cfg.File)
	if err != nil {
		return fmt.Errorf("failed to access path %s: %w", cfg.File, err)
	}

	if fileInfo.IsDir() {
		// Evaluate directory as a module
		if _, err := dang.RunDir(ctx, cfg.File, cfg.Debug); err != nil {
			return err
		}
	} else {
		// Evaluate single file
		if err := dang.RunFile(ctx, cfg.File, cfg.Debug); err != nil {
			return err
		}
	}

	return nil
}

func runREPL(ctx context.Context, cfg Config) error {
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

	// Set up service registry for service-based imports
	services := &dang.ServiceRegistry{}
	defer services.StopAll()
	ctx = dang.ContextWithServices(ctx, services)

	// Find and resolve dang.toml imports
	cwd, _ := os.Getwd()
	var importConfigs []dang.ImportConfig
	configPath, config, err := dang.FindProjectConfig(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to find dang.toml: %v\n", err)
	} else if config != nil {
		configDir := filepath.Dir(configPath)
		ctx = dang.ContextWithProjectConfig(ctx, configPath, config)
		resolved, err := dang.ResolveImportConfigs(ctx, config, configDir)
		if err != nil {
			return fmt.Errorf("failed to resolve imports from %s: %w", configPath, err)
		}
		importConfigs = resolved
	}

	// Detect dagger.json and load the module + deps
	var daggerConn *dagger.Client
	moduleDir := findDaggerModule(cwd)
	if moduleDir != "" {
		fmt.Fprintf(os.Stderr, "Loading Dagger module from %s...\n", moduleDir)

		dag, err := dagger.Connect(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to connect to Dagger: %v\n", err)
		} else {
			daggerConn = dag

			provider := dang.NewGraphQLClientProvider(dang.GraphQLConfig{})
			client, schema, err := provider.GetDaggerModuleSchema(ctx, dag, moduleDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to load Dagger module schema: %v\n", err)
			} else {
				importConfigs = append(importConfigs, dang.ImportConfig{
					Name:       "Dagger",
					Client:     client,
					Schema:     schema,
					AutoImport: true,
				})
			}
		}
	}
	if daggerConn != nil {
		defer daggerConn.Close()
	}

	if len(importConfigs) > 0 {
		ctx = dang.ContextWithImportConfigs(ctx, importConfigs...)
	}

	return runREPLBubbletea(ctx, importConfigs, cfg.Debug)
}

// findDaggerModule searches for a dagger.json starting from dir, walking up.
func findDaggerModule(startPath string) string {
	dir, err := filepath.Abs(startPath)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "dagger.json")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return "" // stop at repo boundary
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func runLSP(ctx context.Context, cfg Config) error {
	// Set up logging
	var logDest io.Writer
	if cfg.LSPLogFile != "" {
		logFile, err := os.Create(cfg.LSPLogFile)
		if err != nil {
			return fmt.Errorf("open lsp log: %w", err)
		}
		defer logFile.Close() //nolint:errcheck
		logDest = logFile
	} else {
		logDest = os.Stderr
	}

	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}

	logHandler := slog.NewTextHandler(logDest, &slog.HandlerOptions{
		Level: level,
	})
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	logger.InfoContext(ctx, "starting LSP server")

	handler := lsp.NewHandler(ctx)
	srv := jrpc2.NewServer(handler, &jrpc2.ServerOptions{
		AllowPush: true,
		Logger:    func(text string) { logger.Debug(text) },
	})

	// Store server reference in handler for callbacks
	handler.SetServer(srv)

	// Start handling requests
	srv.Start(channel.LSP(stdrwc{}, stdrwc{}))

	logger.InfoContext(ctx, "LSP server closed", "error", srv.Wait())
	return nil
}

type stdrwc struct{}

func (stdrwc) Read(p []byte) (int, error) {
	return os.Stdin.Read(p)
}

func (stdrwc) Write(p []byte) (int, error) {
	return os.Stdout.Write(p)
}

func (stdrwc) Close() error {
	if err := os.Stdin.Close(); err != nil {
		return err
	}
	return os.Stdout.Close()
}

func fmtCmd() *cobra.Command {
	var (
		write bool
		list  bool
	)

	cmd := &cobra.Command{
		Use:   "fmt [flags] [path...]",
		Short: "Format Dang source files",
		Long: `Format Dang source files according to the canonical style.

By default, fmt prints the formatted source to stdout.
Use -w to write the result back to the source file.
Use -l to list files that would be changed.`,
		Example: `  # Format a file and print to stdout
  dang fmt script.dang

  # Format a file in place
  dang fmt -w script.dang

  # Format all .dang files in a directory
  dang fmt -w ./my-module

  # List files that need formatting
  dang fmt -l ./my-module`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFmt(args, write, list)
		},
	}

	cmd.Flags().BoolVarP(&write, "write", "w", false, "Write result to source file instead of stdout")
	cmd.Flags().BoolVarP(&list, "list", "l", false, "List files that would be formatted")

	return cmd
}

func runFmt(paths []string, write, list bool) error {
	var files []string

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("accessing %s: %w", path, err)
		}

		if info.IsDir() {
			// Find all .dang files in directory
			entries, err := os.ReadDir(path)
			if err != nil {
				return fmt.Errorf("reading directory %s: %w", path, err)
			}
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".dang") {
					files = append(files, path+"/"+entry.Name())
				}
			}
		} else {
			files = append(files, path)
		}
	}

	for _, file := range files {
		if err := formatFile(file, write, list); err != nil {
			return fmt.Errorf("formatting %s: %w", file, err)
		}
	}

	return nil
}

func formatFile(path string, write, list bool) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	formatted, err := dang.FormatFile(source)
	if err != nil {
		return err
	}

	// Check if file changed
	changed := string(source) != formatted

	if list && !write {
		if changed {
			fmt.Println(path)
		}
		return nil
	}

	if write {
		if changed {
			if err := os.WriteFile(path, []byte(formatted), 0644); err != nil {
				return err
			}
			if list {
				fmt.Println(path)
			}
		}
		return nil
	}

	// Print to stdout
	fmt.Print(formatted)
	return nil
}
