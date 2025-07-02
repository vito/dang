package dash

import (
	"context"
	"errors"
	"fmt"

	"github.com/chewxy/hm"
)

type Block struct {
	Forms []Node
	Loc   *SourceLocation
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
	for i := 0; i < n; i++ {
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

// InferFormsWithPhases implements phased compilation:
// 1. Parse all files (already done)
// 2. Build dependency graph of all declarations
// 3. Typecheck constants and types (which can reference each other)
// 4. Declare function signatures (without bodies)
// 5. Typecheck variables in dependency order (can now reference function signatures)
// 6. Typecheck function bodies last (can reference all package-level declarations)
func InferFormsWithPhases(forms []Node, env hm.Env, fresh hm.Fresher) error {
	// Phase 1: Separate declarations from non-declarations
	var constants []Node      // SlotDecl with constant values (literals, no function calls)
	var types []Node          // ClassDecl
	var variables []Node      // SlotDecl with computed values (function calls, references)
	var functions []Node      // FunDecl
	var nonDeclarations []Node // Everything else (assignments, expressions, etc.)

	for _, form := range forms {
		switch f := form.(type) {
		case *ClassDecl:
			types = append(types, f)
		case SlotDecl:
			if isConstantValue(f.Value) {
				constants = append(constants, f)
			} else {
				variables = append(variables, f)
			}
		case *FunDecl:
			functions = append(functions, f)
		default:
			// All non-declarations (assignments, expressions, assertions, etc.)
			nonDeclarations = append(nonDeclarations, form)
		}
	}

	// Phase 2: Typecheck constants (can be in any order, no dependencies)
	for _, form := range constants {
		_, err := form.Infer(env, fresh)
		if err != nil {
			return fmt.Errorf("constant inference failed: %w", err)
		}
	}

	// Phase 3: Typecheck types (classes) - use traditional hoisting
	for _, form := range types {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(env, fresh, 0); err != nil { // Pass 0: create classes
				return fmt.Errorf("type hoisting failed: %w", err)
			}
		}
	}
	for _, form := range types {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(env, fresh, 1); err != nil { // Pass 1: infer class bodies
				return fmt.Errorf("type body inference failed: %w", err)
			}
		}
	}

	// Phase 4: Declare function signatures (hoist function declarations without bodies)
	for _, form := range functions {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(env, fresh, 0); err != nil { // Hoist function signatures
				return fmt.Errorf("function signature hoisting failed: %w", err)
			}
		}
	}

	// Phase 5: Typecheck variables in dependency order (can now reference function signatures)
	if len(variables) > 0 {
		orderedVars, err := orderByDependencies(variables)
		if err != nil {
			return fmt.Errorf("variable dependency ordering failed: %w", err)
		}

		for _, form := range orderedVars {
			_, err := form.Infer(env, fresh)
			if err != nil {
				return fmt.Errorf("variable inference failed: %w", err)
			}
		}
	}

	// Phase 6: Typecheck function bodies (can reference everything)
	for _, form := range functions {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(env, fresh, 1); err != nil { // Infer function bodies
				return fmt.Errorf("function body inference failed: %w", err)
			}
		}
	}

	// Phase 7: Typecheck non-declarations in original order (can reference all declarations)
	for _, form := range nonDeclarations {
		_, err := form.Infer(env, fresh)
		if err != nil {
			return fmt.Errorf("non-declaration inference failed: %w", err)
		}
	}

	return nil
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
	// Phase 1: Separate declarations from non-declarations (same as inference)
	var constants []Node      // SlotDecl with constant values
	var types []Node          // ClassDecl
	var variables []Node      // SlotDecl with computed values
	var functions []Node      // FunDecl
	var nonDeclarations []Node // Everything else (evaluated in original order)

	for _, form := range forms {
		switch f := form.(type) {
		case *ClassDecl:
			types = append(types, f)
		case SlotDecl:
			if isConstantValue(f.Value) {
				constants = append(constants, f)
			} else {
				variables = append(variables, f)
			}
		case *FunDecl:
			functions = append(functions, f)
		default:
			// All non-declarations (assignments, expressions, assertions, etc.)
			nonDeclarations = append(nonDeclarations, form)
		}
	}

	// Phase 2: Evaluate constants
	for _, form := range constants {
		_, err := EvalNode(ctx, env, form)
		if err != nil {
			return nil, err
		}
	}

	// Phase 3: Evaluate types (classes)
	for _, form := range types {
		_, err := EvalNode(ctx, env, form)
		if err != nil {
			return nil, err
		}
	}

	// Phase 4: Evaluate functions (establish function values in environment)
	for _, form := range functions {
		_, err := EvalNode(ctx, env, form)
		if err != nil {
			return nil, err
		}
	}

	// Phase 5: Evaluate variables in dependency order
	if len(variables) > 0 {
		orderedVars, err := orderByDependencies(variables)
		if err != nil {
			return nil, fmt.Errorf("variable dependency ordering failed: %w", err)
		}

		for _, form := range orderedVars {
			_, err := EvalNode(ctx, env, form)
			if err != nil {
				return nil, err
			}
		}
	}

	// Phase 6: Evaluate non-declarations in original order (assignments, expressions, etc.)
	var result Value = NullValue{}
	for _, form := range nonDeclarations {
		val, err := EvalNode(ctx, env, form)
		if err != nil {
			return nil, err
		}
		result = val
	}

	return result, nil
}

var _ hm.Inferer = Block{}

func (b Block) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	newEnv := env.Clone()

	forms := b.Forms
	if len(forms) == 0 {
		forms = append(forms, Null{})
	}

	// Use phased inference approach for proper dependency handling
	if err := InferFormsWithPhases(forms, newEnv, fresh); err != nil {
		return nil, err
	}

	// Return the type of the last form
	if len(forms) > 0 {
		return forms[len(forms)-1].Infer(newEnv, fresh)
	}

	// Empty block returns null type
	return Null{}.Infer(newEnv, fresh)
}

func (b Block) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	forms := b.Forms
	if len(forms) == 0 {
		return NullValue{}, nil
	}

	newEnv := env.Clone()

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
	mod := NewModule("<lit>")
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
	return NonNullType{mod}, nil
}

var _ Evaluator = &Object{}

func (o *Object) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	if o.Mod == nil {
		return nil, errors.New("object has no module inferred")
	}
	newMod := NewModuleValue(o.Mod)
	evalEnv := &CompositeEnv{
		primary: newMod,
		lexical: env,
	}
	for _, slot := range o.Slots {
		_, err := EvalNode(ctx, evalEnv, slot)
		if err != nil {
			return nil, err
		}
	}
	return newMod, nil
}
