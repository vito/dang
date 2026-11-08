// Package analysis is Dang's language service without a transport: it parses
// and type-checks a directory of Dang source read from an fs.FS, and answers
// questions about the result, such as its diagnostics.
//
// The LSP (pkg/lsp) is an adapter over it: it converts its own positions and
// paths, and handles what only it does (tracking open buffers, resolving
// dang.toml imports, which can start services). Analysis itself reads source
// only from its Source, never starts a process, and reports 1-based
// positions in plain structs that serialize cleanly; the imports a caller
// hands it are used as given (see Options.ImportConfigs).
package analysis

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
)

// Source is the file system Dang source is read from. Paths in it are
// slash-separated and unrooted, as io/fs requires. The engine passes its
// mounted workspace; the LSP passes the disk with its open buffers laid over
// it.
//
// A file whose fs.FileInfo reports a modification time is assumed unchanged
// while its time and size are, which lets a ParseCache skip reading it.
type Source interface {
	fs.FS
}

// Options configures an analysis.
type Options struct {
	// Root names the files of the Source in positions and messages: a file
	// at path p is named filepath.Join(Root, p). The LSP sets it to the
	// directory its Source is rooted at, so errors name real files on disk.
	// Empty names files by their Source path. Diagnostics report Source paths
	// either way.
	Root string

	// Focus, when set, is the Source path of the one file to check in full:
	// sibling files contribute their declarations, so cross-file references
	// resolve, but their bodies are not inferred and only Focus's diagnostics
	// are reported. That keeps re-analysis on every keystroke cheap. Empty
	// infers and reports every file.
	Focus string

	// ImportConfigs are the imports the source can name, e.g. the engine's
	// Dagger import. Analysis doesn't resolve dang.toml imports itself: that
	// may start services, which is up to the caller (the LSP does it).
	//
	// They are used as given. In particular a DangModuleDir import (a
	// dang.toml `path =` import) is compiled from the host file system, not
	// the Source, along with whatever its own dang.toml imports.
	ImportConfigs []dang.ImportConfig

	// ResolveImports, when set, is called for more import configs, only once
	// the analysis is about to infer: not when the Focus file fails to
	// parse. A caller whose imports are costly to resolve (the LSP's can
	// start a `dagger session`) resolves them here, so a syntax error is
	// reported without waiting for them.
	ResolveImports func(context.Context) []dang.ImportConfig

	// SchemaModuleCache shares imported schemas' modules across analyses (see
	// dang.WithSchemaModuleCache). Types like Test.ServerInfo only unify when
	// every file's import yields the same *dang.Type, so a caller that
	// re-analyzes a directory, or analyzes files of it one at a time, must
	// pass the same cache each time. Nil uses a fresh one.
	SchemaModuleCache *sync.Map

	// Version is the Dang version the source is parsed as. Zero means
	// dang.VersionFromContext.
	Version dang.Version

	// Parses, when set, reuses parses of unchanged files across analyses.
	Parses *ParseCache
}

// File is one analyzed source file.
type File struct {
	// Path is the file's path in the Source.
	Path string

	// Name is what positions and messages call the file (see Options.Root).
	Name string

	// Text is the file's contents.
	Text string

	// Block is the parsed and type-annotated file, or nil if it failed to
	// parse.
	Block *dang.FileBlock
}

// Analysis is the result of analyzing a directory.
type Analysis struct {
	// Dir is the analyzed directory, as a path in the Source.
	Dir string

	// Focus is Options.Focus.
	Focus string

	// Files are the directory's .dang files, sorted by path.
	Files []*File

	// Scope is the directory's type scope: the declarations of every file,
	// over the prelude. Nil when the focus file failed to parse, since then
	// nothing was inferred.
	Scope dang.TypeScope

	diagnostics []Diagnostic
}

