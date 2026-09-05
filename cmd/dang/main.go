package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/charmbracelet/fang"
	jsonrpc "github.com/gumeniukcom/golang-jsonrpc2/v2"
	"github.com/gumeniukcom/golang-jsonrpc2/v2/jsonrpcstdio"
	"github.com/spf13/cobra"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/ioctx"
	"github.com/vito/dang/v2/pkg/lsp"
)

// Config holds the application configuration
type Config struct {
	Debug      bool
	DebugAddr  string
	ClearCache bool
	File       string
	LSP        bool
	LSPLogFile string
	CPUProfile string
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
			// Start CPU profile if requested.
			if cfg.CPUProfile != "" {
				f, err := os.Create(cfg.CPUProfile)
				if err != nil {
					return fmt.Errorf("create CPU profile: %w", err)
				}
				defer f.Close() //nolint:errcheck
				if err := pprof.StartCPUProfile(f); err != nil {
					return fmt.Errorf("start CPU profile: %w", err)
				}
				defer pprof.StopCPUProfile()
			}

			// Start debug HTTP server if requested.
			if cfg.DebugAddr != "" {
				if err := setupDebugHandlers(cfg.DebugAddr); err != nil {
					return fmt.Errorf("setup debug handlers: %w", err)
				}
			}

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
	rootCmd.Flags().StringVar(&cfg.DebugAddr, "debug-addr", "", "Serve debug/pprof handlers on this address (e.g. localhost:6060)")
	rootCmd.Flags().BoolVar(&cfg.ClearCache, "clear-cache", false, "Clear GraphQL schema cache and exit")
	rootCmd.Flags().BoolVar(&cfg.LSP, "lsp", false, "Run in Language Server Protocol mode")
	rootCmd.Flags().StringVar(&cfg.LSPLogFile, "lsp-log-file", "", "Path to LSP log file (stderr if not specified)")
	rootCmd.Flags().StringVar(&cfg.CPUProfile, "cpuprofile", "", "Write CPU profile to file")

	// Add subcommands
	rootCmd.AddCommand(fmtCmd())

	// Use fang for styled execution with enhanced features
	ctx := context.Background()
	ctx = ioctx.StdoutToContext(ctx, os.Stdout)
	ctx = ioctx.StderrToContext(ctx, os.Stderr)
	version, commit := versionInfo()
	if err := fang.Execute(ctx, rootCmd,
		fang.WithVersion(version),
		fang.WithCommit(commit),
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

	cwd, _ := os.Getwd()
	moduleDir := dang.FindDaggerModule(cwd)

	return runREPLTUI(ctx, moduleDir, cfg.Debug)
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

	// Set up service registry for service-based imports
	services := &dang.ServiceRegistry{}
	defer services.StopAll()
	ctx = dang.ContextWithServices(ctx, services)

	handler := lsp.NewHandler(ctx)
	rpc := jsonrpc.New()
	// jrpc2 imposed no per-request deadline; the new library defaults to
	// 30s, which the first didOpen in a project (dagger session + schema
	// introspection) can exceed. Keep a generous bound instead of none.
	rpc.SetDefaultTimeOut(15 * time.Minute)
	// Handler errors are logged here (clients receive stable generic
	// codes; detail stays server-side). Full wire tracing, which jrpc2's
	// Logger option provided, is intentionally not reproduced.
	rpc.Use(func(method string, next jsonrpc.RPCMethod) jsonrpc.RPCMethod {
		return func(ctx context.Context, data json.RawMessage) (json.RawMessage, int, error) {
			res, code, err := next(ctx, data)
			if err != nil {
				logger.DebugContext(ctx, "jsonrpc handler error", "method", method, "code", code, "error", err)
			}
			return res, code, err
		}
	})
	if err := handler.Register(rpc); err != nil {
		return err
	}

	// Start handling requests over stdio with LSP Content-Length framing.
	// The transport's default dispatch is strictly sequential and in-order —
	// stronger than jrpc2's notification barrier (which only ordered
	// notifications before later calls), and what LSP's ordering rules
	// assume.
	err := jsonrpcstdio.Serve(ctx, rpc, jsonrpcstdio.FramingContentLength, os.Stdin, os.Stdout)

	logger.InfoContext(ctx, "LSP server closed", "error", err)
	return nil
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
