package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"unicode"

	"github.com/Khan/genqlient/graphql"
	"github.com/sourcegraph/jsonrpc2"
	"github.com/vito/dang/introspection"
	"github.com/vito/dang/pkg/dang"
)

// NewHandler create JSON-RPC handler for this language server.
func NewHandler() jsonrpc2.Handler {
	handler := &langHandler{
		files: make(map[DocumentURI]*File),
		conn:  nil,
	}

	return jsonrpc2.HandlerWithError(handler.handle)
}

type langHandler struct {
	files    map[DocumentURI]*File
	conn     *jsonrpc2.Conn
	rootPath string
	folders  []string
	schema   *introspection.Schema
	client   graphql.Client
	provider *dang.GraphQLClientProvider
}

// File is
type File struct {
	LanguageID      string
	Text            string
	Version         int
	Diagnostics     []Diagnostic
	Symbols         *SymbolTable
	LexicalAnalyzer *LexicalAnalyzer
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

func (h *langHandler) logMessage(typ MessageType, message string) {
	h.conn.Notify(
		context.Background(),
		"window/logMessage",
		&LogMessageParams{
			Type:    typ,
			Message: message,
		})
}

func (h *langHandler) closeFile(uri DocumentURI) error {
	delete(h.files, uri)
	return nil
}

func (h *langHandler) saveFile(uri DocumentURI) error {
	return nil
}

func (h *langHandler) openFile(uri DocumentURI, languageID string, version int) error {
	f := &File{
		Text:       "",
		LanguageID: languageID,
		Version:    version,
	}
	h.files[uri] = f
	return nil
}

func (h *langHandler) updateFile(ctx context.Context, uri DocumentURI, text string, version *int) error {
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

	slog.InfoContext(ctx, "file updated", "path", fp)

	// Parse the Dang code
	parsed, err := dang.Parse(string(uri), []byte(text))
	if err != nil {
		// If parsing fails, set empty structures
		slog.Warn("failed to parse Dang code for LSP", "error", err)
		f.Symbols = &SymbolTable{
			Definitions: make(map[string]*SymbolInfo),
			References:  make(map[string]*SymbolRef),
		}
		f.LexicalAnalyzer = NewLexicalAnalyzer()
	} else {
		// The parser returns a Block
		block, ok := parsed.(*dang.Block)
		if !ok {
			slog.Warn("parsed result is not a Block", "type", fmt.Sprintf("%T", parsed))
			f.Symbols = &SymbolTable{
				Definitions: make(map[string]*SymbolInfo),
				References:  make(map[string]*SymbolRef),
			}
			f.LexicalAnalyzer = NewLexicalAnalyzer()
		} else {
			// Build symbol table and lexical analyzer from the AST
			f.Symbols = h.buildSymbolTable(uri, block.Forms)
			f.LexicalAnalyzer = h.buildLexicalAnalyzer(uri, block.Forms)
		}
	}

	f.Diagnostics = nil

	// Publish diagnostics to the client
	h.publishDiagnostics(ctx, uri, f)

	return nil
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

// buildLexicalAnalyzer performs lexical analysis on parsed AST forms
func (h *langHandler) buildLexicalAnalyzer(uri DocumentURI, forms []dang.Node) *LexicalAnalyzer {
	analyzer := NewLexicalAnalyzer()
	analyzer.Analyze(uri, forms)
	return analyzer
}

// collectSymbols walks the AST and collects symbol definitions and references
func (h *langHandler) collectSymbols(uri DocumentURI, nodes []dang.Node, st *SymbolTable) {
	for _, node := range nodes {
		// Collect declared symbols (definitions)
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
	case *dang.ClassDecl:
		// Collect symbols from class body
		h.collectSymbols(uri, n.Value.Forms, st)
	case *dang.SlotDecl:
		// If the slot value is a block or lambda, collect from it
		if n.Value != nil {
			h.collectNestedSymbols(uri, n.Value, st)
		}
	case *dang.Lambda:
		// Collect from lambda body
		h.collectNestedSymbols(uri, n.FunctionBase.Body, st)
	}
}

// symbolKind determines the LSP completion item kind for a node
func (h *langHandler) symbolKind(node dang.Node) CompletionItemKind {
	switch node.(type) {
	case *dang.ClassDecl:
		return ClassCompletion
	case *dang.SlotDecl:
		// Check if the slot value is a function/lambda
		if slot, ok := node.(*dang.SlotDecl); ok {
			if _, isLambda := slot.Value.(*dang.Lambda); isLambda {
				return FunctionCompletion
			}
		}
		return VariableCompletion
	default:
		return VariableCompletion
	}
}

func (h *langHandler) publishDiagnostics(ctx context.Context, uri DocumentURI, f *File) {
	if h.conn == nil {
		return
	}

	diagnostics := f.Diagnostics
	if diagnostics == nil {
		diagnostics = []Diagnostic{}
	}

	err := h.conn.Notify(ctx, "textDocument/publishDiagnostics", &PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
		Version:     f.Version,
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

func (h *langHandler) handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	slog.DebugContext(ctx, "handle", "method", req.Method)

	switch req.Method {
	case "initialize":
		return h.handleInitialize(ctx, conn, req)
	case "initialized":
		return
	case "shutdown":
		return h.handleShutdown(ctx, conn, req)
	case "textDocument/didOpen":
		return h.handleTextDocumentDidOpen(ctx, conn, req)
	case "textDocument/didChange":
		return h.handleTextDocumentDidChange(ctx, conn, req)
	case "textDocument/didSave":
		return h.handleTextDocumentDidSave(ctx, conn, req)
	case "textDocument/didClose":
		return h.handleTextDocumentDidClose(ctx, conn, req)
	case "textDocument/completion":
		return h.handleTextDocumentCompletion(ctx, conn, req)
	case "textDocument/definition":
		return h.handleTextDocumentDefinition(ctx, conn, req)
	case "textDocument/hover":
		return h.handleTextDocumentHover(ctx, conn, req)
	case "workspace/didChangeConfiguration":
		return h.handleWorkspaceDidChangeConfiguration(ctx, conn, req)
	case "workspace/workspaceFolders":
		return h.handleWorkspaceWorkspaceFolders(ctx, conn, req)
	case "workspace/didChangeWorkspaceFolders":
		return h.handleWorkspaceDidChangeWorkspaceFolders(ctx, conn, req)
	}

	return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeMethodNotFound, Message: fmt.Sprintf("method not supported: %s", req.Method)}
}