// Analyze parses and type-checks the .dang files of dir in src.
//
// Parse and type errors don't fail the analysis: they are its Diagnostics. An
// error reading src does.
func Analyze(ctx context.Context, src Source, dir string, opts Options) (*Analysis, error) {
	dir = path.Clean(dir)
	if opts.Focus != "" {
		opts.Focus = path.Clean(opts.Focus)
	}
	a := &Analysis{Dir: dir, Focus: opts.Focus}

	version := opts.Version
	if version.IsZero() {
		version = dang.VersionFromContext(ctx)
	}

	paths, err := dangFiles(src, dir)
	if err != nil {
		return nil, err
	}
	if opts.Focus != "" && !slices.Contains(paths, opts.Focus) {
		return nil, fmt.Errorf("analyze %s: focus %s is not one of its .dang files", dir, opts.Focus)
	}

	byName := make(map[string]string, len(paths))
	pathOf := func(filename string) (string, bool) {
		p, ok := byName[filepath.Clean(filename)]
		if !ok || (opts.Focus != "" && p != opts.Focus) {
			return "", false
		}
		return p, true
	}

	var blocks []*dang.FileBlock
	var focus *dang.FileBlock
	for _, p := range paths {
		name := p
		if opts.Root != "" {
			name = filepath.Join(opts.Root, filepath.FromSlash(p))
		}
		byName[filepath.Clean(name)] = p

		block, text, err := parse(src, p, name, version, opts.Parses)
		if err != nil {
			var perr parseError
			if !errors.As(err, &perr) {
				return nil, err
			}
			// A file that fails to parse is reported and otherwise left out,
			// so the rest of the directory still analyzes.
			if opts.Focus == "" || p == opts.Focus {
				a.diagnostics = append(a.diagnostics, errorDiagnostics(perr.err, CodeSyntax, p, func(string) (string, bool) {
					return p, true
				})...)
			}
			a.Files = append(a.Files, &File{Path: p, Name: name, Text: text})
			continue
		}
		a.Files = append(a.Files, &File{Path: p, Name: name, Text: text, Block: block})
		blocks = append(blocks, block)
		if p == opts.Focus {
			focus = block
		}
	}

	if opts.Focus != "" && focus == nil {
		// Nothing to check: the focus file doesn't parse.
		return a, nil
	}

	imports := opts.ImportConfigs
	if opts.ResolveImports != nil {
		imports = append(slices.Clip(imports), opts.ResolveImports(ctx)...)
	}

	if opts.SchemaModuleCache != nil {
		// Attached before the import configs, so that they find it instead
		// of making a fresh one.
		ctx = dang.WithSchemaModuleCache(ctx, opts.SchemaModuleCache)
	}
	if len(imports) > 0 {
		ctx = dang.ContextWithImportConfigs(ctx, imports...)
	}

	// Collect inference warnings (e.g. the rescue laziness analysis) instead
	// of printing them; they are reported as diagnostics.
	ctx, warnings := dang.WithInferWarningSink(ctx)

	scope := dang.NewPreludeTypeScope("")
	fresh := hm.NewSimpleFresher()
	if focus != nil {
		err = dang.InferDirectoryFilesFocused(ctx, blocks, focus, scope, fresh)
	} else {
		err = dang.InferDirectoryFiles(ctx, blocks, scope, fresh)
	}
	a.Scope = scope
	if err != nil {
		a.diagnostics = append(a.diagnostics, errorDiagnostics(err, CodeType, opts.Focus, pathOf)...)
	}
	a.diagnostics = append(a.diagnostics, warningDiagnostics(warnings.Take(), opts.Focus, pathOf)...)

	for _, f := range a.Files {
		if f.Block != nil && (opts.Focus == "" || f.Path == opts.Focus) {
			a.diagnostics = append(a.diagnostics, deprecationDiagnostics(f.Block, f.Path)...)
		}
	}

	return a, nil
}

// Diagnostics returns the problems found: parse errors, type errors,
// inference warnings and deprecated calls, in that order for each file. With
// a Focus, only the focus file's.
func (a *Analysis) Diagnostics() []Diagnostic {
	return append([]Diagnostic(nil), a.diagnostics...)
}

// File returns the analyzed file at path in the Source, or nil.
func (a *Analysis) File(path string) *File {
	for _, f := range a.Files {
		if f.Path == path {
			return f
		}
	}
	return nil
}

// FileScope returns the type scope a file's top level sees: the directory's
// declarations with the file's own imports. Nil if nothing was inferred.
func (a *Analysis) FileScope(path string) dang.TypeScope {
	if a.Scope == nil {
		return nil
	}
	if f := a.File(path); f != nil && f.Block != nil && f.Block.TypeScope != nil {
		return f.Block.TypeScope
	}
	return a.Scope
}

// dangFiles lists the .dang files directly in dir, sorted. A directory that
// doesn't exist has none.
func dangFiles(src Source, dir string) ([]string, error) {
	entries, err := fs.ReadDir(src, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list .dang files in %s: %w", dir, err)
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".dang") {
			continue
		}
		paths = append(paths, path.Join(dir, entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}
