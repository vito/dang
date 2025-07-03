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
	"github.com/chewxy/hm"
	"github.com/chzyer/readline"
	"github.com/kr/pretty"
	"github.com/spf13/cobra"
	"github.com/vito/dash/introspection"
	"github.com/vito/dash/pkg/dash"
	"github.com/vito/dash/pkg/ioctx"
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
		Use:   "dash [flags] [file|directory]",
		Short: "Dash language interpreter",
		Long: `Dash is a functional language for building Dagger pipelines.
It provides type-safe, composable abstractions for container operations.`,
		Example: `  # Run a Dash script
  dash script.dash

  # Run all .dash files in a directory as a module
  dash ./my-module

  # Start interactive REPL
  dash

  # Run with debug logging enabled
  dash --debug script.dash
  dash -d ./my-module`,
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
		fang.WithErrorHandler(func(w io.Writer, styles fang.Styles, err error) {
			fmt.Fprintln(w, err.Error())
		}),
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

	// Check if the path is a directory or file
	fileInfo, err := os.Stat(cfg.File)
	if err != nil {
		return fmt.Errorf("failed to access path %s: %w", cfg.File, err)
	}

	if fileInfo.IsDir() {
		// Evaluate directory as a module
		if _, err := dash.RunDir(ctx, dag.GraphQLClient(), schema, cfg.File, cfg.Debug); err != nil {
			return fmt.Errorf("failed to evaluate Dash directory: %w", err)
		}
	} else {
		// Evaluate single file
		if err := dash.RunFile(ctx, dag.GraphQLClient(), schema, cfg.File, cfg.Debug); err != nil {
			return fmt.Errorf("failed to evaluate Dash file: %w", err)
		}
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
	r.evalEnv = dash.NewEvalEnvWithSchema(r.typeEnv, r.dag.GraphQLClient(), r.schema)

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

	for _, node := range result.(dash.Block).Forms {
		if r.debug {
			pretty.Println(node)
		}

		// Type inference
		_, err = dash.Infer(r.typeEnv, node, true)
		if err != nil {
			return fmt.Errorf("type error: %w", err)
		}

		// Evaluation with stdout context
		ctx := ioctx.StdoutToContext(r.ctx, os.Stdout)
		val, err := dash.EvalNode(ctx, r.evalEnv, node)
		if err != nil {
			return fmt.Errorf("evaluation error: %w", err)
		}

		// Print result (unless it's null)
		if _, isNull := val.(dash.NullValue); !isNull {
			fmt.Printf("=> %s\n", val.String())
		}
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
	repl.typeEnv = dash.NewEnv(repl.schema)
	repl.evalEnv = dash.NewEvalEnvWithSchema(repl.typeEnv, repl.dag.GraphQLClient(), repl.schema)
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
	fmt.Printf("Connected to Dagger with %d GraphQL types\n\n", len(repl.schema.Types))

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

func (r *REPL) schemaCommand(repl *REPL, args []string) error {
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

func (r *REPL) typeCommand(repl *REPL, args []string) error {
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
	result, err := dash.Parse("type-check", []byte(expr))
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	node := result.(dash.Block)

	// Type inference
	inferredType, err := dash.Infer(repl.typeEnv, node, false)
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
					if rt, ok := ft.Arg().(*dash.RecordType); ok {
						if len(rt.Fields) == 0 {
							fmt.Println("Note: This is a zero-argument function that auto-calls")
						} else {
							hasRequired := false
							for _, field := range rt.Fields {
								if fieldType, _ := field.Value.Type(); fieldType != nil {
									if _, isNonNull := fieldType.(dash.NonNullType); isNonNull {
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

func (r *REPL) inspectCommand(repl *REPL, args []string) error {
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

func (r *REPL) findCommand(repl *REPL, args []string) error {
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
