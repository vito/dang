package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"unicode"

	"github.com/creachadair/jrpc2"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

// NewHandler create JSON-RPC handler for this language server.
func NewHandler(rootCtx context.Context) *langHandler {
	handler := &langHandler{
		rootCtx:            rootCtx,
		files:              make(map[DocumentURI]*File),
		loadedEnvDirs:      make(map[string]bool),
		importCache:        make(map[string][]dang.ImportConfig),
		schemaModuleCaches: make(map[string]*sync.Map),
		parses:             &analysis.ParseCache{},
		mu:                 new(sync.Mutex),
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

	// Per-directory cache of *Type pointers for imported schemas. A long-
	// lived cache is essential here: each analyzeDirectory call otherwise
	// gets a fresh ContextWithImportConfigs cache, so a sibling file whose
	// parse gets invalidated (e.g. open-buffer transition) re-builds its
	// schema module and breaks type identity with the rest of the directory.
	schemaModuleCaches map[string]*sync.Map

	// Cached file parses. Sibling files in the same directory don't change on
	// every keystroke, so reusing their parse trees turns directory analysis
	// from O(files) into O(1) parses.
	parses *analysis.ParseCache

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
	AST         *dang.FileBlock // Parsed and type-annotated AST
	TypeScope   dang.TypeScope  // Type environment after inference

	// Analysis is the analysis of the file's directory, and Path the file's
	// path in its Source (see analyzeDirectory). Analysis is nil when the
	// directory couldn't be read.
	Analysis *analysis.Analysis
	Path     string

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

	// Mark the file as being processed and publish the latest text before
	// analysis. Readers either get a complete snapshot from before this point or
	// wait until this update finishes.
	f.mu.Lock()
	f.processing = true
	f.Text = text
	if version != nil {
		f.Version = *version
	}
	f.mu.Unlock()
	h.mu.Unlock()

	fp, err := fromURI(uri)
	if err != nil {
		versionNumber, diagnostics := h.finishFileUpdate(f, emptyFileAnalysis())
		h.publishDiagnostics(ctx, uri, diagnostics, versionNumber)
		return fmt.Errorf("file path from URI: %w", err)
	}

	slog.InfoContext(ctx, "file updated", "path", fp)

	result, err := h.analyzeDirectory(ctx, uri, fp)

	// Completion needs tree-sitter, whose module initialize started
	// compiling alongside this analysis. Finish that before readers get the
	// file, so the first completion doesn't spend the client's timeout on
	// the compile. Once it's compiled, this returns at once.
	dang.LoadTreeSitter()

	if err != nil {
		versionNumber, diagnostics := h.finishFileUpdate(f, emptyFileAnalysis())
		h.publishDiagnostics(ctx, uri, diagnostics, versionNumber)
		return err
	}

	versionNumber, diagnostics := h.finishFileUpdate(f, result)

	// Publish diagnostics to the client.
	h.publishDiagnostics(ctx, uri, diagnostics, versionNumber)

	return nil
}

type directoryFile struct {
	URI   DocumentURI
	Block *dang.FileBlock
}

type fileAnalysis struct {
	Diagnostics []Diagnostic
	Symbols     *SymbolTable
	AST         *dang.FileBlock
	TypeScope   dang.TypeScope
	Analysis    *analysis.Analysis
	Path        string
}

// analyzeDirectory analyzes the directory of the file at fp, focused on it:
// its body is inferred in full, its siblings only for their declarations.
// The analysis reads the directory from disk, with the open buffers in it
// laid over, through a Source rooted at the directory.
func (h *langHandler) analyzeDirectory(ctx context.Context, uri DocumentURI, fp string) (*fileAnalysis, error) {
	fileDir := filepath.Dir(fp)
	focus := filepath.Base(fp)

	// Look up the directory's dang.toml once per analysis: it decides both the
	// Dang version every file in the directory is parsed as and the imports.
	configPath, projectConfig, configErr := dang.FindProjectConfig(fileDir)
	if configErr != nil {
		slog.WarnContext(ctx, "failed to find dang.toml", "error", configErr)
	}
	version := dang.VersionForConfig(ctx, projectConfig)
	if projectConfig != nil {
		ctx = dang.ContextWithProjectConfig(ctx, configPath, projectConfig)
	}

	// Resolve import configs once for the directory, using a cache to avoid
	// spawning new dagger sessions on every keystroke, and only once the file
	// parses.
	resolveImports := func(ctx context.Context) []dang.ImportConfig {
		configs, _ := h.resolveImports(ctx, fileDir, configPath, projectConfig)
		return configs
	}

	// The schema-module cache is long-lived, per directory. Without it, each
	// analyzeDirectory call gets a fresh cache and ImportDecls in re-parsed
	// sibling files build divergent *Type instances — types like
	// Test.ServerInfo fail to unify.
	src := analysis.Overlay(os.DirFS(fileDir), h.openBuffers(fileDir))
	a, err := analysis.Analyze(ctx, src, ".", analysis.Options{
		Root:              fileDir,
		Focus:             focus,
		ResolveImports:    resolveImports,
		SchemaModuleCache: h.schemaModuleCacheFor(fileDir),
		Version:           version,
		Parses:            h.parses,
	})
	if err != nil {
		return nil, err
	}

	result := emptyFileAnalysis()
	result.Analysis = a
	result.Path = focus
	result.Diagnostics = fileDiagnostics(a, focus)

	current := a.File(focus)
	if current == nil || current.Block == nil {
		return result, nil
	}
	result.AST = current.Block
	// The block's TypeScope composes the shared dirScope with the file's own
	// imports, so editor features see exactly what inference saw — including the
	// file's unqualified imported symbols, which only live in the file-local env.
	result.TypeScope = a.FileScope(focus)

	// Build a directory-wide symbol table so features like go-to-definition can
	// resolve declarations from sibling files while still reporting the correct
	// URI for each declaration.
	var parsedFiles []directoryFile
	for _, f := range a.Files {
		if f.Block != nil {
			parsedFiles = append(parsedFiles, directoryFile{URI: toURI(f.Name), Block: f.Block})
		}
	}
	result.Symbols = h.buildDirectorySymbolTable(parsedFiles, uri)

	return result, nil
}

func (h *langHandler) finishFileUpdate(f *File, result *fileAnalysis) (int, []Diagnostic) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if result != nil {
		f.Diagnostics = result.Diagnostics
		f.Symbols = result.Symbols
		f.AST = result.AST
		f.TypeScope = result.TypeScope
		f.Analysis = result.Analysis
		f.Path = result.Path
	}

	f.processing = false
	f.cond.Broadcast()

	diagnostics := append([]Diagnostic(nil), f.Diagnostics...)
	return f.Version, diagnostics
}

