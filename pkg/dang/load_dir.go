package dang

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/vito/dang/v2/pkg/hm"
)

// LoadOptions configures LoadDir.
type LoadOptions struct {
	// FS is the filesystem to read the module from, the directory being a
	// slash-separated path in it (see fs.ValidPath). Nil reads the
	// directory from the OS filesystem, as RunDir does.
	//
	// From an FS, the nearest dang.toml at or above the directory (within
	// the FS, stopping at a .git) still sets the Dang version, but its
	// imports can't be resolved: an FS has no paths to load schema files or
	// Dang modules from. Supply them with ContextWithImportConfigs instead.
	FS fs.FS

	// Eager evaluates the module's top level as RunDir does: every
	// top-level variable is initialized, and every top-level statement run,
	// before LoadDir returns. By default the module is only declared (see
	// LoadDir).
	Eager bool
}

// Module is a directory module loaded by LoadDir: its type and value
// environments, which snippets can be checked and evaluated against as if
// they were one more file of the module (see Session).
type Module struct {
	// Dir is the directory the module was loaded from: a path in
	// LoadOptions.FS when one was given.
	Dir string

	// Version is the Dang version the module's source is written in, which
	// its snippets are parsed at too.
	Version Version

	// Files are the module's .dang files, parsed and type-checked, in name
	// order.
	Files []*FileBlock

	// TypeEnv holds the types of the module's top-level declarations, from
	// every file, private (`let`) ones included. Imports are file-local, so
	// they are not in it, the builtin imports (JSON, Random, …) included.
	TypeEnv TypeScope

	// ValueEnv holds the values of the module's top-level declarations,
	// matching TypeEnv. Unless the module was loaded with Eager, its
	// variables are initialized on first read.
	ValueEnv ValueScope

	// values are the context values the module was compiled under, which
	// its snippets are compiled and run under too (see context).
	values [len(moduleContextKeys)]any

	// sources holds the text of the module's files by filename when they
	// were read from an FS, for reporting errors in them.
	sources map[string]string

	// services is the registry LoadDir created for the import services the
	// module starts, stopped by Close; nil when ctx came with one.
	services *ServiceRegistry
}

// moduleContextKeys are the context values a module's code is compiled and
// evaluated under: its imports and the caches giving imported types one
// identity, its module identity (for `let` member visibility), its project
// config and version pin (for native imports), and its service registry.
// Snippets get the same values, so that their imports' types unify with the
// module's, they may use the module's private members, and so on.
var moduleContextKeys = [...]any{
	importConfigsKey{},
	schemaModuleCacheKey{},
	dangModuleCacheKey{},
	moduleScopeKey{},
	projectConfigKey{},
	versionKey{},
	servicesKey{},
}

// LoadDir loads the .dang files of dir as one module, the way RunDir does,
// and returns both its type and value environments, so that snippets can be
// type-checked and evaluated in its scope (see Module.NewSession). The whole
// module is type-checked, function bodies included, and a type error fails
// the load.
//
// By default the module is declared, not run: its imports, directives,
// constants, types and functions are evaluated, but its top-level variables
// are bound as lazy initializers that run when first read (by a snippet, or
// by module code a snippet calls), and its top-level statements never run.
// So loading a module calls no imported API unless reading a binding does,
// and a snippet calling one helper runs only what that helper needs. Two
// things still run up front, as they do for DeclareDir: the static members
// of types (`self { }` blocks), and the top level of imported native Dang
// modules. With opts.Eager, the module's top level runs as under RunDir.
//
// An initializer runs under whichever evaluation first reads the binding, with
// that evaluation's stdout and cancellation, and its result, an error
// included, is kept for every later read. Only a run cut short by that
// evaluation's own cancellation or timeout is not kept: the next read runs
// the initializer again.
//
// Call Close when done with the module, to stop the import services it
// started, unless ctx came with a ServiceRegistry of its own
// (ContextWithServices).
func LoadDir(ctx context.Context, dir string, opts LoadOptions) (*Module, error) {
	ctx, services := ensureServiceRegistry(ctx)
	m := &Module{Dir: dir, services: services}
	loaded := false
	defer func() {
		if !loaded {
			m.Close()
		}
	}()

	var blocks []*FileBlock
	var err error
	if opts.FS == nil {
		info, err := os.Stat(dir)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", dir)
		}
		ctx, blocks, err = parseDirBlocks(ctx, dir)
		if err != nil {
			return nil, err
		}
		m.Version, err = VersionForDir(ctx, dir)
		if err != nil {
			return nil, fmt.Errorf("determining Dang version: %w", err)
		}
	} else {
		ctx, blocks, m.Version, m.sources, err = parseFSBlocks(ctx, opts.FS, dir)
		if err != nil {
			return nil, err
		}
	}

	// Snippets look their imports up in the same caches, so make sure there
	// are some even if the module itself imports nothing.
	if _, ok := ctx.Value(schemaModuleCacheKey{}).(*sync.Map); !ok {
		ctx = WithSchemaModuleCache(ctx, &sync.Map{})
	}
	if _, ok := ctx.Value(dangModuleCacheKey{}).(*sync.Map); !ok {
		ctx = WithDangModuleCache(ctx, &sync.Map{})
	}
	// All files share one module identity, as under RunDir (see moduleScope).
	ctx = withModuleScope(ctx, &moduleScope{label: "dir:" + dir})

	types := NewPreludeTypeScope("")
	if err := InferDirectoryFiles(ctx, blocks, types, hm.NewSimpleFresher()); err != nil {
		return nil, withSourceText(ConvertInferError(err), m.sources)
	}

	values := newDirectoryValueScope(types)
	evalCtx := NewEvalContext(dir, "")
	evalCtx.sources = m.sources
	if err := evaluateDirectory(WithEvalContext(ctx, evalCtx), blocks, values, opts.Eager); err != nil {
		if translated, ok := translateBoundaryEvalError(err, evalCtx); ok {
			return nil, translated
		}
		return nil, err
	}

	m.Files = blocks
	m.TypeEnv = withoutBuiltinImports(types)
	m.ValueEnv = values
	for i, key := range moduleContextKeys {
		m.values[i] = ctx.Value(key)
	}
	loaded = true
	return m, nil
}

