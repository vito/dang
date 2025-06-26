package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/charmbracelet/fang"
	"github.com/chzyer/readline"
	"github.com/spf13/cobra"
	"github.com/vito/dash/introspection"
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
		Use:   "dash [flags] [file]",
		Short: "Dash language interpreter",
		Long: `Dash is a functional language for building Dagger pipelines.
It provides type-safe, composable abstractions for container operations.`,
		Example: `  # Run a Dash script
  dash script.dash

  # Start interactive REPL
  dash

  # Run with debug logging enabled
  dash --debug script.dash
  dash -d script.dash`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				cfg.File = args[0]
				return run(cfg)
			} else {
				return runREPL(cfg)
			}
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

func runREPL(cfg Config) error {
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

	// Create REPL instance
	repl := &REPL{
		ctx:    ctx,
		schema: schema,
		dag:    dag,
		debug:  cfg.Debug,
	}

	return repl.Run()
}

// REPL represents the Read-Eval-Print Loop
type REPL struct {
	ctx      context.Context
	schema   *introspection.Schema
	dag      *dagger.Client
	debug    bool
	typeEnv  *dash.Module
	evalEnv  dash.EvalEnv
	commands map[string]REPLCommand
}

// REPLCommand represents a REPL command function
type REPLCommand struct {
	description string
	handler     func(*REPL, []string) error
}

