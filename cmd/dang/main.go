package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/charmbracelet/fang"
	"github.com/chzyer/readline"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/kr/pretty"
	"github.com/spf13/cobra"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
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

	// Load GraphQL configuration
	config := dang.LoadGraphQLConfig()
	provider := dang.NewGraphQLClientProvider(config)

	// Get configured GraphQL client and schema
	client, schema, err := provider.GetClientAndSchema(ctx)
	if err != nil {
		return fmt.Errorf("failed to setup GraphQL client: %w", err)
	}
	defer provider.Close() //nolint:errcheck

	// Check if the path is a directory or file
	fileInfo, err := os.Stat(cfg.File)
	if err != nil {
		return fmt.Errorf("failed to access path %s: %w", cfg.File, err)
	}

	if fileInfo.IsDir() {
		// Evaluate directory as a module
		if _, err := dang.RunDir(ctx, client, schema, cfg.File, cfg.Debug); err != nil {
			return err
		}
	} else {
		// Evaluate single file
		if err := dang.RunFile(ctx, client, schema, cfg.File, cfg.Debug); err != nil {
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

	// Load GraphQL configuration
	config := dang.LoadGraphQLConfig()
	provider := dang.NewGraphQLClientProvider(config)

	// Get configured GraphQL client and schema
	client, schema, err := provider.GetClientAndSchema(ctx)
	if err != nil {
		return fmt.Errorf("failed to setup GraphQL client: %w", err)
	}
	defer provider.Close() //nolint:errcheck

	// Create REPL instance
	repl := &REPL{
		schema: schema,
		client: client,
		debug:  cfg.Debug,
	}

	return repl.Run(ctx)
}

// REPL represents the Read-Eval-Print Loop
type REPL struct {
	schema   *introspection.Schema
	client   graphql.Client
	debug    bool
	typeEnv  dang.Env
	evalEnv  dang.EvalEnv
	commands map[string]REPLCommand
}

// REPLCommand represents a REPL command function
type REPLCommand struct {
	description string
	handler     func(context.Context, *REPL, []string) error
}

// Run starts the REPL
func (r *REPL) Run(ctx context.Context) error {
	// Initialize environments
	r.typeEnv = dang.NewEnv(r.schema)
	r.evalEnv = dang.NewEvalEnvWithSchema(r.typeEnv, r.client, r.schema)

	// Initialize commands
	r.initCommands()

	// Configure readline
	rl, err := readline.NewEx(&readline.Config{
		Prompt:            "dang> ",
		HistoryFile:       "/tmp/dang_history",
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
	defer rl.Close() //nolint:errcheck

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

		if err := r.processLine(ctx, line); err != nil {
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
			description: "Show Dang version information",
			handler:     r.versionCommand,
		},
		"history": {
			description: "Show command history",
			handler:     r.historyCommand,
		},
		"schema": {
			description: "Show GraphQL schema information",
			handler:     r.schemaCommand,
		},
		"type": {
			description: "Show type information for an expression",
			handler:     r.typeCommand,
		},
		"inspect": {
			description: "Inspect a GraphQL type in detail",
			handler:     r.inspectCommand,
		},
		"find": {
			description: "Find functions or types by name pattern",
			handler:     r.findCommand,
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

	// Add common Dang keywords
	dangKeywords := []string{"print", "container", "let", "if", "match", "true", "false", "null"}
	for _, keyword := range dangKeywords {
		items = append(items, readline.PcItem(keyword))
	}

	return readline.NewPrefixCompleter(items...)
}

// printWelcome prints the welcome message
func (r *REPL) printWelcome() {
	fmt.Println("Welcome to Dang REPL v0.1.0!")
	fmt.Println("Interactive environment for Dang functional language")
	fmt.Printf("Connected to GraphQL API with %d types\n", len(r.schema.Types))
	fmt.Println()
	fmt.Println("Type :help for available commands")
	fmt.Println("Type expressions to evaluate them")
	fmt.Println("Use Tab for auto-completion, Ctrl+C to exit")
	fmt.Println()
}

// processLine processes a single line of input
func (r *REPL) processLine(ctx context.Context, line string) error {
	// Check if it's a REPL command
	if strings.HasPrefix(line, ":") {
		return r.handleCommand(ctx, line[1:])
	}

	// Otherwise, evaluate as Dang code
	return r.evaluateExpression(ctx, line)
}

// handleCommand handles REPL commands
func (r *REPL) handleCommand(ctx context.Context, cmdLine string) error {
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

	return cmd.handler(ctx, r, args)
}

// evaluateExpression evaluates a Dang expression
func (r *REPL) evaluateExpression(ctx context.Context, expr string) error {
	// Parse the expression
	result, err := dang.Parse("repl", []byte(expr))
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	forms := result.(*dang.ModuleBlock).Forms

	if r.debug {
		for _, node := range forms {
			_, _ = pretty.Println(node)
		}
	}

	// Type inference using phased approach (handles hoisting for type declarations)
	fresh := hm.NewSimpleFresher()
	_, err = dang.InferFormsWithPhases(ctx, forms, r.typeEnv, fresh)
	if err != nil {
		return fmt.Errorf("type error: %w", err)
	}

	for _, node := range forms {
		// Evaluation with stdout context
		val, err := dang.EvalNode(ctx, r.evalEnv, node)
		if err != nil {
			return fmt.Errorf("evaluation error: %w", err)
		}

		fmt.Printf("=> %s\n", val.String())

		if r.debug {
			_, _ = pretty.Println(val)
		}
	}

	return nil
}

// REPL command handlers

func (r *REPL) helpCommand(ctx context.Context, repl *REPL, args []string) error {
	fmt.Println("Available commands:")
	for cmd, info := range r.commands {
		fmt.Printf("  :%s - %s\n", cmd, info.description)
	}
	fmt.Println("\nYou can also type Dang expressions to evaluate them.")
	fmt.Println("Examples:")
	fmt.Println("  print(\"Hello, World!\")")
	fmt.Println("  42 + 8")
	fmt.Println("  [1, 2, 3]")
	return nil
}

func (r *REPL) exitCommand(ctx context.Context, repl *REPL, args []string) error {
	return fmt.Errorf("exit")
}

func (r *REPL) clearCommand(ctx context.Context, repl *REPL, args []string) error {
	fmt.Print("\033[2J\033[H") // ANSI escape codes to clear screen and move cursor to top-left
	repl.printWelcome()
	return nil
}

func (r *REPL) resetCommand(ctx context.Context, repl *REPL, args []string) error {
	// Reset evaluation environment
	repl.typeEnv = dang.NewEnv(repl.schema)
	repl.evalEnv = dang.NewEvalEnvWithSchema(repl.typeEnv, repl.client, repl.schema)
	fmt.Println("Environment reset.")
	return nil
}

func (r *REPL) debugCommand(ctx context.Context, repl *REPL, args []string) error {
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

func (r *REPL) envCommand(ctx context.Context, repl *REPL, args []string) error {
	// Parse optional filter argument
	filter := ""
	showAll := false
	if len(args) > 0 {
		if args[0] == "all" {
			showAll = true
		} else {
			filter = args[0]
		}
	}

	fmt.Println("Current environment bindings:")
	fmt.Printf("Connected to GraphQL API with %d types\n\n", len(repl.schema.Types))

	// Show built-in functions
	fmt.Println("Built-in functions:")
	fmt.Println("  print(value: a) -> Null")
	fmt.Println()

	// Show global functions (from Query type)
	if repl.schema.QueryType.Name != "" {
		queryType := findType(repl.schema, repl.schema.QueryType.Name)
		if queryType != nil && len(queryType.Fields) > 0 {
			fmt.Println("Global functions (Query type):")
			count := 0
			for _, field := range queryType.Fields {
				if filter != "" && !strings.Contains(strings.ToLower(field.Name), strings.ToLower(filter)) {
					continue
				}

				if !showAll && count >= 10 {
					fmt.Printf("  ... and %d more (use ':env all' to see all)\n", len(queryType.Fields)-count)
					break
				}

				// Format field signature
				signature := formatFieldSignature(field)
				fmt.Printf("  %s\n", signature)
				count++
			}
			fmt.Println()
		}
	}

	// Show available types (sample)
	if !showAll {
		fmt.Println("Available GraphQL types (sample):")
		count := 0
		for _, t := range repl.schema.Types {
			if filter != "" && !strings.Contains(strings.ToLower(t.Name), strings.ToLower(filter)) {
				continue
			}

			if !isBuiltinType(t.Name) && count < 10 {
				fieldCount := len(t.Fields)
				fmt.Printf("  %s (%d fields)\n", t.Name, fieldCount)
				count++
			}
		}
		if count == 10 {
			fmt.Printf("  ... and more (use ':schema' for full schema info)\n")
		}
	}

	if filter != "" {
		fmt.Printf("\n(Filtered by: %s)\n", filter)
	}
	fmt.Println("\nTip: Use ':schema' to explore the full GraphQL schema")
	fmt.Println("     Use ':type <expression>' to check types")
	return nil
}

func (r *REPL) versionCommand(ctx context.Context, repl *REPL, args []string) error {
	fmt.Println("Dang REPL v0.1.0")
	fmt.Println("Interactive Read-Eval-Print Loop for Dang language")
	fmt.Printf("Connected to GraphQL API with %d types\n", len(repl.schema.Types))
	return nil
}

func (r *REPL) historyCommand(ctx context.Context, repl *REPL, args []string) error {
	fmt.Println("Command history is managed by readline")
	fmt.Println("Use Up/Down arrows to navigate history")
	fmt.Println("Use Ctrl+R for reverse search")
	fmt.Println("History is persisted in /tmp/dang_history")
	return nil
}

// Helper functions for enhanced commands

// findType finds a GraphQL type by name
func findType(schema *introspection.Schema, name string) *introspection.Type {
	for _, t := range schema.Types {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// formatFieldSignature formats a GraphQL field signature for display
func formatFieldSignature(field *introspection.Field) string {
	var parts []string
	parts = append(parts, field.Name)

	if len(field.Args) > 0 {
		args := []string{}
		for _, arg := range field.Args {
			argStr := fmt.Sprintf("%s: %s", arg.Name, formatTypeRef(arg.TypeRef))
			args = append(args, argStr)
		}
		parts = append(parts, fmt.Sprintf("(%s)", strings.Join(args, ", ")))
	} else {
		parts = append(parts, "()")
	}

	parts = append(parts, "->", formatTypeRef(field.TypeRef))
	return strings.Join(parts, " ")
}

// formatTypeRef formats a GraphQL type reference for display
func formatTypeRef(ref *introspection.TypeRef) string {
	switch ref.Kind {
	case introspection.TypeKindNonNull:
		return formatTypeRef(ref.OfType) + "!"
	case introspection.TypeKindList:
		return "[" + formatTypeRef(ref.OfType) + "]"
	default:
		if ref.Name != "" {
			return ref.Name
		}
		return "Unknown"
	}
}

// isBuiltinType checks if a type name is a GraphQL builtin
func isBuiltinType(name string) bool {
	builtins := []string{"String", "Int", "Float", "Boolean", "ID", "__Schema", "__Type", "__Field", "__InputValue", "__EnumValue", "__Directive"}
	for _, builtin := range builtins {
		if name == builtin || strings.HasPrefix(name, "__") {
			return true
		}
	}
	return false
}

// Additional REPL command handlers

func (r *REPL) schemaCommand(ctx context.Context, repl *REPL, args []string) error {
	if len(args) == 0 {
		// Show schema overview
		fmt.Printf("GraphQL Schema Overview:\n")
		fmt.Printf("Query Type: %s\n", repl.schema.QueryType.Name)
		if repl.schema.Mutation() != nil && repl.schema.Mutation().Name != "" {
			fmt.Printf("Mutation Type: %s\n", repl.schema.Mutation().Name)
		}
		if repl.schema.Subscription() != nil && repl.schema.Subscription().Name != "" {
			fmt.Printf("Subscription Type: %s\n", repl.schema.Subscription().Name)
		}
		fmt.Printf("Total Types: %d\n\n", len(repl.schema.Types))

		// Show types by category
		objects, _, enums, scalars, _ := categorizeTypes(repl.schema.Types)

		fmt.Printf("Object Types (%d):\n", len(objects))
		for i, t := range objects {
			if i < 10 {
				fmt.Printf("  %s (%d fields)\n", t.Name, len(t.Fields))
			} else if i == 10 {
				fmt.Printf("  ... and %d more\n", len(objects)-10)
				break
			}
		}

		if len(enums) > 0 {
			fmt.Printf("\nEnum Types (%d):\n", len(enums))
			for i, t := range enums {
				if i < 5 {
					fmt.Printf("  %s\n", t.Name)
				} else if i == 5 {
					fmt.Printf("  ... and %d more\n", len(enums)-5)
					break
				}
			}
		}

		if len(scalars) > 0 {
			fmt.Printf("\nScalar Types (%d):\n", len(scalars))
			for _, t := range scalars {
				if !isBuiltinType(t.Name) {
					fmt.Printf("  %s\n", t.Name)
				}
			}
		}

		fmt.Println("\nUse ':schema <type>' to inspect a specific type")
		fmt.Println("Use ':find <pattern>' to search for types or functions")
		return nil
	}

	// Show specific type
	typeName := args[0]
	return r.inspectTypeByName(typeName)
}

func (r *REPL) typeCommand(ctx context.Context, repl *REPL, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: :type <expression>")
		fmt.Println("Examples:")
		fmt.Println("  :type container")
		fmt.Println("  :type print")
		fmt.Println("  :type \"hello\"")
		fmt.Println("  :type [1, 2, 3]")
		return nil
	}

	expr := strings.Join(args, " ")

	// Parse the expression
	result, err := dang.Parse("type-check", []byte(expr))
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	node := result.(*dang.Block)

	// Type inference
	inferredType, err := dang.Infer(ctx, repl.typeEnv, node, false)
	if err != nil {
		return fmt.Errorf("type error: %w", err)
	}

	fmt.Printf("Expression: %s\n", expr)
	fmt.Printf("Type: %s\n", inferredType)

	// Try to provide additional context
	if strings.TrimSpace(expr) != "" && !strings.Contains(expr, " ") {
		// Single symbol, try to provide more info
		if scheme, found := repl.typeEnv.SchemeOf(strings.TrimSpace(expr)); found {
			if t, _ := scheme.Type(); t != nil {
				fmt.Printf("Scheme: %s\n", scheme)

				// Check if it's a function that auto-calls
				if ft, ok := t.(*hm.FunctionType); ok {
					if rt, ok := ft.Arg().(*dang.RecordType); ok {
						if len(rt.Fields) == 0 {
							fmt.Println("Note: This is a zero-argument function that auto-calls")
						} else {
							hasRequired := false
							for _, field := range rt.Fields {
								if fieldType, _ := field.Value.Type(); fieldType != nil {
									if _, isNonNull := fieldType.(hm.NonNullType); isNonNull {
										hasRequired = true
										break
									}
								}
							}
							if !hasRequired {
								fmt.Println("Note: This function has only optional arguments and auto-calls")
							}
						}
					}
				}
			}
		}
	}

	return nil
}

func (r *REPL) inspectCommand(ctx context.Context, repl *REPL, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: :inspect <type-name>")
		fmt.Println("Examples:")
		fmt.Println("  :inspect Container")
		fmt.Println("  :inspect Query")
		return nil
	}

	typeName := args[0]
	return r.inspectTypeByName(typeName)
}

func (r *REPL) inspectTypeByName(typeName string) error {
	gqlType := findType(r.schema, typeName)
	if gqlType == nil {
		return fmt.Errorf("type '%s' not found", typeName)
	}

	fmt.Printf("Type: %s\n", gqlType.Name)
	if gqlType.Description != "" {
		fmt.Printf("Description: %s\n", gqlType.Description)
	}
	fmt.Printf("Kind: %s\n", gqlType.Kind)

	if len(gqlType.Fields) > 0 {
		fmt.Printf("\nFields (%d):\n", len(gqlType.Fields))
		for _, field := range gqlType.Fields {
			signature := formatFieldSignature(field)
			fmt.Printf("  %s\n", signature)
			if field.Description != "" {
				fmt.Printf("    Description: %s\n", field.Description)
			}
			if field.IsDeprecated {
				fmt.Printf("    DEPRECATED")
				if field.DeprecationReason != "" {
					fmt.Printf(": %s", field.DeprecationReason)
				}
				fmt.Println()
			}
		}
	}

	if len(gqlType.EnumValues) > 0 {
		fmt.Printf("\nEnum Values (%d):\n", len(gqlType.EnumValues))
		for _, val := range gqlType.EnumValues {
			fmt.Printf("  %s\n", val.Name)
			if val.Description != "" {
				fmt.Printf("    Description: %s\n", val.Description)
			}
		}
	}

	return nil
}

func (r *REPL) findCommand(ctx context.Context, repl *REPL, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: :find <pattern>")
		fmt.Println("Examples:")
		fmt.Println("  :find container")
		fmt.Println("  :find file")
		fmt.Println("  :find git")
		return nil
	}

	pattern := strings.ToLower(args[0])

	fmt.Printf("Searching for '%s'...\n\n", pattern)

	// Search in global functions (Query type)
	if r.schema.QueryType.Name != "" {
		queryType := findType(r.schema, r.schema.QueryType.Name)
		if queryType != nil {
			matches := []string{}
			for _, field := range queryType.Fields {
				if strings.Contains(strings.ToLower(field.Name), pattern) {
					signature := formatFieldSignature(field)
					matches = append(matches, fmt.Sprintf("  %s", signature))
				}
			}
			if len(matches) > 0 {
				fmt.Printf("Global Functions:\n")
				for _, match := range matches {
					fmt.Println(match)
				}
				fmt.Println()
			}
		}
	}

	// Search in types
	typeMatches := []string{}
	for _, t := range r.schema.Types {
		if strings.Contains(strings.ToLower(t.Name), pattern) && !isBuiltinType(t.Name) {
			typeMatches = append(typeMatches, fmt.Sprintf("  %s (%s, %d fields)", t.Name, t.Kind, len(t.Fields)))
		}
	}
	if len(typeMatches) > 0 {
		fmt.Printf("Types:\n")
		for _, match := range typeMatches {
			fmt.Println(match)
		}
		fmt.Println()
	}

	// Search in type fields
	fieldMatches := []string{}
	for _, t := range r.schema.Types {
		if isBuiltinType(t.Name) {
			continue
		}
		for _, field := range t.Fields {
			if strings.Contains(strings.ToLower(field.Name), pattern) {
				signature := formatFieldSignature(field)
				fieldMatches = append(fieldMatches, fmt.Sprintf("  %s.%s", t.Name, signature))
			}
		}
	}
	if len(fieldMatches) > 0 {
		fmt.Printf("Type Fields:\n")
		for i, match := range fieldMatches {
			if i < 20 {
				fmt.Println(match)
			} else if i == 20 {
				fmt.Printf("  ... and %d more matches\n", len(fieldMatches)-20)
				break
			}
		}
	}

	if len(typeMatches) == 0 && len(fieldMatches) == 0 {
		fmt.Printf("No matches found for '%s'\n", pattern)
	}

	return nil
}

// categorizeTypes categorizes GraphQL types by kind
func categorizeTypes(types []*introspection.Type) (objects, interfaces, enums, scalars, inputTypes []*introspection.Type) {
	for _, t := range types {
		if isBuiltinType(t.Name) {
			continue
		}
		switch t.Kind {
		case introspection.TypeKindObject:
			objects = append(objects, t)
		case introspection.TypeKindInterface:
			interfaces = append(interfaces, t)
		case introspection.TypeKindEnum:
			enums = append(enums, t)
		case introspection.TypeKindScalar:
			scalars = append(scalars, t)
		case introspection.TypeKindInputObject:
			inputTypes = append(inputTypes, t)
		}
	}
	return
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

	if list {
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
		}
		return nil
	}

	// Print to stdout
	fmt.Print(formatted)
	return nil
}
