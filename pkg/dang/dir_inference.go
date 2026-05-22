package dang

import (
	"context"
	"fmt"
	"slices"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
)

// InferDirectoryFiles runs phased type inference across multiple file blocks of
// a directory module with file-local import scopes. Each file's imports populate
// a fresh per-file env, which is then composed with the shared dirEnv: lookups
// fall through dirEnv → file imports, so local declarations correctly shadow
// imported names. Adds go to dirEnv so cross-file declarations are visible to
// siblings.
//
// Phases run in lockstep across files (one phase across all files before the
// next phase begins), matching the behavior of single-module phased inference.
// This makes cross-file references in signatures and bodies resolve regardless
// of file order: every type name is registered before any signature is checked,
// every signature is in place before any body is inferred, and so on.
//
// Auto-imports declared in ctx are injected into each block's Forms in place,
// so subsequent evaluation reuses the same *ImportDecl nodes whose Infer state
// (cached schema/client) was populated here.
func InferDirectoryFiles(ctx context.Context, files []*ModuleBlock, dirEnv Env, fresh hm.Fresher) error {
	overall := &InferenceErrors{}
	scopes := prepareFileScopes(ctx, files, dirEnv, fresh, overall)
	runDirectoryPhases(ctx, scopes, fresh, overall, inferencePhases)

	if overall.HasErrors() {
		return overall
	}
	return nil
}

// fileScope ties a file's rest-of-forms (everything except its imports) to the
// composite env that scopes its imports file-locally.
type fileScope struct {
	classified ClassifiedForms
	fileEnv    Env
}

// prepareFileScopes per-file: prepends auto-imports, splits imports from the
// rest, runs the imports phase against a fresh per-file env, and builds the
// file's composite env. The file-local imports are installed once up front so
// that all subsequent phases can refer to them through fileEnv. The composite
// is also stashed on block.Env so editor features can look up symbols (e.g.
// unqualified imported names) against the same env inference saw.
//
// Schema modules are deduplicated across files: every file that imports the
// same name gets the same *Module pointer, so types like Dagger.Workspace
// unify across file boundaries. Without this, each ImportDecl would build its
// own module via NewEnv and unification would fail on identity even when the
// schemas are identical.
func prepareFileScopes(ctx context.Context, files []*ModuleBlock, dirEnv Env, fresh hm.Fresher, errs *InferenceErrors) []fileScope {
	for _, block := range files {
		block.Forms = prependAutoImports(ctx, block.Forms)
	}
	shareImportModules(ctx, files, errs)

	scopes := make([]fileScope, 0, len(files))
	for _, block := range files {
		imports, rest := partitionImports(block.Forms)

		importsEnv := NewModule("", ObjectKind)
		if _, err := inferImportsPhaseResilient(ctx, imports, importsEnv, fresh, errs); err != nil {
			errs.Add(err)
		}

		fileEnv := &CompositeModule{primary: dirEnv, lexical: importsEnv}
		block.Env = fileEnv
		scopes = append(scopes, fileScope{
			classified: classifyForms(rest),
			fileEnv:    fileEnv,
		})
	}
	return scopes
}

// shareImportModules pre-populates ImportDecl.inferred (and the schema/client
// fields used by Eval) so that every ImportDecl with the same name across the
// given files points at one shared *Module. If any ImportDecl already has
// inferred state from a previous call (e.g. via the LSP's parse cache), that
// existing module wins — across calls, identities are preserved so newly added
// files snap to the existing module rather than producing a divergent build.
func shareImportModules(ctx context.Context, files []*ModuleBlock, errs *InferenceErrors) {
	type sharedImport struct {
		mod    Env
		client graphql.Client
		schema *introspection.Schema
	}
	shared := make(map[string]sharedImport)

	for _, block := range files {
		for _, form := range block.Forms {
			imp, ok := form.(*ImportDecl)
			if !ok || imp.Name == nil {
				continue
			}
			name := imp.Name.Name
			if _, done := shared[name]; done {
				continue
			}
			if imp.inferred != nil {
				shared[name] = sharedImport{mod: imp.inferred, client: imp.client, schema: imp.schema}
				continue
			}
			config, err := imp.loadImportConfig(ctx)
			if err != nil {
				errs.Add(err)
				continue
			}
			shared[name] = sharedImport{
				mod:    NewEnv(name, config.Schema),
				client: config.Client,
				schema: config.Schema,
			}
		}
	}

	for _, block := range files {
		for _, form := range block.Forms {
			imp, ok := form.(*ImportDecl)
			if !ok || imp.Name == nil {
				continue
			}
			s, ok := shared[imp.Name.Name]
			if !ok {
				continue
			}
			imp.inferred = s.mod
			imp.client = s.client
			imp.schema = s.schema
		}
	}
}

// directoryPhase runs a single phase against every file's forms in order. The
// phase function picks the relevant slice of classified forms for the phase
// (e.g. types, variables) so all phases share one signature.
type directoryPhase struct {
	name string
	fn   func(ctx context.Context, classified ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error)
}

func runDirectoryPhases(ctx context.Context, scopes []fileScope, fresh hm.Fresher, errs *InferenceErrors, phases []directoryPhase) {
	for _, phase := range phases {
		for _, scope := range scopes {
			if _, err := phase.fn(ctx, scope.classified, scope.fileEnv, fresh, errs); err != nil {
				errs.Add(fmt.Errorf("%s phase failed: %w", phase.name, err))
			}
		}
	}
}

