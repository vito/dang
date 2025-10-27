package lsp

import (
	"context"
	"errors"
	stdErrors "errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"unicode"

	"dagger.io/dagger"
	"github.com/Khan/genqlient/graphql"
	"github.com/newstack-cloud/ls-builder/common"
	lsp "github.com/newstack-cloud/ls-builder/lsp_3_17"
	"github.com/vito/dang/introspection"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

// NewHandler creates an LSP handler for the Dang language server.
func NewHandler(ctx context.Context) *lsp.Handler {
	handler := &langHandler{
		files:         make(map[lsp.DocumentURI]*File),
		moduleSchemas: make(map[string]*moduleSchema),
	}

	// Initialize a single Dagger client to be shared across all workspaces/modules
	dag, err := dagger.Connect(ctx)
	if err != nil {
		slog.WarnContext(ctx, "failed to connect to Dagger, will retry on demand", "error", err)
		// Don't fail initialization, just log the warning
	} else {
		handler.dag = dag
	}

	return lsp.NewHandler(
		lsp.WithInitializeHandler(handler.handleInitialize),
		lsp.WithInitializedHandler(handler.handleInitialized),
		lsp.WithShutdownHandler(handler.handleShutdown),
		lsp.WithTextDocumentDidOpenHandler(handler.handleTextDocumentDidOpen),
		lsp.WithTextDocumentDidChangeHandler(handler.handleTextDocumentDidChange),
		lsp.WithTextDocumentDidSaveHandler(handler.handleTextDocumentDidSave),
		lsp.WithTextDocumentDidCloseHandler(handler.handleTextDocumentDidClose),
		lsp.WithCompletionHandler(handler.handleTextDocumentCompletion),
		lsp.WithGotoDefinitionHandler(handler.handleTextDocumentDefinition),
		lsp.WithHoverHandler(handler.handleTextDocumentHover),
		lsp.WithDocumentRenameHandler(handler.handleTextDocumentRename),
		lsp.WithWorkspaceSymbolHandler(handler.handleWorkspaceSymbol),
		lsp.WithWorkspaceDidChangeConfigurationHandler(handler.handleWorkspaceDidChangeConfiguration),
		lsp.WithWorkspaceDidChangeFoldersHandler(handler.handleWorkspaceDidChangeWorkspaceFolders),
	)
}

type langHandler struct {
	files    map[lsp.DocumentURI]*File
	rootPath string
	folders  []string

	dag *dagger.Client

	// Per-module schema cache
	moduleSchemas map[string]*moduleSchema // moduleDir -> schema

	// Default schema/client for non-module files
	defaultSchema   *introspection.Schema
	defaultClient   graphql.Client
	defaultProvider *dang.GraphQLClientProvider
}

// moduleSchema holds the schema and client for a specific Dagger module
type moduleSchema struct {
	schema *introspection.Schema
	client graphql.Client
}

// File is
type File struct {
	LanguageID  string
	Text        string
	Version     int
	Diagnostics []lsp.Diagnostic
	Symbols     *SymbolTable
	AST         *dang.Block // Parsed and type-annotated AST
	TypeEnv     dang.Env    // Type environment after inference
}

// SymbolTable tracks symbol definitions and references in a file
type SymbolTable struct {
	// Map from symbol name to its definition location
	Definitions map[string]*SymbolInfo
	// Map from line:col string to symbol at that position
	References map[string]*SymbolRef
}

// SymbolInfo describes a symbol definition
type SymbolInfo struct {
	Name     string
	Location *lsp.Location
	Kind     lsp.CompletionItemKind
	Node     dang.Node // The AST node that declared this symbol
}

// SymbolRef describes a symbol reference
type SymbolRef struct {
	Name     string
	Location lsp.Range
}

func isWindowsDrivePath(path string) bool {
	if len(path) < 4 {
		return false
	}
	return unicode.IsLetter(rune(path[0])) && path[1] == ':'
}

func isWindowsDriveURI(uri string) bool {
	if len(uri) < 4 {
		return false
	}
	return uri[0] == '/' && unicode.IsLetter(rune(uri[1])) && uri[2] == ':'
}

func fromURI(uri lsp.DocumentURI) (string, error) {
	u, err := url.ParseRequestURI(string(uri))
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("only file URIs are supported, got %v", u.Scheme)
	}
	if isWindowsDriveURI(u.Path) {
		u.Path = u.Path[1:]
	}
	return u.Path, nil
}

func toURI(path string) lsp.DocumentURI {
	if isWindowsDrivePath(path) {
		path = "/" + path
	}
	return lsp.DocumentURI((&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
	}).String())
}

func (h *langHandler) openFile(uri lsp.DocumentURI, languageID string, version int) error {
	f := &File{
		Text:       "",
		LanguageID: languageID,
		Version:    version,
	}
	h.files[uri] = f
	return nil
}

