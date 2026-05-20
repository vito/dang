package lsp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"unicode"

	"github.com/creachadair/jrpc2"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

// NewHandler create JSON-RPC handler for this language server.
func NewHandler(rootCtx context.Context) *langHandler {
	handler := &langHandler{
		rootCtx:       rootCtx,
		files:         make(map[DocumentURI]*File),
		loadedEnvDirs: make(map[string]bool),
		importCache:   make(map[string][]dang.ImportConfig),
		mu:            new(sync.Mutex),
	}

	return handler
}

// SetServer sets the server instance for the handler.
func (h *langHandler) SetServer(srv *jrpc2.Server) {
	h.server = srv
}

type langHandler struct {
	rootCtx  context.Context
	files    map[DocumentURI]*File
	server   *jrpc2.Server
	rootPath string
	folders  []string

	// Directories where we've already loaded .envrc via direnv
	loadedEnvDirs map[string]bool

	// Cached import configs keyed by the dang.toml config path (or "" for
	// auto-detected Dagger imports without a config file). Prevents spawning
	// a new dagger session on every keystroke.
	importCache map[string][]dang.ImportConfig

	// TODO?: make per-file or something
	mu *sync.Mutex
}

// File is
type File struct {
	LanguageID  string
	Text        string
	Version     int
	Diagnostics []Diagnostic
	Symbols     *SymbolTable
	AST         *dang.ModuleBlock // Parsed and type-annotated AST
	TypeEnv     dang.Env          // Type environment after inference

	// Synchronization for async file processing
	mu         sync.Mutex
	cond       *sync.Cond
	processing bool // true while the file is being parsed/typechecked
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
	Location *Location
	Kind     CompletionItemKind
	Node     dang.Node // The AST node that declared this symbol
}

