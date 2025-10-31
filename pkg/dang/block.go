package dang

import (
	"context"
	"errors"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

type Block struct {
	InferredTypeHolder
	Forms []Node
	Loc   *SourceLocation

	// Filled in during inference phase for non-inline blocks
	Env Env
}

var _ hm.Expression = (*Block)(nil)
var _ Evaluator = (*Block)(nil)
var _ Node = (*Block)(nil)

func (b *Block) DeclaredSymbols() []string {
	return nil // Blocks don't declare symbols directly (their forms do)
}

func (b *Block) ReferencedSymbols() []string {
	var symbols []string

	// Add symbols from all forms in the block
	for _, form := range b.Forms {
		symbols = append(symbols, form.ReferencedSymbols()...)
	}

	return symbols
}

func (f *Block) Body() hm.Expression { return f }

func (f *Block) GetSourceLocation() *SourceLocation { return f.Loc }

type Hoister interface {
	Hoist(context.Context, hm.Env, hm.Fresher, int) error
}

var _ Hoister = (*Block)(nil)

type Declarer interface {
	IsDeclarer() bool
}

func (b *Block) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, depth int) error {
	newEnv := env.Clone()
	var errs []error
	for _, form := range b.Forms {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, newEnv, fresh, depth); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// orderByDependencies orders the declarers using topological sort based on dependencies
func orderByDependencies(declarers []Node) ([]Node, error) {
	if len(declarers) <= 1 {
		return declarers, nil
	}

	// Build dependency graph
	declared := make(map[string]int)    // symbol name -> index in declarers
	dependencies := make(map[int][]int) // declarer index -> list of indices it depends on

	// First pass: collect what each declarer declares
	for i, declarer := range declarers {
		names := getDeclarationNames(declarer)
		for _, name := range names {
			declared[name] = i
		}
	}

	// Second pass: collect what each declarer depends on
	for i, declarer := range declarers {
		refs := getSymbolReferences(declarer)
		for _, ref := range refs {
			if depIndex, exists := declared[ref]; exists && depIndex != i {
				dependencies[i] = append(dependencies[i], depIndex)
			}
		}
	}

	// Topological sort
	return topologicalSort(declarers, dependencies)
}

// getDeclarationNames extracts the symbol names that a declarer introduces
func getDeclarationNames(node Node) []string {
	return node.DeclaredSymbols()
}

// getSymbolReferences extracts all symbol references in a node's value/body
func getSymbolReferences(node Node) []string {
	return node.ReferencedSymbols()
}

// extractSymbols is deprecated - use node.ReferencedSymbols() instead
// This function is kept for backward compatibility but should not be used in new code

// topologicalSort performs Kahn's algorithm for topological sorting
func topologicalSort(nodes []Node, dependencies map[int][]int) ([]Node, error) {
	n := len(nodes)
	inDegree := make([]int, n)

	// Calculate in-degrees
	for dependent, deps := range dependencies {
		inDegree[dependent] = len(deps)
	}

	// Start with nodes that have no dependencies
	queue := []int{}
	for i := range n {
		if inDegree[i] == 0 {
			queue = append(queue, i)
		}
	}

	var result []Node
	processed := 0

	for len(queue) > 0 {
		// Remove node from queue
		current := queue[0]
		queue = queue[1:]
		result = append(result, nodes[current])
		processed++

		// For each node that depends on current, reduce its in-degree
		for dependent, deps := range dependencies {
			for _, dep := range deps {
				if dep == current {
					inDegree[dependent]--
					if inDegree[dependent] == 0 {
						queue = append(queue, dependent)
					}
				}
			}
		}
	}

	// Check for cycles
	if processed != n {
		return nil, errors.New("circular dependency detected in declarations")
	}

	return result, nil
}

// OrderFormsByDependencies reorders the forms in a block based on dependencies
func OrderFormsByDependencies(forms []Node) ([]Node, error) {
	var declarers, nonDeclarers []Node
	for _, form := range forms {
		if declarer, ok := form.(Declarer); ok {
			if declarer.IsDeclarer() {
				declarers = append(declarers, form)
			} else {
				nonDeclarers = append(nonDeclarers, form)
			}
		} else {
			nonDeclarers = append(nonDeclarers, form)
		}
	}

	// Order declarers by their dependencies
	orderedDeclarers, err := orderByDependencies(declarers)
	if err != nil {
		return nil, err
	}

	// Return the ordered forms
	result := append(orderedDeclarers, nonDeclarers...)
	return result, nil
}

// ClassifiedForms holds forms categorized by their compilation phase
type ClassifiedForms struct {
	Imports         []Node // ImportDecl (must be processed before anything else)
	Directives      []Node // DirectiveDecl (must be processed first after imports)
	Constants       []Node // SlotDecl with constant values (literals, no function calls)
	Types           []Node // ClassDecl
	Variables       []Node // SlotDecl with computed values (function calls, references)
	Functions       []Node // FunDecl and SlotDecl with function bodies
	NonDeclarations []Node // Everything else (assignments, expressions, etc.)
}

// classifyForms separates forms by their compilation phase requirements
func classifyForms(forms []Node) ClassifiedForms {
	var classified ClassifiedForms

	for _, form := range forms {
		switch f := form.(type) {
		case *ImportDecl:
			classified.Imports = append(classified.Imports, f)
		case *DirectiveDecl:
			classified.Directives = append(classified.Directives, f)
		case *InterfaceDecl:
			classified.Types = append(classified.Types, f)
		case *ClassDecl:
			classified.Types = append(classified.Types, f)
		case *EnumDecl:
			classified.Types = append(classified.Types, f)
		case *ScalarDecl:
			classified.Types = append(classified.Types, f)
		case *SlotDecl:
			if isConstantValue(f.Value) {
				classified.Constants = append(classified.Constants, f)
			} else if _, isFunDecl := f.Value.(*FunDecl); isFunDecl {
				// Treat SlotDecl with function bodies as functions for proper hoisting
				classified.Functions = append(classified.Functions, f)
			} else {
				classified.Variables = append(classified.Variables, f)
			}
		case *FunDecl:
			classified.Functions = append(classified.Functions, f)
		default:
			// All non-declarations (assignments, expressions, assertions, etc.)
			classified.NonDeclarations = append(classified.NonDeclarations, form)
		}
	}

	return classified
}

// InferFormsWithPhases implements phased compilation:
// 1. Parse all files (already done)
// 2. Build dependency graph of all declarations
// 3. Typecheck constants and types (which can reference each other)
// 4. Declare function signatures (without bodies)
// 5. Typecheck variables in dependency order (can now reference function signatures)
// 6. Typecheck function bodies last (can reference all package-level declarations)
//
// This function collects all errors instead of failing fast, allowing partial inference
// to succeed and providing better error messages showing all problems at once.
func InferFormsWithPhases(ctx context.Context, forms []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	errs := &InferenceErrors{}
	classified := classifyForms(forms)

	phases := []struct {
		name string
		fn   func(*InferenceErrors) (hm.Type, error)
	}{
		{"imports", func(errs *InferenceErrors) (hm.Type, error) {
			return inferImportsPhaseResilient(ctx, classified.Imports, env, fresh, errs)
		}},
		{"directives", func(errs *InferenceErrors) (hm.Type, error) {
			return inferDirectivesPhaseResilient(ctx, classified.Directives, env, fresh, errs)
		}},
		{"constants", func(errs *InferenceErrors) (hm.Type, error) {
			return inferConstantsPhaseResilient(ctx, classified.Constants, env, fresh, errs)
		}},
		{"types", func(errs *InferenceErrors) (hm.Type, error) {
			return inferTypesPhaseResilient(ctx, classified.Types, env, fresh, errs)
		}},
		{"function signatures", func(errs *InferenceErrors) (hm.Type, error) {
			return inferFunctionSignaturesPhaseResilient(ctx, classified.Functions, env, fresh, errs)
		}},
		{"variables", func(errs *InferenceErrors) (hm.Type, error) {
			return inferVariablesPhaseResilient(ctx, classified.Variables, env, fresh, errs)
		}},
		{"function bodies", func(errs *InferenceErrors) (hm.Type, error) {
			return inferFunctionBodiesPhaseResilient(ctx, classified.Functions, env, fresh, errs)
		}},
		{"non-declarations", func(errs *InferenceErrors) (hm.Type, error) {
			return inferNonDeclarationsPhaseResilient(ctx, classified.NonDeclarations, env, fresh, errs)
		}},
	}

	var lastT hm.Type
	for _, phase := range phases {
		t, err := phase.fn(errs)
		if err != nil {
			// Critical error that prevents continuing this phase
			errs.Add(fmt.Errorf("%s phase failed: %w", phase.name, err))
		}
		if t != nil {
			lastT = t
		}
	}

	if errs.HasErrors() {
		return lastT, errs
	}
	return lastT, nil
}

// isConstantValue determines if a value expression is a compile-time constant
func isConstantValue(value Node) bool {
	if value == nil {
		return true // Type-only declarations
	}

	switch value.(type) {
	case *String, *Int, *Boolean, *Null:
		return true
	default:
		return false
	}
}

// EvaluateFormsWithPhases evaluates forms using the same phased approach as inference.
// This ensures that constants, types, functions, and variables are evaluated in the correct order.
func EvaluateFormsWithPhases(ctx context.Context, forms []Node, env EvalEnv) (Value, error) {
	var result Value = NullValue{}
	var err error

	// Classify forms by their compilation requirements
	classified := classifyForms(forms)
	//
	// Phase 1: Evaluate imports
	for _, form := range classified.Imports {
		result, err = EvalNode(ctx, env, form)
		if err != nil {
			return nil, fmt.Errorf("import evaluation failed: %w", err)
		}
	}

	// Phase 2: Evaluate directives (must be available before any usage)
	for _, form := range classified.Directives {
		_, err = EvalNode(ctx, env, form)
		if err != nil {
			return nil, fmt.Errorf("directive evaluation failed: %w", err)
		}
	}

	// Phase 3: Evaluate constants (can be in any order, no dependencies)
	for _, form := range classified.Constants {
		_, err = EvalNode(ctx, env, form)
		if err != nil {
			return nil, fmt.Errorf("constant evaluation failed: %w", err)
		}
	}

	// Phase 4: Evaluate types (classes)
	for _, form := range classified.Types {
		_, err = EvalNode(ctx, env, form)
		if err != nil {
			return nil, fmt.Errorf("type evaluation failed: %w", err)
		}
	}

	// Phase 5: Evaluate functions (establish function values in environment)
	for _, form := range classified.Functions {
		_, err = EvalNode(ctx, env, form)
		if err != nil {
			return nil, fmt.Errorf("function evaluation failed: %w", err)
		}
	}

	// Phase 6: Evaluate variables in dependency order
	if len(classified.Variables) > 0 {
		orderedVars, err := orderByDependencies(classified.Variables)
		if err != nil {
			return nil, fmt.Errorf("variable dependency ordering failed: %w", err)
		}

		for _, form := range orderedVars {
			_, err = EvalNode(ctx, env, form)
			if err != nil {
				return nil, fmt.Errorf("variable evaluation failed: %w", err)
			}
		}
	}

	// Phase 7: Evaluate non-declarations in original order (assignments, expressions, etc.)
	for _, form := range classified.NonDeclarations {
		result, err = EvalNode(ctx, env, form)
		if err != nil {
			return nil, fmt.Errorf("non-declaration evaluation failed: %w", err)
		}
	}

	return result, nil
}

var _ hm.Inferer = (*Block)(nil)

func (b *Block) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(b, func() (hm.Type, error) {
		newEnv := env.Clone()

		// Store the environment, even if inference fails
		if dangEnv, ok := newEnv.(Env); ok {
			b.Env = dangEnv
		}

		forms := b.Forms
		if len(forms) == 0 {
			forms = append(forms, &Null{})
		}

		// Collect all inference errors rather than bailing on the first sign of
		// trouble, so that the LSP has something to work with
		errs := &InferenceErrors{}

		var typ hm.Type
		var err error
		for _, form := range forms {
			if inferer, ok := form.(hm.Inferer); ok {
				typ, err = inferer.Infer(ctx, newEnv, fresh)
				if err != nil {
					errs.Add(err)
				}
			}
		}

		if errs.HasErrors() {
			// Return all collected inference errors
			return nil, errs
		}

		// Set inferred type only if we're able to fully infer
		b.SetInferredType(typ)

		return typ, nil
	})
}