func emptyFileAnalysis() *fileAnalysis {
	return &fileAnalysis{
		Diagnostics: []Diagnostic{},
		Symbols:     emptySymbolTable(),
	}
}

func emptySymbolTable() *SymbolTable {
	return &SymbolTable{
		Definitions: make(map[string]*SymbolInfo),
		References:  make(map[string]*SymbolRef),
	}
}

// openBuffers returns the text of the open buffers in dir, keyed by file name,
// including unsaved ones that don't exist on disk yet.
func (h *langHandler) openBuffers(dir string) map[string]string {
	h.mu.Lock()
	files := make(map[DocumentURI]*File, len(h.files))
	for uri, f := range h.files {
		files[uri] = f
	}
	h.mu.Unlock()

	buffers := map[string]string{}
	for uri, f := range files {
		path, err := fromURI(uri)
		if err != nil {
			continue
		}
		path = filepath.Clean(path)
		if filepath.Dir(path) != dir {
			continue
		}
		f.mu.Lock()
		buffers[filepath.Base(path)] = f.Text
		f.mu.Unlock()
	}
	return buffers
}

// schemaModuleCacheFor returns the persistent schema-module cache for a
// directory, creating it on first reference. Sharing the cache across all
// analyzeDirectory calls for the same directory keeps imported-type identity
// stable when sibling parses get invalidated (e.g. on open-buffer transitions).
func (h *langHandler) schemaModuleCacheFor(dirPath string) *sync.Map {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cache, ok := h.schemaModuleCaches[dirPath]; ok {
		return cache
	}
	cache := &sync.Map{}
	h.schemaModuleCaches[dirPath] = cache
	return cache
}