func (h *langHandler) closeFile(uri lsp.DocumentURI) error {
	delete(h.files, uri)
	return nil
}

func (h *langHandler) saveFile(uri lsp.DocumentURI) error {
	return nil
}

func (h *langHandler) updateFile(ctx *common.LSPContext, uri lsp.DocumentURI, text string, version *int) error {
	f, ok := h.files[uri]
	if !ok {
		return fmt.Errorf("document not found: %v", uri)
	}

	f.Text = text
	if version != nil {
		f.Version = *version
	}

	fp, err := fromURI(uri)
	if err != nil {
		return fmt.Errorf("file path from URI: %w", err)
	}

	slog.InfoContext(ctx.Context, "file updated", "path", fp)

	// Clear diagnostics before collecting new ones
	f.Diagnostics = []lsp.Diagnostic{}

	// Parse the Dang code
	parsed, err := dang.Parse(string(uri), []byte(text))
	if err != nil {
		// If parsing fails, add parse error as diagnostic and set empty structures
		slog.Warn("failed to parse Dang code for LSP", "error", err)

		// Try to extract location info from parse error
		f.Diagnostics = append(f.Diagnostics, h.errorToDiagnostics(err, uri)...)

		f.Symbols = &SymbolTable{
			Definitions: make(map[string]*SymbolInfo),
			References:  make(map[string]*SymbolRef),
		}
		f.AST = nil
	} else {
		// The parser returns a Block
		block, ok := parsed.(*dang.Block)
		if !ok {
			slog.Warn("parsed result is not a Block", "type", fmt.Sprintf("%T", parsed))
			f.Symbols = &SymbolTable{
				Definitions: make(map[string]*SymbolInfo),
				References:  make(map[string]*SymbolRef),
			}
			f.AST = nil
		} else {
			// Store the AST
			f.AST = block

			// Build symbol table from the AST
			f.Symbols = h.buildSymbolTable(uri, block.Forms)

			// Get schema for this file's module
			schema, _, err := h.getSchemaForFile(ctx.Context, fp)
			if err != nil {
				slog.WarnContext(ctx.Context, "failed to get schema for file", "path", fp, "error", err)
			}

			// Run type inference to annotate AST with types
			if schema != nil {
				typeEnv := dang.NewEnv(schema)
				fresh := hm.NewSimpleFresher()
				_, err := dang.InferFormsWithPhases(ctx.Context, block.Forms, typeEnv, fresh)
				if err != nil {
					f.Diagnostics = append(f.Diagnostics, h.errorToDiagnostics(err, uri)...)
				}
				// Store the type environment in the File for later use (e.g., hover)
				f.TypeEnv = typeEnv
			}
		}
	}

	// Publish diagnostics to the client
	h.publishDiagnostics(ctx, uri, f)

	return nil
}

func (h *langHandler) publishDiagnostics(ctx *common.LSPContext, uri lsp.DocumentURI, f *File) {
	dispatcher := lsp.NewDispatcher(ctx)

	diagnostics := f.Diagnostics
	if diagnostics == nil {
		diagnostics = []lsp.Diagnostic{}
	}

	version := lsp.Integer(f.Version)
	err := dispatcher.PublishDiagnostics(lsp.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
		Version:     &version,
	})
	if err != nil {
		slog.ErrorContext(ctx.Context, "failed to publish diagnostics", "error", err)
	}
}
func (h *langHandler) buildSymbolTable(uri lsp.DocumentURI, forms []dang.Node) *SymbolTable {
	st := &SymbolTable{
		Definitions: make(map[string]*SymbolInfo),
		References:  make(map[string]*SymbolRef),
	}

	// Walk the AST and collect symbols
	h.collectSymbols(uri, forms, st)

	return st
}