func (b *Block) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	forms := b.Forms
	if len(forms) == 0 {
		return NullValue{}, nil
	}

	newEnv := env.Clone()

	// Blocks evaluate forms in textual order
	var result Value = NullValue{}
	for _, form := range forms {
		val, err := EvalNode(ctx, newEnv, form)
		if err != nil {
			return nil, err
		}
		result = val
	}
	return result, nil
}

func (b *Block) Walk(fn func(Node) bool) {
	if !fn(b) {
		return
	}
	for _, form := range b.Forms {
		form.Walk(fn)
	}
}

type Object struct {
	InferredTypeHolder
	Slots []*SlotDecl
	Loc   *SourceLocation

	// Filled in during inference phase
	// This is a little weird but has come up twice, maybe OK pattern?
	// Requires mutating node in-place.
	Mod *Module
}

var _ Node = &Object{}

func (o *Object) DeclaredSymbols() []string {
	return nil // Objects don't declare symbols in the global scope
}

func (o *Object) ReferencedSymbols() []string {
	var symbols []string
	// Objects reference symbols from their slots
	for _, slot := range o.Slots {
		symbols = append(symbols, slot.ReferencedSymbols()...)
	}
	return symbols
}

func (f *Object) Body() hm.Expression { return f }

func (f *Object) GetSourceLocation() *SourceLocation { return f.Loc }