// versionForFile returns the Dang version of the file at path (see
// dang.VersionForDir). A broken dang.toml is reported elsewhere (import
// resolution), so here it just falls back to the version of ungoverned
// source.
func versionForFile(ctx context.Context, path string) dang.Version {
	version, err := dang.VersionForDir(ctx, filepath.Dir(path))
	if err != nil {
		slog.WarnContext(ctx, "failed to determine Dang version", "path", path, "error", err)
	}
	return version
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

// waitForFile waits for a file to finish processing and returns it.
// Returns nil if the file doesn't exist.
func (h *langHandler) waitForFile(uri DocumentURI) *File {
	h.mu.Lock()
	f, ok := h.files[uri]
	h.mu.Unlock()

	if !ok {
		return nil
	}

	return f.waitForSnapshot()
}

func (f *File) waitForSnapshot() *File {
	// Wait for processing to complete, then return a snapshot so subsequent
	// didChange processing can update the shared File without racing readers.
	f.mu.Lock()
	defer f.mu.Unlock()
	for f.processing {
		f.cond.Wait()
	}

	return &File{
		LanguageID:  f.LanguageID,
		Text:        f.Text,
		Version:     f.Version,
		Diagnostics: append([]Diagnostic(nil), f.Diagnostics...),
		Symbols:     f.Symbols,
		AST:         f.AST,
		TypeScope:   f.TypeScope,
		Analysis:    f.Analysis,
		Path:        f.Path,
	}
}

// resolveImports returns cached import configs for the given file directory,
// resolving them on first access. configPath and projectConfig are the result
// of dang.FindProjectConfig for fileDir (empty and nil if none was found). The
// returned context has the project config attached if a dang.toml was found.
func (h *langHandler) resolveImports(ctx context.Context, fileDir, configPath string, projectConfig *dang.ProjectConfig) ([]dang.ImportConfig, context.Context) {
	// Cache key: config path, or a synthetic key for auto-detected Dagger.
	cacheKey := configPath
	if cacheKey == "" {
		cacheKey = "auto:" + fileDir
	}

	h.mu.Lock()
	cached, ok := h.importCache[cacheKey]
	h.mu.Unlock()
	if ok {
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
	// dang.toml or auto-detect from Dagger module config. The schema
	// is eagerly introspected (module-aware).
	importConfigs = dang.ResolveDaggerImport(ctx, importConfigs, fileDir)

	h.mu.Lock()
	h.importCache[cacheKey] = importConfigs
	h.mu.Unlock()
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
		// For FieldDecl, use the precise location from the Symbol itself
		if fieldDecl, ok := node.(*dang.FieldDecl); ok && fieldDecl.Name != nil && fieldDecl.Name.Loc != nil {
			loc := fieldDecl.Name.Loc
			st.Definitions[fieldDecl.Name.Name] = &SymbolInfo{
				Name: fieldDecl.Name.Name,
				Location: &Location{
					URI:   uri,
					Range: nameRange(loc, fieldDecl.Name.Name),
				},
				Kind: h.symbolKind(node),
				Node: node,
			}
		} else if objectDecl, ok := node.(*dang.ObjectDecl); ok && objectDecl.Name != nil && objectDecl.Name.Loc != nil {
			// For ObjectDecl, use the precise location from the Symbol itself
			loc := objectDecl.Name.Loc
			st.Definitions[objectDecl.Name.Name] = &SymbolInfo{
				Name: objectDecl.Name.Name,
				Location: &Location{
					URI:   uri,
					Range: nameRange(loc, objectDecl.Name.Name),
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
	case *dang.Coerce:
		// Inference wraps values flowing into typed contexts (a field's
		// value, a function's body) in a Coerce; look through it.
		h.collectNestedSymbols(uri, n.Expr, st)
	case *dang.Block:
		h.collectSymbols(uri, n.Forms, st)
	case *dang.FileBlock:
		h.collectSymbols(uri, n.Forms, st)
	case *dang.ObjectDecl:
		// Collect symbols from object body
		h.collectSymbols(uri, n.Value.Forms, st)
	case *dang.FieldDecl:
		// If the field value is a block, collect from it
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
	case *dang.ObjectDecl:
		return ObjectCompletion
	case *dang.FieldDecl:
		// Check if the field value is a function
		if field, ok := node.(*dang.FieldDecl); ok {
			if _, isFunDecl := uncoerced(field.Value).(*dang.FunDecl); isFunDecl {
				return FunctionCompletion
			}
		}
		return VariableCompletion
	default:
		return VariableCompletion
	}
}

// uncoerced looks through the Coerce nodes inference wraps around values
// flowing into typed contexts, such as a field's value.
func uncoerced(node dang.Node) dang.Node {
	for {
		c, ok := node.(*dang.Coerce)
		if !ok {
			return node
		}
		node = c.Expr
	}
}

func (h *langHandler) publishDiagnostics(ctx context.Context, uri DocumentURI, diagnostics []Diagnostic, version int) {
	if h.server == nil {
		return
	}

	if diagnostics == nil {
		diagnostics = []Diagnostic{}
	}

	err := h.server.Notify(ctx, "textDocument/publishDiagnostics", &PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
		Version:     version,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to publish diagnostics", "error", err)
	}
}

func isIdentifierChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '_'
}

func (h *langHandler) addFolder(folder string) {
	folder = filepath.Clean(folder)
	h.mu.Lock()
	defer h.mu.Unlock()
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
	case "textDocument/codeAction":
		return h.handleTextDocumentCodeAction
	case "textDocument/rename":
		return h.handleTextDocumentRename
	case "textDocument/references":
		return h.handleTextDocumentReferences
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