// Close stops the import services the module started, if LoadDir created
// the registry tracking them.
func (m *Module) Close() {
	if m.services != nil {
		m.services.StopAll()
	}
}

// context returns ctx with the values the module was compiled under (see
// moduleContextKeys). Everything else, such as cancellation and stdout,
// comes from ctx.
func (m *Module) context(ctx context.Context) context.Context {
	for i, key := range moduleContextKeys {
		if m.values[i] != nil {
			ctx = context.WithValue(ctx, key, m.values[i])
		}
	}
	return ctx
}

// parseFSBlocks is parseDirBlocks for a directory of an fs.FS: it parses the
// directory's .dang files, in name order, at the version of the dang.toml
// governing it, and returns their text by filename.
func parseFSBlocks(ctx context.Context, fsys fs.FS, dir string) (context.Context, []*FileBlock, Version, map[string]string, error) {
	if !fs.ValidPath(dir) {
		return ctx, nil, Version{}, nil, fmt.Errorf("invalid directory %q in fs.FS", dir)
	}
	configPath, cfg, err := findFSProjectConfig(fsys, dir)
	if err != nil {
		return ctx, nil, Version{}, nil, fmt.Errorf("finding dang.toml: %w", err)
	}
	if cfg != nil && len(cfg.Imports) > 0 && len(importConfigsFromContext(ctx)) == 0 {
		return ctx, nil, Version{}, nil, fmt.Errorf("%s declares imports, which can't be resolved from an fs.FS; supply them with ContextWithImportConfigs", configPath)
	}
	version := VersionForConfig(ctx, cfg)

	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return ctx, nil, Version{}, nil, err
	}
	var blocks []*FileBlock
	sources := map[string]string{}
	for _, entry := range entries { // sorted by name
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".dang") {
			continue
		}
		filename := path.Join(dir, entry.Name())
		source, err := fs.ReadFile(fsys, filename)
		if err != nil {
			return ctx, nil, Version{}, nil, err
		}
		parsed, err := parseSourceWithSyntaxCache(ctx, filename, source, version)
		if err != nil {
			return ctx, nil, Version{}, nil, fmt.Errorf("failed to parse file %s: %w", filename, err)
		}
		block, ok := parsed.(*FileBlock)
		if !ok {
			return ctx, nil, Version{}, nil, fmt.Errorf("parsed result for %s is not a FileBlock", filename)
		}
		blocks = append(blocks, block)
		sources[filename] = string(source)
	}
	return ctx, blocks, version, sources, nil
}

// findFSProjectConfig is FindProjectConfig within an fs.FS: the nearest
// dang.toml at or above dir, stopping at a .git or the FS's root.
func findFSProjectConfig(fsys fs.FS, dir string) (string, *ProjectConfig, error) {
	for {
		configPath := path.Join(dir, "dang.toml")
		data, err := fs.ReadFile(fsys, configPath)
		switch {
		case err == nil:
			var config ProjectConfig
			if _, err := toml.Decode(string(data), &config); err != nil {
				return "", nil, fmt.Errorf("parsing %s: %w", configPath, err)
			}
			return configPath, &config, nil
		case !errors.Is(err, fs.ErrNotExist):
			return "", nil, err
		}
		if _, err := fs.Stat(fsys, path.Join(dir, ".git")); err == nil || dir == "." {
			return "", nil, nil
		}
		dir = path.Dir(dir)
	}
}