var _ hm.Inferer = &Object{}

func (o *Object) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod := NewModule("")
	inferEnv := &CompositeModule{
		primary: mod,
		lexical: env.(Env),
	}
	for _, slot := range o.Slots {
		_, err := slot.Infer(ctx, inferEnv, fresh)
		if err != nil {
			return nil, err
		}
	}
	o.Mod = mod
	return hm.NonNullType{Type: mod}, nil
}

var _ Evaluator = &Object{}

func (o *Object) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	if o.Mod == nil {
		return nil, errors.New("object has no module inferred")
	}
	newMod := NewModuleValue(o.Mod)
	evalEnv := CreateCompositeEnv(newMod, env)
	for _, slot := range o.Slots {
		_, err := EvalNode(ctx, evalEnv, slot)
		if err != nil {
			return nil, err
		}
	}
	return newMod, nil
}

func (o *Object) Walk(fn func(Node) bool) {
	if !fn(o) {
		return
	}
	for _, slot := range o.Slots {
		slot.Walk(fn)
	}
}

// Resilient phase functions for LSP - continue past errors

func inferImportsPhaseResilient(ctx context.Context, imports []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	var lastT hm.Type
	for _, form := range imports {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 0); err != nil {
				errs.Add(fmt.Errorf("import hoisting failed for %v: %w", form, err))
				continue
			}
		}
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			errs.Add(fmt.Errorf("import inference failed for %v: %w", form, err))
			continue
		}
		lastT = t
	}
	return lastT, nil
}

