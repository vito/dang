package sprout

import (
	"context"
	"errors"
	"fmt"

	"github.com/vito/sprout/pkg/hm"
)

type Block struct {
	Forms []Node
	// Evaluate forms in the current scope, not a nested one.
	Inline bool
	Loc    *SourceLocation
}

var _ hm.Expression = Block{}
var _ Evaluator = Block{}

func (b Block) DeclaredSymbols() []string {
	return nil // Blocks don't declare symbols directly (their forms do)
}

func (b Block) ReferencedSymbols() []string {
	var symbols []string

	// Add symbols from all forms in the block
	for _, form := range b.Forms {
		symbols = append(symbols, form.ReferencedSymbols()...)
	}

	return symbols
}

func (f Block) Body() hm.Expression { return f }

func (f Block) GetSourceLocation() *SourceLocation { return f.Loc }

type Hoister interface {
	Hoist(hm.Env, hm.Fresher, int) error
}

var _ Hoister = Block{}

type Declarer interface {
	IsDeclarer() bool
}

func (b Block) Hoist(env hm.Env, fresh hm.Fresher, depth int) error {
	var errs []error
	for _, form := range b.Forms {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(env, fresh, depth); err != nil {
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
	Directives      []Node // DirectiveDecl (must be processed first)
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
		case *DirectiveDecl:
			classified.Directives = append(classified.Directives, f)
		case *ClassDecl:
			classified.Types = append(classified.Types, f)
		case SlotDecl:
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
func InferFormsWithPhases(forms []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var lastT hm.Type
	var err error

	// Phase 1: Classify forms by their compilation requirements
	classified := classifyForms(forms)

	// Phase 2: Hoist and typecheck directives (must be available before any usage)
	for _, form := range classified.Directives {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(env, fresh, 0); err != nil {
				return nil, fmt.Errorf("directive hoisting failed: %w", err)
			}
		}
		lastT, err = form.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("directive inference failed: %w", err)
		}
	}

	// Phase 3: Typecheck constants (can be in any order, no dependencies)
	for _, form := range classified.Constants {
		lastT, err = form.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("constant inference failed: %w", err)
		}
	}

	// Phase 4: Typecheck types (classes) - use multi-pass hoisting
	// Pass 0: Create class types
	for _, form := range classified.Types {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(env, fresh, 0); err != nil {
				return nil, fmt.Errorf("type hoisting (pass 0) failed: %w", err)
			}
		}
	}
	// Pass 1: Infer class bodies
	for _, form := range classified.Types {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(env, fresh, 1); err != nil {
				return nil, fmt.Errorf("type hoisting (pass 1) failed: %w", err)
			}
		}
	}
	// Complete type inference
	for _, form := range classified.Types {
		lastT, err = form.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("type inference failed: %w", err)
		}
	}

	// Phase 5: Declare function signatures (hoist function declarations without bodies)
	for _, form := range classified.Functions {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(env, fresh, 0); err != nil {
				return nil, fmt.Errorf("function signature hoisting failed: %w", err)
			}
		}
	}

	// Phase 6: Typecheck variables in dependency order (can now reference function signatures)
	if len(classified.Variables) > 0 {
		orderedVars, err := orderByDependencies(classified.Variables)
		if err != nil {
			return nil, fmt.Errorf("variable dependency ordering failed: %w", err)
		}

		for _, form := range orderedVars {
			lastT, err = form.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("variable inference failed: %w", err)
			}
		}
	}

	// Phase 7: Typecheck function bodies (can reference everything)
	for _, form := range classified.Functions {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(env, fresh, 1); err != nil {
				return nil, fmt.Errorf("function body hoisting failed: %w", err)
			}
		}
		lastT, err = form.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("function inference failed: %w", err)
		}
	}

	// Phase 8: Typecheck non-declarations in original order (can reference all declarations)
	for _, form := range classified.NonDeclarations {
		lastT, err = form.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("non-declaration inference failed: %w", err)
		}
	}

	return lastT, nil
}

// isConstantValue determines if a value expression is a compile-time constant
func isConstantValue(value Node) bool {
	if value == nil {
		return true // Type-only declarations
	}

	switch value.(type) {
	case String, Int, Boolean, Null:
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

	// Phase 1: Classify forms by their compilation requirements
	classified := classifyForms(forms)

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

var _ hm.Inferer = Block{}

func (b Block) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	newEnv := env
	if !b.Inline {
		newEnv = env.Clone()
	}

	forms := b.Forms
	if len(forms) == 0 {
		forms = append(forms, Null{})
	}

	// Use phased inference approach for proper dependency handling
	return InferFormsWithPhases(forms, newEnv, fresh)
}

func (b Block) Eval(ctx context.Context, env EvalEnv) (Value, error) {
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
	Slots []SlotDecl
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

func (o *Object) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod := NewModule("")
	inferEnv := &CompositeModule{
		primary: mod,
		lexical: env.(Env),
	}
	for _, slot := range o.Slots {
		_, err := slot.Infer(inferEnv, fresh)
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