// Session checks and evaluates snippets in a module's scope, REPL-style: as
// if each were one more file of the module, it sees all of the module's
// declarations, private ones included, and gets the module's auto-imports.
//
// A snippet joins the session once it evaluates successfully: later
// snippets then see its declarations and imports, which shadow the
// module's. A snippet that fails to check or to evaluate leaves the session
// as it was (apart from what its evaluation did to the outside world).
// Assigning a module binding rebinds it for the session only: the module
// itself is never changed by a session.
//
// A Session is not safe for concurrent use.
type Session struct {
	module *Module

	// types and values are the newest frames of the session, over the
	// module's environment. Each snippet that joins the session adds one.
	types  TypeScope
	values ValueScope

	// importTypes and importValues are the session's imports: auto-imports
	// and those its snippets write. They are looked up after the module's
	// declarations and the session's own, as a file's imports are.
	importTypes  *Type
	importValues ValueScope

	// sources holds the text of the module's files read from an FS and of
	// the snippets in the session, by filename, for reporting errors in
	// them. checked counts the snippets checked, to name unnamed ones.
	sources map[string]string
	checked int
}

// NewSession starts a session in the module's scope, with the module's
// auto-imports in it. It evaluates nothing but the imports, which call no
// API.
func (m *Module) NewSession(ctx context.Context) (*Session, error) {
	ctx = m.context(ctx)
	// The session's root value frame is sealed, so writes to the module's
	// bindings stay in the session. Its imports, like a file's, start with
	// the builtin imports (see builtin_imports.go).
	s := &Session{
		module:       m,
		types:        m.TypeEnv.Clone().(TypeScope),
		values:       m.ValueEnv.Derive(true),
		importTypes:  newFileImportsScope(),
		importValues: m.ValueEnv.Derive(true),
		sources:      maps.Clone(m.sources),
	}
	installBuiltinImportValues(s.importValues)
	if s.sources == nil {
		s.sources = map[string]string{}
	}

	imports := InjectAutoImports(ctx, nil)
	errs := &InferenceErrors{}
	if _, err := inferImportsPhaseResilient(ctx, imports, s.importTypes, hm.NewSimpleFresher(), errs); err != nil {
		errs.Add(err)
	}
	if errs.HasErrors() {
		return nil, errs
	}
	for _, imp := range imports {
		if _, err := EvalNode(ctx, s.importValues, imp); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Snippet is source type-checked in a session, ready to evaluate.
type Snippet struct {
	// Type is the type of the snippet's result: its last form, if that is
	// an expression or statement rather than a declaration. Nil otherwise.
	Type hm.Type

	session  *Session
	filename string
	source   string
	sources  map[string]string
	imports  []Node
	forms    []Node

	// base is the session frame the snippet was checked against, and types
	// and importTypes the frames it was checked in, which the session takes
	// on once the snippet evaluates.
	base        TypeScope
	types       TypeScope
	importTypes *Type

	evaluated bool
}

// defaultSnippetFilename names a snippet's source in errors when the caller
// gives it no name.
const defaultSnippetFilename = "<eval>"

// Check parses and type-checks source as the session's next snippet,
// without evaluating anything. filename names it in errors and positions;
// if empty, the session's first snippet is "<eval>", and later ones
// "<eval-2>", "<eval-3>" and so on. A type error is reported against the
// snippet's own source, at its position in the snippet.
func (s *Session) Check(ctx context.Context, filename, source string) (*Snippet, error) {
	s.checked++
	if filename == "" {
		filename = defaultSnippetFilename
		if s.checked > 1 {
			filename = fmt.Sprintf("<eval-%d>", s.checked)
		}
	}
	ctx = s.module.context(ctx)
	sources := maps.Clone(s.sources)
	sources[filename] = source

	parsed, err := ParseWithRecovery(filename, []byte(source), GlobalStore("filePath", filename), WithVersion(s.module.Version))
	if err != nil {
		return nil, err
	}
	block, ok := parsed.(*FileBlock)
	if !ok {
		return nil, fmt.Errorf("parsed result for %s is not a FileBlock", filename)
	}
	imports, forms := partitionImports(block.Forms)

	// Check in frames of the snippet's own, so a snippet that fails leaves
	// nothing behind in the session.
	snippet := &Snippet{
		session:     s,
		filename:    filename,
		source:      source,
		sources:     sources,
		imports:     imports,
		forms:       forms,
		base:        s.types,
		types:       s.types.Clone().(TypeScope),
		importTypes: s.importTypes.Clone().(*Type),
	}
	fresh := hm.NewSimpleFresher()
	errs := &InferenceErrors{}
	if _, err := inferImportsPhaseResilient(ctx, imports, snippet.importTypes, fresh, errs); err != nil {
		errs.Add(err)
	}
	if errs.HasErrors() {
		return nil, withSourceText(errs, sources)
	}
	env := &OverlayTypeScope{primary: snippet.types, lexical: snippet.importTypes}
	t, err := InferTopLevel(ctx, forms, env, fresh)
	if err != nil {
		return nil, withSourceText(err, sources)
	}
	if len(forms) > 0 && len(forms[len(forms)-1].DeclaredSymbols()) == 0 {
		snippet.Type = t
	}
	return snippet, nil
}

// Eval evaluates the snippet in its session and returns its result: the
// value of its last form if that is an expression or statement (see Type),
// else null. Forms run in source order, as at a REPL prompt, and the
// module's lazy top-level bindings they read are initialized on the way.
//
// A snippet evaluates at most once, and only while the session is as it was
// checked against: once another snippet has joined the session, check this
// one again.
func (sn *Snippet) Eval(ctx context.Context) (Value, error) {
	s := sn.session
	if sn.evaluated {
		return nil, fmt.Errorf("snippet %s was already evaluated", sn.filename)
	}
	if s.types != sn.base {
		return nil, fmt.Errorf("snippet %s was checked before the session changed; check it again", sn.filename)
	}
	sn.evaluated = true

	evalCtx := NewEvalContext(sn.filename, sn.source)
	evalCtx.sources = sn.sources
	ctx = WithEvalContext(s.module.context(ctx), evalCtx)
	fail := func(err error) (Value, error) {
		if translated, ok := translateBoundaryEvalError(err, evalCtx); ok {
			return nil, translated
		}
		return nil, err
	}

	importValues := s.importValues.Derive(false)
	// Sealed, so that assignments to the module's bindings or an earlier
	// snippet's land in this snippet's frame, which joins the session only
	// if the snippet succeeds.
	values := s.values.Derive(true)
	env := CreateOverlayValueScope(values, importValues)
	for _, imp := range sn.imports {
		if _, err := EvalNode(ctx, importValues, imp); err != nil {
			return fail(err)
		}
	}
	var result Value = NullValue{}
	for i, form := range sn.forms {
		val, err := EvalNode(ctx, env, form)
		if err != nil {
			return fail(err)
		}
		if i == len(sn.forms)-1 && len(form.DeclaredSymbols()) == 0 {
			result = val
		}
	}

	s.types, s.importTypes = sn.types, sn.importTypes
	s.values, s.importValues = values, importValues
	s.sources[sn.filename] = sn.source
	return result, nil
}

// Check type-checks source as a snippet in a fresh session of the module
// (see Session.Check).
func (m *Module) Check(ctx context.Context, filename, source string) (*Snippet, error) {
	s, err := m.NewSession(ctx)
	if err != nil {
		return nil, err
	}
	return s.Check(ctx, filename, source)
}

// Eval type-checks and evaluates source as a snippet in a fresh session of
// the module, returning its result and the result's type (see
// Snippet.Eval). Nothing runs if it fails to type-check.
func (m *Module) Eval(ctx context.Context, filename, source string) (Value, hm.Type, error) {
	snippet, err := m.Check(ctx, filename, source)
	if err != nil {
		return nil, nil, err
	}
	val, err := snippet.Eval(ctx)
	if err != nil {
		return nil, snippet.Type, err
	}
	return val, snippet.Type, nil
}

// withSourceText attaches the right source text to the located type errors
// in err whose files sources holds: the source of a snippet or of a file
// read from an fs.FS, which ConvertInferError, reading from disk, can't find
// (or finds the wrong file for).
func withSourceText(err error, sources map[string]string) error {
	if err == nil || len(sources) == 0 {
		return err
	}
	var errs *InferenceErrors
	if errors.As(err, &errs) {
		converted := &InferenceErrors{Errors: make([]error, len(errs.Errors)), poisoned: errs.poisoned}
		for i, e := range errs.Errors {
			converted.Errors[i] = withSourceText(e, sources)
		}
		return converted
	}
	var inferErr *InferError
	if errors.As(err, &inferErr) && inferErr.Location != nil {
		if source, ok := sources[inferErr.Location.Filename]; ok {
			return NewSourceError(inferErr, inferErr.Location, source)
		}
	}
	var sourceErr *SourceError
	if errors.As(err, &sourceErr) && sourceErr.Location != nil {
		if source, ok := sources[sourceErr.Location.Filename]; ok && source != sourceErr.Source {
			return NewSourceError(sourceErr.Inner, sourceErr.Location, source)
		}
	}
	return err
}