func inferDirectivesPhaseResilient(ctx context.Context, directives []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	var lastT hm.Type
	for _, form := range directives {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 0); err != nil {
				errs.Add(fmt.Errorf("directive hoisting failed for %v: %w", form, err))
				continue
			}
		}
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			errs.Add(fmt.Errorf("directive inference failed for %v: %w", form, err))
			continue
		}
		lastT = t
	}
	return lastT, nil
}

func inferConstantsPhaseResilient(ctx context.Context, constants []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	var lastT hm.Type
	for _, form := range constants {
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			errs.Add(fmt.Errorf("constant inference failed for %v: %w", form, err))
			assignFallbackType(form, env, fresh)
			continue
		}
		lastT = t
	}
	return lastT, nil
}

func inferTypesPhaseResilient(ctx context.Context, types []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	// Pass 0: Create class types
	for _, form := range types {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 0); err != nil {
				errs.Add(fmt.Errorf("type hoisting (pass 0) failed for %T: %w", form, err))
				// Continue to try other types
			}
		}
	}

	// Pass 1: Infer class bodies
	for _, form := range types {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 1); err != nil {
				errs.Add(fmt.Errorf("type hoisting (pass 1) failed for %T: %w", form, err))
				// Continue to try other types
			}
		}
	}

	// Complete type inference
	var lastT hm.Type
	for _, form := range types {
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			errs.Add(err)
			assignFallbackType(form, env, fresh)
			continue
		}
		lastT = t
	}
	return lastT, nil
}

func inferFunctionSignaturesPhaseResilient(ctx context.Context, functions []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	for _, form := range functions {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 0); err != nil {
				errs.Add(err)
				// Continue to try other functions
			}
		}
	}
	return nil, nil
}

func inferVariablesPhaseResilient(ctx context.Context, variables []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	if len(variables) == 0 {
		return nil, nil
	}

	orderedVars, err := orderByDependencies(variables)
	if err != nil {
		// Can't continue if we can't order dependencies - return critical error
		return nil, fmt.Errorf("variable dependency ordering failed: %w", err)
	}

	var lastT hm.Type
	for _, form := range orderedVars {
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			errs.Add(err)
			assignFallbackType(form, env, fresh)
			continue
		}
		lastT = t
	}
	return lastT, nil
}

func inferFunctionBodiesPhaseResilient(ctx context.Context, functions []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	var lastT hm.Type
	for _, form := range functions {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 1); err != nil {
				errs.Add(fmt.Errorf("function body hoisting failed for %v: %w", form, err))
				continue
			}
		}
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			errs.Add(err)
			// Function already has signature from earlier phase, so no fallback needed
			continue
		}
		lastT = t
	}
	return lastT, nil
}

func inferNonDeclarationsPhaseResilient(ctx context.Context, nonDeclarations []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	var lastT hm.Type
	for _, form := range nonDeclarations {
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			errs.Add(err)
			// Non-declarations don't need fallback types
			continue
		}
		lastT = t
	}
	return lastT, nil
}