// collectSymbols walks the AST and collects symbol definitions and references
func (h *langHandler) collectSymbols(uri lsp.DocumentURI, nodes []dang.Node, st *SymbolTable) {
	for _, node := range nodes {
		// For SlotDecl, use the precise location from the Symbol itself
		if slotDecl, ok := node.(*dang.SlotDecl); ok && slotDecl.Name != nil && slotDecl.Name.Loc != nil {
			loc := slotDecl.Name.Loc
			st.Definitions[slotDecl.Name.Name] = &SymbolInfo{
				Name: slotDecl.Name.Name,
				Location: &lsp.Location{
					URI: uri,
					Range: &lsp.Range{
						Start: lsp.Position{Line: lsp.UInteger(loc.Line - 1), Character: lsp.UInteger(loc.Column - 1)},
						End:   lsp.Position{Line: lsp.UInteger(loc.Line - 1), Character: lsp.UInteger(loc.Column - 1 + len(slotDecl.Name.Name))},
					},
				},
				Kind: h.symbolKind(node),
				Node: node,
			}
		} else if classDecl, ok := node.(*dang.ClassDecl); ok && classDecl.Name != nil && classDecl.Name.Loc != nil {
			// For ClassDecl, use the precise location from the Symbol itself
			loc := classDecl.Name.Loc
			st.Definitions[classDecl.Name.Name] = &SymbolInfo{
				Name: classDecl.Name.Name,
				Location: &lsp.Location{
					URI: uri,
					Range: &lsp.Range{
						Start: lsp.Position{Line: lsp.UInteger(loc.Line - 1), Character: lsp.UInteger(loc.Column - 1)},
						End:   lsp.Position{Line: lsp.UInteger(loc.Line - 1), Character: lsp.UInteger(loc.Column - 1 + len(classDecl.Name.Name))},
					},
				},
				Kind: h.symbolKind(node),
				Node: node,
			}
		} else {
			// For other node types, use the generic DeclaredSymbols method
			declared := node.DeclaredSymbols()
			for _, name := range declared {
				loc := node.GetSourceLocation()
				if loc != nil {
					// LSP uses 0-based line/column, SourceLocation uses 1-based
					st.Definitions[name] = &SymbolInfo{
						Name: name,
						Location: &lsp.Location{
							URI: uri,
							Range: &lsp.Range{
								Start: lsp.Position{Line: lsp.UInteger(loc.Line - 1), Character: lsp.UInteger(loc.Column - 1)},
								End:   lsp.Position{Line: lsp.UInteger(loc.Line - 1), Character: lsp.UInteger(loc.Column - 1 + len(name))},
							},
						},
						Kind: h.symbolKind(node),
						Node: node,
					}
				}
			}
		}

		// Recursively process nested nodes
		h.collectNestedSymbols(uri, node, st)
	}
}

// collectNestedSymbols recursively collects symbols from nested structures
func (h *langHandler) collectNestedSymbols(uri lsp.DocumentURI, node dang.Node, st *SymbolTable) {
	switch n := node.(type) {
	case *dang.Block:
		h.collectSymbols(uri, n.Forms, st)
	case *dang.ClassDecl:
		// Collect symbols from class body
		h.collectSymbols(uri, n.Value.Forms, st)
	case *dang.SlotDecl:
		// If the slot value is a block or lambda, collect from it
		if n.Value != nil {
			h.collectNestedSymbols(uri, n.Value, st)
		}
	case *dang.Lambda:
		// Collect from lambda arguments
		for _, arg := range n.FunctionBase.Args {
			h.collectSymbols(uri, []dang.Node{arg}, st)
		}
		// Collect from lambda body
		h.collectNestedSymbols(uri, n.FunctionBase.Body, st)
	case *dang.FunDecl:
		// Collect from function arguments
		for _, arg := range n.FunctionBase.Args {
			h.collectSymbols(uri, []dang.Node{arg}, st)
		}
		// Collect from function body
		h.collectNestedSymbols(uri, n.FunctionBase.Body, st)
	}
}

// symbolKind determines the LSP completion item kind for a node
func (h *langHandler) symbolKind(node dang.Node) lsp.CompletionItemKind {
	switch node.(type) {
	case *dang.ClassDecl:
		return lsp.CompletionItemKindClass
	case *dang.SlotDecl:
		// Check if the slot value is a function/lambda
		if slot, ok := node.(*dang.SlotDecl); ok {
			if _, isLambda := slot.Value.(*dang.Lambda); isLambda {
				return lsp.CompletionItemKindFunction
			}
		}
		return lsp.CompletionItemKindVariable
	default:
		return lsp.CompletionItemKindVariable
	}
}