// Run starts the REPL
func (r *REPL) Run() error {
	// Initialize environments
	r.typeEnv = dash.NewEnv(r.schema)
	r.evalEnv = dash.NewEvalEnvWithSchema(r.schema, r.dag)

	// Initialize commands
	r.initCommands()

	// Configure readline
	rl, err := readline.NewEx(&readline.Config{
		Prompt:            "dash> ",
		HistoryFile:       "/tmp/dash_history",
		AutoComplete:      r.createCompleter(),
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
		HistorySearchFold: true,
		FuncFilterInputRune: func(r rune) (rune, bool) {
			switch r {
			case readline.CharCtrlZ:
				return r, false
			}
			return r, true
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create readline: %w", err)
	}
	defer rl.Close()

	// Print welcome message
	r.printWelcome()

	// Main REPL loop
	for {
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				if len(line) == 0 {
					break
				} else {
					continue
				}
			} else if err == io.EOF {
				break
			}
			return err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if err := r.processLine(line); err != nil {
			if err.Error() == "exit" {
				break
			}
			fmt.Printf("Error: %v\n", err)
		}
	}

	fmt.Println("Goodbye!")
	return nil
}

// initCommands initializes REPL commands
func (r *REPL) initCommands() {
	r.commands = map[string]REPLCommand{
		"help": {
			description: "Show this help message",
			handler:     r.helpCommand,
		},
		"exit": {
			description: "Exit the REPL",
			handler:     r.exitCommand,
		},
		"quit": {
			description: "Exit the REPL",
			handler:     r.exitCommand,
		},
		"clear": {
			description: "Clear the screen",
			handler:     r.clearCommand,
		},
		"reset": {
			description: "Reset the evaluation environment",
			handler:     r.resetCommand,
		},
		"debug": {
			description: "Toggle debug mode",
			handler:     r.debugCommand,
		},
		"env": {
			description: "Show current environment bindings",
			handler:     r.envCommand,
		},
		"version": {
			description: "Show Dash version information",
			handler:     r.versionCommand,
		},
		"history": {
			description: "Show command history",
			handler:     r.historyCommand,
		},
	}
}

// createCompleter creates autocomplete functionality
func (r *REPL) createCompleter() readline.AutoCompleter {
	items := []readline.PrefixCompleterInterface{}

	// Add REPL commands
	for cmd := range r.commands {
		items = append(items, readline.PcItem(":"+cmd))
	}

	// Add common Dash keywords
	dashKeywords := []string{"print", "container", "let", "if", "match", "true", "false", "null"}
	for _, keyword := range dashKeywords {
		items = append(items, readline.PcItem(keyword))
	}

	return readline.NewPrefixCompleter(items...)
}

// printWelcome prints the welcome message
func (r *REPL) printWelcome() {
	fmt.Println("Welcome to Dash REPL v0.1.0!")
	fmt.Println("Interactive environment for Dash functional language")
	fmt.Printf("Connected to Dagger with %d GraphQL types\n", len(r.schema.Types))
	fmt.Println()
	fmt.Println("Type :help for available commands")
	fmt.Println("Type expressions to evaluate them")
	fmt.Println("Use Tab for auto-completion, Ctrl+C to exit")
	fmt.Println()
}

// processLine processes a single line of input
func (r *REPL) processLine(line string) error {
	// Check if it's a REPL command
	if strings.HasPrefix(line, ":") {
		return r.handleCommand(line[1:])
	}

	// Otherwise, evaluate as Dash code
	return r.evaluateExpression(line)
}

// handleCommand handles REPL commands
func (r *REPL) handleCommand(cmdLine string) error {
	parts := strings.Fields(cmdLine)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	cmdName := parts[0]
	args := parts[1:]

	cmd, exists := r.commands[cmdName]
	if !exists {
		return fmt.Errorf("unknown command: %s (type :help for available commands)", cmdName)
	}

	return cmd.handler(r, args)
}

// evaluateExpression evaluates a Dash expression
func (r *REPL) evaluateExpression(expr string) error {
	// Parse the expression
	result, err := dash.Parse("repl", []byte(expr))
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	node := result.(dash.Block)

	// Type inference
	_, err = dash.Infer(r.typeEnv, node, true)
	if err != nil {
		return fmt.Errorf("type error: %w", err)
	}

	// Evaluation
	val, err := dash.EvalNode(r.ctx, r.evalEnv, node)
	if err != nil {
		return fmt.Errorf("evaluation error: %w", err)
	}

	// Print result (unless it's null)
	if _, isNull := val.(dash.NullValue); !isNull {
		fmt.Printf("=> %s\n", val.String())
	}

	return nil
}

// REPL command handlers

func (r *REPL) helpCommand(repl *REPL, args []string) error {
	fmt.Println("Available commands:")
	for cmd, info := range r.commands {
		fmt.Printf("  :%s - %s\n", cmd, info.description)
	}
	fmt.Println("\nYou can also type Dash expressions to evaluate them.")
	fmt.Println("Examples:")
	fmt.Println("  print(\"Hello, World!\")")
	fmt.Println("  42 + 8")
	fmt.Println("  [1, 2, 3]")
	return nil
}

func (r *REPL) exitCommand(repl *REPL, args []string) error {
	return fmt.Errorf("exit")
}

func (r *REPL) clearCommand(repl *REPL, args []string) error {
	fmt.Print("\033[2J\033[H") // ANSI escape codes to clear screen and move cursor to top-left
	repl.printWelcome()
	return nil
}

func (r *REPL) resetCommand(repl *REPL, args []string) error {
	// Reset evaluation environment
	repl.evalEnv = dash.NewEvalEnvWithSchema(repl.schema, repl.dag)
	fmt.Println("Environment reset.")
	return nil
}

func (r *REPL) debugCommand(repl *REPL, args []string) error {
	repl.debug = !repl.debug
	level := slog.LevelInfo
	if repl.debug {
		level = slog.LevelDebug
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	status := "disabled"
	if repl.debug {
		status = "enabled"
	}
	fmt.Printf("Debug mode %s.\n", status)
	return nil
}

func (r *REPL) envCommand(repl *REPL, args []string) error {
	fmt.Println("Current environment bindings:")
	fmt.Println("(Note: Environment introspection is limited in current implementation)")
	fmt.Printf("- Connected to Dagger with %d schema types\n", len(repl.schema.Types))
	fmt.Println("- Built-in functions: print")
	return nil
}

func (r *REPL) versionCommand(repl *REPL, args []string) error {
	fmt.Println("Dash REPL v0.1.0")
	fmt.Println("Interactive Read-Eval-Print Loop for Dash language")
	fmt.Printf("Connected to Dagger with %d GraphQL types\n", len(repl.schema.Types))
	return nil
}

func (r *REPL) historyCommand(repl *REPL, args []string) error {
	fmt.Println("Command history is managed by readline")
	fmt.Println("Use Up/Down arrows to navigate history")
	fmt.Println("Use Ctrl+R for reverse search")
	fmt.Println("History is persisted in /tmp/dash_history")
	return nil
}