// SymbolRef describes a symbol reference
type SymbolRef struct {
	Name     string
	Location Range
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

func fromURI(uri DocumentURI) (string, error) {
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

func toURI(path string) DocumentURI {
	if isWindowsDrivePath(path) {
		path = "/" + path
	}
	return DocumentURI((&url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(path),
	}).String())
}

func (h *langHandler) closeFile(uri DocumentURI) error {
	h.mu.Lock()
	delete(h.files, uri)
	h.mu.Unlock()
	return nil
}

func (h *langHandler) saveFile(uri DocumentURI) error {
	return nil
}

func (h *langHandler) openFile(uri DocumentURI, languageID string, version int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	f := &File{
		Text:       "",
		LanguageID: languageID,
		Version:    version,
		processing: true,
	}
	f.cond = sync.NewCond(&f.mu)
	h.files[uri] = f
	return nil
}

func (h *langHandler) updateFile(ctx context.Context, uri DocumentURI, text string, version *int) error {
	h.mu.Lock()
	f, ok := h.files[uri]
	if !ok {
		h.mu.Unlock()
		return fmt.Errorf("document not found: %v", uri)
	}

	// Mark the file as being processed
	f.mu.Lock()
	f.processing = true
	f.mu.Unlock()
	h.mu.Unlock()

	// Ensure we signal completion when done
	defer func() {
		f.mu.Lock()
		f.processing = false
		f.cond.Broadcast() // Wake up all waiting handlers
		f.mu.Unlock()
	}()

	f.Text = text
	if version != nil {
		f.Version = *version
	}

	fp, err := fromURI(uri)
	if err != nil {
		return fmt.Errorf("file path from URI: %w", err)
	}

	slog.InfoContext(ctx, "file updated", "path", fp)

	if err := h.analyzeDirectory(ctx, uri, fp, f); err != nil {
		return err
	}

	// Publish diagnostics to the client
	h.publishDiagnostics(ctx, uri, f)

	return nil
}

type directoryFile struct {
	URI   DocumentURI
	Block *dang.ModuleBlock
}

func (h *langHandler) analyzeDirectory(ctx context.Context, uri DocumentURI, fp string, f *File) error {
	// Clear diagnostics before collecting new ones.
	f.Diagnostics = []Diagnostic{}
	f.Symbols = emptySymbolTable()
	f.AST = nil
	f.TypeEnv = nil

	fileDir := filepath.Dir(fp)
	files, err := h.directoryDangFiles(fileDir)
	if err != nil {
		return err
	}

	var parsedFiles []directoryFile
	var allForms []dang.Node
	var currentBlock *dang.ModuleBlock

	for _, path := range files {
		fileURI := toURI(path)
		fileText, err := h.textForPath(path)
		if err != nil {
			return err
		}

		parsed, err := dang.ParseWithRecovery(path, []byte(fileText), dang.GlobalStore("filePath", path))
		if err != nil {
			slog.WarnContext(ctx, "failed to parse Dang code for LSP", "path", path, "error", err)
			if sameFile(path, fp) {
				f.Diagnostics = append(f.Diagnostics, h.errorToDiagnostics(err, uri)...)
			}
			continue
		}

		block, ok := parsed.(*dang.ModuleBlock)
		if !ok {
			slog.WarnContext(ctx, "parsed result is not a ModuleBlock", "path", path, "type", fmt.Sprintf("%T", parsed))
			continue
		}

		if sameFile(path, fp) {
			currentBlock = block
			f.AST = block
		}

		parsedFiles = append(parsedFiles, directoryFile{
			URI:   fileURI,
			Block: block,
		})
		allForms = append(allForms, block.Forms...)
	}

	if currentBlock == nil {
		return nil
	}

	// Build a directory-wide symbol table so features like go-to-definition can
	// resolve declarations from sibling files while still reporting the correct
	// URI for each declaration.
	f.Symbols = h.buildDirectorySymbolTable(parsedFiles, uri)

	// Resolve import configs once for the directory, using a cache to avoid
	// spawning new dagger sessions on every keystroke.
	importConfigs, ctx := h.resolveImports(ctx, fileDir)
	if len(importConfigs) > 0 {
		ctx = dang.ContextWithImportConfigs(ctx, importConfigs...)
	}

	// Inject auto-imports (e.g. Dagger) before inference. Keep each file's AST
	// forms untouched so editor features work against user-written source only.
	allForms = dang.InjectAutoImports(ctx, allForms)

	// Run type inference over every .dang file in the directory, matching the
	// interpreter's directory-module semantics.
	typeEnv := dang.NewPreludeEnv("")
	fresh := hm.NewSimpleFresher()
	_, err = dang.InferFormsWithPhases(ctx, allForms, typeEnv, fresh)
	if err != nil {
		f.Diagnostics = append(f.Diagnostics, h.errorToDiagnosticsForPath(err, uri, fp)...)
	}
	// Store the type environment in the File for later use (e.g., hover).
	f.TypeEnv = typeEnv

	return nil
}

func emptySymbolTable() *SymbolTable {
	return &SymbolTable{
		Definitions: make(map[string]*SymbolInfo),
		References:  make(map[string]*SymbolRef),
	}
}

func (h *langHandler) directoryDangFiles(dir string) ([]string, error) {
	dangFiles, err := filepath.Glob(filepath.Join(dir, "*.dang"))
	if err != nil {
		return nil, fmt.Errorf("failed to find .dang files in directory %s: %w", dir, err)
	}

	seen := make(map[string]bool, len(dangFiles))
	for i, path := range dangFiles {
		path = filepath.Clean(path)
		dangFiles[i] = path
		seen[path] = true
	}

	// Include open, unsaved .dang files that may not exist on disk yet.
	h.mu.Lock()
	for openURI := range h.files {
		path, err := fromURI(openURI)
		if err != nil {
			continue
		}
		path = filepath.Clean(path)
		if filepath.Dir(path) != dir || filepath.Ext(path) != ".dang" || seen[path] {
			continue
		}
		dangFiles = append(dangFiles, path)
		seen[path] = true
	}
	h.mu.Unlock()

	sort.Strings(dangFiles)
	return dangFiles, nil
}

func (h *langHandler) textForPath(path string) (string, error) {
	uri := toURI(path)

	h.mu.Lock()
	openFile := h.files[uri]
	h.mu.Unlock()

	if openFile != nil {
		return openFile.Text, nil
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read source file %s: %w", path, err)
	}
	return string(contents), nil
}

func (h *langHandler) buildDirectorySymbolTable(files []directoryFile, currentURI DocumentURI) *SymbolTable {
	st := emptySymbolTable()

	// Prefer declarations in the current file for simple symbol lookup. Sibling
	// declarations are fallbacks for references that are genuinely cross-file.
	for _, file := range files {
		if file.URI == currentURI && file.Block != nil {
			h.collectSymbols(file.URI, file.Block.Forms, st)
			break
		}
	}

	for _, file := range files {
		if file.URI == currentURI || file.Block == nil {
			continue
		}

		siblingSymbols := h.buildSymbolTable(file.URI, file.Block.Forms)
		for name, info := range siblingSymbols.Definitions {
			if _, exists := st.Definitions[name]; !exists {
				st.Definitions[name] = info
			}
		}
		for pos, ref := range siblingSymbols.References {
			if _, exists := st.References[pos]; !exists {
				st.References[pos] = ref
			}
		}
	}

	return st
}

func sameFile(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// waitForFile waits for a file to finish processing and returns it.
// Returns nil if the file doesn't exist.
func (h *langHandler) waitForFile(uri DocumentURI) *File {
	h.mu.Lock()
	f, ok := h.files[uri]
	h.mu.Unlock()

	if !ok {
		return nil
	}

	// Wait for processing to complete
	f.mu.Lock()
	for f.processing {
		f.cond.Wait()
	}
	f.mu.Unlock()

	return f
}

// resolveImports returns cached import configs for the given file directory,
// resolving them on first access. The returned context has the project config
// attached if a dang.toml was found.
func (h *langHandler) resolveImports(ctx context.Context, fileDir string) ([]dang.ImportConfig, context.Context) {
	configPath, projectConfig, configErr := dang.FindProjectConfig(fileDir)
	if configErr != nil {
		slog.WarnContext(ctx, "failed to find dang.toml", "error", configErr)
	}

	// Cache key: config path, or a synthetic key for auto-detected Dagger.
	cacheKey := configPath
	if cacheKey == "" {
		cacheKey = "auto:" + fileDir
	}

	if cached, ok := h.importCache[cacheKey]; ok {
		// Re-attach project config to the context even on cache hit.
		if projectConfig != nil {
			ctx = dang.ContextWithProjectConfig(ctx, configPath, projectConfig)
		}
		return cached, ctx
	}

	var importConfigs []dang.ImportConfig

	if projectConfig != nil {
		configDir := filepath.Dir(configPath)

		// Load .envrc before resolving imports, so that ${VAR}
		// expansion in dang.toml picks up direnv variables.
		h.loadEnvrc(ctx, configDir)

		ctx = dang.ContextWithProjectConfig(ctx, configPath, projectConfig)
		// Use rootCtx for import resolution so that service
		// processes outlive individual LSP requests.
		resolveCtx := dang.ContextWithProjectConfig(h.rootCtx, configPath, projectConfig)
		resolved, resolveErr := dang.ResolveImportConfigs(resolveCtx, projectConfig, configDir)
		if resolveErr != nil {
			slog.WarnContext(ctx, "failed to resolve dang.toml imports", "error", resolveErr)
		} else {
			importConfigs = append(importConfigs, resolved...)
		}
	}

	// Resolve the Dagger import: use an explicit one from
	// dang.toml or auto-detect from dagger.json. The schema
	// is eagerly introspected (module-aware).
	importConfigs = dang.ResolveDaggerImport(ctx, importConfigs, fileDir)

	h.importCache[cacheKey] = importConfigs
	return importConfigs, ctx
}

func (h *langHandler) buildSymbolTable(uri DocumentURI, forms []dang.Node) *SymbolTable {
	st := &SymbolTable{
		Definitions: make(map[string]*SymbolInfo),
		References:  make(map[string]*SymbolRef),
	}

	// Walk the AST and collect symbols
	h.collectSymbols(uri, forms, st)

	return st
}

// collectSymbols walks the AST and collects symbol definitions and references
func (h *langHandler) collectSymbols(uri DocumentURI, nodes []dang.Node, st *SymbolTable) {
	for _, node := range nodes {
		// For SlotDecl, use the precise location from the Symbol itself
		if slotDecl, ok := node.(*dang.SlotDecl); ok && slotDecl.Name != nil && slotDecl.Name.Loc != nil {
			loc := slotDecl.Name.Loc
			st.Definitions[slotDecl.Name.Name] = &SymbolInfo{
				Name: slotDecl.Name.Name,
				Location: &Location{
					URI: uri,
					Range: Range{
						Start: Position{Line: loc.Line - 1, Character: loc.Column - 1},
						End:   Position{Line: loc.Line - 1, Character: loc.Column - 1 + len(slotDecl.Name.Name)},
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
				Location: &Location{
					URI: uri,
					Range: Range{
						Start: Position{Line: loc.Line - 1, Character: loc.Column - 1},
						End:   Position{Line: loc.Line - 1, Character: loc.Column - 1 + len(classDecl.Name.Name)},
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
						Location: &Location{
							URI: uri,
							Range: Range{
								Start: Position{Line: loc.Line - 1, Character: loc.Column - 1},
								End:   Position{Line: loc.Line - 1, Character: loc.Column - 1 + len(name)},
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
func (h *langHandler) collectNestedSymbols(uri DocumentURI, node dang.Node, st *SymbolTable) {
	switch n := node.(type) {
	case *dang.Block:
		h.collectSymbols(uri, n.Forms, st)
	case *dang.ModuleBlock:
		h.collectSymbols(uri, n.Forms, st)
	case *dang.ClassDecl:
		// Collect symbols from class body
		h.collectSymbols(uri, n.Value.Forms, st)
	case *dang.SlotDecl:
		// If the slot value is a block, collect from it
		if n.Value != nil {
			h.collectNestedSymbols(uri, n.Value, st)
		}
	case *dang.BlockArg:
		// Collect from block arg parameters
		for _, arg := range n.Args {
			h.collectSymbols(uri, []dang.Node{arg}, st)
		}
		// Collect from block arg body
		h.collectNestedSymbols(uri, n.BodyNode, st)
	case *dang.FunDecl:
		// Collect from function arguments
		for _, arg := range n.FunctionBase.Args { //nolint:staticcheck // Body() method shadows Body field
			h.collectSymbols(uri, []dang.Node{arg}, st)
		}
		// Collect from function body
		h.collectNestedSymbols(uri, n.FunctionBase.Body, st) //nolint:staticcheck // Body() method shadows Body field
	}
}

// symbolKind determines the LSP completion item kind for a node
func (h *langHandler) symbolKind(node dang.Node) CompletionItemKind {
	switch node.(type) {
	case *dang.ClassDecl:
		return ClassCompletion
	case *dang.SlotDecl:
		// Check if the slot value is a function
		if slot, ok := node.(*dang.SlotDecl); ok {
			if _, isFunDecl := slot.Value.(*dang.FunDecl); isFunDecl {
				return FunctionCompletion
			}
		}
		return VariableCompletion
	default:
		return VariableCompletion
	}
}

func (h *langHandler) publishDiagnostics(ctx context.Context, uri DocumentURI, f *File) {
	if h.server == nil {
		return
	}

	diagnostics := f.Diagnostics
	if diagnostics == nil {
		diagnostics = []Diagnostic{}
	}

	err := h.server.Notify(ctx, "textDocument/publishDiagnostics", &PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
		Version:     f.Version,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to publish diagnostics", "error", err)
	}
}

func (h *langHandler) errorToDiagnosticsForPath(err error, uri DocumentURI, path string) []Diagnostic {
	var inferErrs *dang.InferenceErrors
	if errors.As(err, &inferErrs) {
		var ds []Diagnostic
		for _, e := range inferErrs.Errors {
			ds = append(ds, h.errorToDiagnosticsForPath(e, uri, path)...)
		}
		return ds
	}

	if loc := errorLocation(err); loc != nil && loc.Filename != "" && !sameFile(loc.Filename, path) {
		return nil
	}

	return h.errorToDiagnostics(err, uri)
}

func errorLocation(err error) *dang.SourceLocation {
	var sourceErr *dang.SourceError
	var parseErr interface {
		ParseErrorLocation() *dang.SourceLocation
	}
	var inferErr *dang.InferError
	if errors.As(err, &sourceErr) {
		return sourceErr.Location
	} else if errors.As(err, &parseErr) {
		return parseErr.ParseErrorLocation()
	} else if errors.As(err, &inferErr) {
		return inferErr.Location
	}
	return nil
}

// errorToDiagnostic converts a Dang error to an LSP Diagnostic
func (h *langHandler) errorToDiagnostics(err error, uri DocumentURI) []Diagnostic {
	slog.Warn("converting error", "type", fmt.Sprintf("%T", err), "err", err)
	for e := errors.Unwrap(err); e != nil && e != err; e = errors.Unwrap(e) {
		slog.Warn("unwrapped", "type", fmt.Sprintf("%T", e), "err", e)
	}

	var inferErrs *dang.InferenceErrors
	if errors.As(err, &inferErrs) {
		var ds []Diagnostic
		for _, e := range inferErrs.Errors {
			ds = append(ds, h.errorToDiagnostics(e, uri)...)
		}
		return ds
	}

	var startLine, endLine = 0, 0
	var startCol, endCol = 0, 1

	var loc *dang.SourceLocation

	// Try to extract parse or infer error with location info
	var sourceErr *dang.SourceError
	var parseErr interface {
		ParseErrorLocation() *dang.SourceLocation
	}
	var inferErr *dang.InferError
	if errors.As(err, &sourceErr) {
		loc = sourceErr.Location
		slog.Warn("got source error", "err", sourceErr, "loc", loc)
	} else if errors.As(err, &parseErr) {
		loc = parseErr.ParseErrorLocation()
		slog.Warn("got parse error", "err", parseErr, "loc", loc)
	} else if errors.As(err, &inferErr) {
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

	// For SourceErrors, use the inner error message to avoid the full
	// formatted output with source highlighting in the diagnostic.
	message := err.Error()
	if sourceErr != nil {
		message = sourceErr.Inner.Error()
	}

	return []Diagnostic{
		{
			Range: Range{
				Start: Position{Line: startLine, Character: startCol},
				End:   Position{Line: endLine, Character: endCol},
			},
			Severity: 1, // Error
			Source:   stringPtr("dang"),
			Message:  message,
		},
	}
}

func stringPtr(s string) *string {
	return &s
}

func isIdentifierChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '_'
}

func (h *langHandler) addFolder(folder string) {
	folder = filepath.Clean(folder)
	found := slices.Contains(h.folders, folder)
	if !found {
		h.folders = append(h.folders, folder)
	}
}

// Assign implements jrpc2.Assigner
func (h *langHandler) Assign(ctx context.Context, method string) jrpc2.Handler {
	switch method {
	case "initialize":
		return h.handleInitialize
	case "initialized":
		return func(ctx context.Context, req *jrpc2.Request) (any, error) {
			return nil, nil
		}
	case "shutdown":
		return h.handleShutdown
	case "textDocument/didOpen":
		return h.handleTextDocumentDidOpen
	case "textDocument/didChange":
		return h.handleTextDocumentDidChange
	case "textDocument/didSave":
		return h.handleTextDocumentDidSave
	case "textDocument/didClose":
		return h.handleTextDocumentDidClose
	case "textDocument/completion":
		return h.handleTextDocumentCompletion
	case "textDocument/definition":
		return h.handleTextDocumentDefinition
	case "textDocument/hover":
		return h.handleTextDocumentHover
	case "textDocument/rename":
		return h.handleTextDocumentRename
	case "textDocument/formatting":
		return h.handleTextDocumentFormatting
	case "workspace/symbol":
		return h.handleWorkspaceSymbol
	case "workspace/didChangeConfiguration":
		return h.handleWorkspaceDidChangeConfiguration
	case "workspace/workspaceFolders":
		return h.handleWorkspaceWorkspaceFolders
	case "workspace/didChangeWorkspaceFolders":
		return h.handleWorkspaceDidChangeWorkspaceFolders
	}

	return nil
}