// declarationPhases is the subset of phases that build the directory's public
// API. Body inference is skipped, matching DeclareFormsWithPhases. Phase order
// is critical: type names → signatures.
var declarationPhases = []directoryPhase{
	{"type names", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
		return inferTypeNamesPhaseResilient(ctx, c.Types, env, fresh, errs)
	}},
	{"directives", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
		return inferDirectivesPhaseResilient(ctx, c.Directives, env, fresh, errs)
	}},
	{"constants", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
		return inferConstantsPhaseResilient(ctx, c.Constants, env, fresh, errs)
	}},
	{"variable signatures", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
		return declareVariableSignaturesPhaseResilient(ctx, c.Variables, env, fresh, errs)
	}},
	{"type signatures", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
		return declareTypeSignaturesPhaseResilient(ctx, c.Types, env, fresh, errs)
	}},
	{"function signatures", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
		return declareFunctionSignaturesPhaseResilient(ctx, c.Functions, env, fresh, errs)
	}},
}

// variablePhase is broken out (rather than living inline in bodyPhases) so
// the LSP focused path can run it for sibling files without paying for the
// rest of body inference. Siblings need it because declaration-phase
// signatures don't cover `pub answer = expr` (no annotation) — without the
// variables phase, the active file's references to such a sibling export
// show up as "not found".
var variablePhase = []directoryPhase{
	{"variables", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
		return inferVariablesPhaseResilient(ctx, c.Variables, env, fresh, errs)
	}},
}

// bodyPhases are the post-declaration phases that walk into bodies. Order
// matters: type bodies first (so method return types are pinned), then
// variables (which may reference method types), then function bodies, then
// non-declarations.
var bodyPhases = slices.Concat(
	[]directoryPhase{
		{"type bodies", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
			return inferTypeBodiesPhaseResilient(ctx, c.Types, env, fresh, errs)
		}},
	},
	variablePhase,
	[]directoryPhase{
		{"function bodies", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
			return inferFunctionBodiesPhaseResilient(ctx, c.Functions, env, fresh, errs)
		}},
		{"non-declarations", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
			return inferNonDeclarationsPhaseResilient(ctx, c.NonDeclarations, env, fresh, errs)
		}},
	},
)

// inferencePhases mirrors InferFormsWithPhases but with each phase reachable
// per-file. Phase order is critical: declarations before bodies.
var inferencePhases = slices.Concat(declarationPhases, bodyPhases)

// InferDirectoryFilesFocused runs phased inference with full body checks only
// for the active file. Sibling files run the declaration phases plus the
// variables phase (needed to publish types of unannotated exports) but skip
// type-body, function-body, and non-declaration phases. Used by the LSP to
// keep keystroke-driven analysis cheap without losing sibling visibility.
//
// active must be one of files (pointer equality). A nil active runs only the
// declaration phases (plus variables across all scopes).
func InferDirectoryFilesFocused(ctx context.Context, files []*ModuleBlock, active *ModuleBlock, dirEnv Env, fresh hm.Fresher) error {
	overall := &InferenceErrors{}
	scopes := prepareFileScopes(ctx, files, dirEnv, fresh, overall)

	runDirectoryPhases(ctx, scopes, fresh, overall, declarationPhases)

	// Identify the active scope (if any) so we can run variable inference for
	// siblings only and run the rest of body inference for the active scope.
	var activeIdx = -1
	if active != nil {
		for i, block := range files {
			if block == active {
				activeIdx = i
				break
			}
		}
	}

	for i, scope := range scopes {
		if i == activeIdx {
			continue
		}
		runDirectoryPhases(ctx, []fileScope{scope}, fresh, overall, variablePhase)
	}

	if activeIdx >= 0 {
		runDirectoryPhases(ctx, scopes[activeIdx:activeIdx+1], fresh, overall, bodyPhases)
	}

	if overall.HasErrors() {
		return overall
	}
	return nil
}

// partitionImports splits forms into ImportDecls and everything else, preserving
// original order within each partition.
func partitionImports(forms []Node) (imports, rest []Node) {
	for _, f := range forms {
		if _, ok := f.(*ImportDecl); ok {
			imports = append(imports, f)
		} else {
			rest = append(rest, f)
		}
	}
	return
}

// prependAutoImports prepends synthetic ImportDecls for any auto-import configs
// in ctx that aren't already explicitly imported in forms. Used per-file so that
// each file gets the auto-imports it needs without leaking siblings' imports.
func prependAutoImports(ctx context.Context, forms []Node) []Node {
	configs := importConfigsFromContext(ctx)
	if len(configs) == 0 {
		return forms
	}

	existing := make(map[string]bool)
	for _, form := range forms {
		if imp, ok := form.(*ImportDecl); ok && imp.Name != nil {
			existing[imp.Name.Name] = true
		}
	}

	var injected []Node
	for _, config := range configs {
		if config.AutoImport && !existing[config.Name] {
			injected = append(injected, &ImportDecl{
				Name: &Symbol{Name: config.Name},
			})
		}
	}

	if len(injected) == 0 {
		return forms
	}
	return append(injected, forms...)
}

