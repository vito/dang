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
	// Evaluate forms in the current scope, not a nested one.
	Inline bool
	Loc    *SourceLocation
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
	var errs []error
	for _, form := range b.Forms {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, depth); err != nil {
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
		case *ClassDecl:
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
func InferFormsWithPhases(ctx context.Context, forms []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Phase 1: Classify forms by their compilation requirements
	classified := classifyForms(forms)

	// Execute each compilation phase in order
	phases := []func() (hm.Type, error){
		func() (hm.Type, error) { return inferImportsPhase(ctx, classified.Imports, env, fresh) },
		func() (hm.Type, error) { return inferDirectivesPhase(ctx, classified.Directives, env, fresh) },
		func() (hm.Type, error) { return inferConstantsPhase(ctx, classified.Constants, env, fresh) },
		func() (hm.Type, error) { return inferTypesPhase(ctx, classified.Types, env, fresh) },
		func() (hm.Type, error) { return inferFunctionSignaturesPhase(ctx, classified.Functions, env, fresh) },
		func() (hm.Type, error) { return inferVariablesPhase(ctx, classified.Variables, env, fresh) },
		func() (hm.Type, error) { return inferFunctionBodiesPhase(ctx, classified.Functions, env, fresh) },
		func() (hm.Type, error) { return inferNonDeclarationsPhase(ctx, classified.NonDeclarations, env, fresh) },
	}

	var lastT hm.Type
	for _, phase := range phases {
		t, err := phase()
		if err != nil {
			return nil, err
		}
		if t != nil {
			lastT = t
		}
	}

	return lastT, nil
}

// inferImportsPhase processes import declarations first
func inferImportsPhase(ctx context.Context, imports []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var lastT hm.Type
	for _, form := range imports {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 0); err != nil {
				return nil, fmt.Errorf("import hoisting failed: %w", err)
			}
		}
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("import inference failed: %w", err)
		}
		lastT = t
	}
	return lastT, nil
}

// inferDirectivesPhase processes directive declarations
func inferDirectivesPhase(ctx context.Context, directives []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var lastT hm.Type
	for _, form := range directives {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 0); err != nil {
				return nil, fmt.Errorf("directive hoisting failed: %w", err)
			}
		}
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("directive inference failed: %w", err)
		}
		lastT = t
	}
	return lastT, nil
}

// inferConstantsPhase processes constant declarations
func inferConstantsPhase(ctx context.Context, constants []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var lastT hm.Type
	for _, form := range constants {
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("constant inference failed: %w", err)
		}
		lastT = t
	}
	return lastT, nil
}

// inferTypesPhase processes type declarations using multi-pass hoisting
func inferTypesPhase(ctx context.Context, types []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Pass 0: Create class types
	for _, form := range types {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 0); err != nil {
				return nil, fmt.Errorf("type hoisting (pass 0) failed: %w", err)
			}
		}
	}

	// Pass 1: Infer class bodies
	for _, form := range types {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 1); err != nil {
				return nil, fmt.Errorf("type hoisting (pass 1) failed: %w", err)
			}
		}
	}

	// Complete type inference
	var lastT hm.Type
	for _, form := range types {
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("type inference failed: %w", err)
		}
		lastT = t
	}
	return lastT, nil
}

// inferFunctionSignaturesPhase declares function signatures without bodies
func inferFunctionSignaturesPhase(ctx context.Context, functions []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	for _, form := range functions {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 0); err != nil {
				return nil, fmt.Errorf("function signature hoisting failed: %w", err)
			}
		}
	}
	return nil, nil
}

// inferVariablesPhase processes variable declarations in dependency order
func inferVariablesPhase(ctx context.Context, variables []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	if len(variables) == 0 {
		return nil, nil
	}

	orderedVars, err := orderByDependencies(variables)
	if err != nil {
		return nil, fmt.Errorf("variable dependency ordering failed: %w", err)
	}

	var lastT hm.Type
	for _, form := range orderedVars {
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("variable inference failed: %w", err)
		}
		lastT = t
	}
	return lastT, nil
}

// inferFunctionBodiesPhase processes function bodies
func inferFunctionBodiesPhase(ctx context.Context, functions []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var lastT hm.Type
	for _, form := range functions {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 1); err != nil {
				return nil, fmt.Errorf("function body hoisting failed: %w", err)
			}
		}
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("function inference failed: %w", err)
		}
		lastT = t
	}
	return lastT, nil
}

// inferNonDeclarationsPhase processes non-declaration forms
func inferNonDeclarationsPhase(ctx context.Context, nonDeclarations []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var lastT hm.Type
	for _, form := range nonDeclarations {
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("non-declaration inference failed: %w", err)
		}
		lastT = t
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
			return nil, fmt.Errorf("non-declaration evaluation failed: %w", err)
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
	newEnv := env
	if !b.Inline {
		newEnv = env.Clone()
	}

	forms := b.Forms
	if len(forms) == 0 {
		forms = append(forms, &Null{})
	}

	// Use phased inference approach for proper dependency handling
	return InferFormsWithPhases(ctx, forms, newEnv, fresh)
}

func (b *Block) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	forms := b.Forms
	if len(forms) == 0 {
		return NullValue{}, nil
	}

	newEnv := env
	if !b.Inline {
		newEnv = env.Clone()
	}

	// Use phased evaluation to match the inference order
	return EvaluateFormsWithPhases(ctx, forms, newEnv)
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
