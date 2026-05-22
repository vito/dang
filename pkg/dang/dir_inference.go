package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/pkg/hm"
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
// that all subsequent phases can refer to them through fileEnv.
func prepareFileScopes(ctx context.Context, files []*ModuleBlock, dirEnv Env, fresh hm.Fresher, errs *InferenceErrors) []fileScope {
	scopes := make([]fileScope, 0, len(files))

	for _, block := range files {
		block.Forms = prependAutoImports(ctx, block.Forms)
		imports, rest := partitionImports(block.Forms)

		importsEnv := NewModule("", ObjectKind)
		inferImportsPhaseResilient(ctx, imports, importsEnv, fresh, errs)

		fileEnv := &CompositeModule{primary: dirEnv, lexical: importsEnv}
		scopes = append(scopes, fileScope{
			classified: classifyForms(rest),
			fileEnv:    fileEnv,
		})
	}
	return scopes
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

// inferencePhases mirrors InferFormsWithPhases but with each phase reachable
// per-file. Phase order is critical: type names → signatures → bodies.
var inferencePhases = []directoryPhase{
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
	{"type bodies", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
		return inferTypeBodiesPhaseResilient(ctx, c.Types, env, fresh, errs)
	}},
	{"variables", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
		return inferVariablesPhaseResilient(ctx, c.Variables, env, fresh, errs)
	}},
	{"function bodies", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
		return inferFunctionBodiesPhaseResilient(ctx, c.Functions, env, fresh, errs)
	}},
	{"non-declarations", func(ctx context.Context, c ClassifiedForms, env Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
		return inferNonDeclarationsPhaseResilient(ctx, c.NonDeclarations, env, fresh, errs)
	}},
}

// declarationPhases is the subset of phases that build the directory's public
// API. Body inference is skipped, matching DeclareFormsWithPhases.
var declarationPhases = inferencePhases[:6]

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