// errorToDiagnostic converts a Dang error to an LSP Diagnostic
func (h *langHandler) errorToDiagnostics(err error, uri lsp.DocumentURI) []lsp.Diagnostic {
	slog.Warn("converting error", "type", fmt.Sprintf("%T", err), "err", err)
	for e := errors.Unwrap(err); e != nil && e != err; e = errors.Unwrap(e) {
		slog.Warn("unwrapped", "type", fmt.Sprintf("%T", e), "err", e)
	}

	var inferErrs *dang.InferenceErrors
	if errors.As(err, &inferErrs) {
		var ds []lsp.Diagnostic
		for _, e := range inferErrs.Errors {
			ds = append(ds, h.errorToDiagnostics(e, uri)...)
		}
		return ds
	}

	var startLine, endLine int = 0, 0
	var startCol, endCol int = 0, 1

	var loc *dang.SourceLocation

	// Try to extract parse or infer error with location info
	var parseErr interface {
		ParseErrorLocation() *dang.SourceLocation
	}
	var inferErr *dang.InferError
	if errors.As(err, &parseErr) {
		loc = parseErr.ParseErrorLocation()
		slog.Warn("got parse error", "err", parseErr, "loc", loc)
	} else if stdErrors.As(err, &inferErr) {
		slog.Warn("got infer error error", "err", inferErr)
		loc = inferErr.Location
	}

	if loc != nil {
		// LSP uses 0-based lines and columns, Dang uses 1-based
		startLine = loc.Line - 1
		startCol = loc.Column - 1
		endCol = startCol + loc.Length
		if loc.Length == 0 {
			endCol = startCol + 1 // Default to at least one character
		}

		// If we have an End position, use it
		endLine = startLine
		if loc.End != nil {
			endLine = loc.End.Line - 1
			endCol = loc.End.Column - 1
		}
	}

	return []lsp.Diagnostic{
		{
			Range: lsp.Range{
				Start: lsp.Position{Line: lsp.UInteger(startLine), Character: lsp.UInteger(startCol)},
				End:   lsp.Position{Line: lsp.UInteger(endLine), Character: lsp.UInteger(endCol)},
			},
			Severity: func() *lsp.DiagnosticSeverity { s := lsp.DiagnosticSeverityError; return &s }(),
			Source:   stringPtr("dang"),
			Message:  err.Error(),
		},
	}
}

func stringPtr(s string) *string {
	return &s
}

// getSchemaForFile returns the appropriate schema for a given file path.
// It searches for a dagger.json in the file's directory or parent directories,
// and caches the result per module directory.
func (h *langHandler) getSchemaForFile(ctx context.Context, filePath string) (*introspection.Schema, graphql.Client, error) {
	// Find the module directory for this file
	moduleDir := findDaggerModule(filepath.Dir(filePath))

	slog.WarnContext(ctx, "getting schema for file", "filePath", filePath)
	if moduleDir == "" {
		slog.WarnContext(ctx, "module dir not found", "filePath", filePath)

		// Not in a module, use default schema
		if h.defaultSchema == nil {
			// Lazily load default schema on first use
			if err := h.loadDefaultSchema(ctx); err != nil {
				return nil, nil, fmt.Errorf("failed to load default schema: %w", err)
			}
		}
		return h.defaultSchema, h.defaultClient, nil
	}

	// Check cache for this module
	if cached, ok := h.moduleSchemas[moduleDir]; ok {
		slog.WarnContext(ctx, "module schema cache hit", "filePath", filePath)
		return cached.schema, cached.client, nil
	}

	// Check if we have a Dagger client available
	if h.dag == nil {
		// No Dagger client, fall back to default schema
		slog.WarnContext(ctx, "no Dagger client available, falling back to default", "dir", moduleDir)
		if h.defaultSchema == nil {
			if err := h.loadDefaultSchema(ctx); err != nil {
				return nil, nil, fmt.Errorf("failed to load default schema: %w", err)
			}
		}
		return h.defaultSchema, h.defaultClient, nil
	}

	// Load and cache module schema
	slog.InfoContext(ctx, "loading schema for module", "dir", moduleDir)

	provider := dang.NewGraphQLClientProvider(dang.GraphQLConfig{}) // Empty config means use Dagger
	client, schema, err := provider.GetDaggerModuleSchema(ctx, h.dag, moduleDir)
	if err != nil {
		// If module schema loading fails, fall back to default schema
		slog.WarnContext(ctx, "failed to load module schema, falling back to default", "dir", moduleDir, "error", err)
		if h.defaultSchema == nil {
			if err := h.loadDefaultSchema(ctx); err != nil {
				return nil, nil, fmt.Errorf("failed to load default schema: %w", err)
			}
		}
		return h.defaultSchema, h.defaultClient, nil
	}

	h.moduleSchemas[moduleDir] = &moduleSchema{
		schema: schema,
		client: client,
	}

	slog.InfoContext(ctx, "cached schema for module", "dir", moduleDir, "types", len(schema.Types))
	return schema, client, nil
}

// loadDefaultSchema loads the default GraphQL schema from environment/config
func (h *langHandler) loadDefaultSchema(ctx context.Context) error {
	config := dang.LoadGraphQLConfig()
	h.defaultProvider = dang.NewGraphQLClientProvider(config)

	client, schema, err := h.defaultProvider.GetClientAndSchema(ctx)
	if err != nil {
		return err
	}

	h.defaultClient = client
	h.defaultSchema = schema

	slog.InfoContext(ctx, "loaded default GraphQL schema", "types", len(schema.Types))
	return nil
}

func isIdentifierChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '_'
}

func (h *langHandler) addFolder(folder string) {
	folder = filepath.Clean(folder)
	found := false
	for _, cur := range h.folders {
		if cur == folder {
			found = true
			break
		}
	}
	if !found {
		h.folders = append(h.folders, folder)
	}
}
