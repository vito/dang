package dang

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

func orderVariablesForInference(variables []Node) ([]Node, error) {
	if len(variables) <= 1 {
		return variables, nil
	}

	declared := make(map[string]int)
	names := make([]string, len(variables))
	dependencies := make(map[int][]int)

	for i, variable := range variables {
		decls := variable.DeclaredSymbols()
		if len(decls) > 0 {
			names[i] = decls[0]
		}
		for _, name := range decls {
			declared[name] = i
		}
	}

	for i, variable := range variables {
		for _, ref := range variable.ReferencedSymbols() {
			dep, ok := declared[ref]
			if ok && dep != i {
				dependencies[i] = append(dependencies[i], dep)
			}
		}
	}

	order, cycle := dfsTopologicalOrder(len(variables), dependencies)
	if cycle != nil {
		cycleNames := make([]string, len(cycle))
		for i, idx := range cycle {
			cycleNames[i] = names[idx]
			if cycleNames[i] == "" {
				cycleNames[i] = fmt.Sprintf("<variable %d>", idx)
			}
		}

		err := fmt.Errorf("circular module variable initializer: %s", strings.Join(cycleNames, " -> "))
		node := variables[cycle[0]]
		if slot, ok := node.(*SlotDecl); ok && slot.Value != nil {
			return nil, NewInferError(err, slot.Value)
		}
		return nil, NewInferError(err, node)
	}

	sorted := make([]Node, len(order))
	for i, idx := range order {
		sorted[i] = variables[idx]
	}
	return sorted, nil
}

// dfsTopologicalOrder returns either a topological order (deps before
// dependents) or, if one exists, a cycle path through the dependency graph.
func dfsTopologicalOrder(n int, dependencies map[int][]int) (order []int, cycle []int) {
	const (
		unvisited = iota
		visiting
		done
	)

	state := make([]int, n)
	var stack []int
	positions := make(map[int]int)

	var visit func(int) []int
	visit = func(i int) []int {
		state[i] = visiting
		positions[i] = len(stack)
		stack = append(stack, i)

		for _, dep := range dependencies[i] {
			switch state[dep] {
			case unvisited:
				if c := visit(dep); c != nil {
					return c
				}
			case visiting:
				c := append([]int(nil), stack[positions[dep]:]...)
				return append(c, dep)
			}
		}

		stack = stack[:len(stack)-1]
		delete(positions, i)
		state[i] = done
		order = append(order, i)
		return nil
	}

	for i := range n {
		if state[i] == unvisited {
			if c := visit(i); c != nil {
				return nil, c
			}
		}
	}

	return order, nil
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
		case *UnionDecl:
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

// DeclareFormsWithPhases performs the declaration/signature half of phased
// compilation. It establishes imports, directives, constants, type names,
// object/interface/union shapes, constructors, fields, and function signatures,
// but deliberately does not infer computed variables, function bodies, or other
// executable expressions.
//
// This is the boundary used by tooling that needs a module's public API before
// the full program can be checked, such as Dagger self-call bootstrapping.
func DeclareFormsWithPhases(ctx context.Context, forms []Node, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	errs := &InferenceErrors{}
	classified := classifyForms(forms)

	phases := []struct {
		name string
		fn   func(*InferenceErrors) (hm.Type, error)
	}{
		{"imports", func(errs *InferenceErrors) (hm.Type, error) {
			return inferImportsPhaseResilient(ctx, classified.Imports, env, fresh, errs)
		}},
		{"type names", func(errs *InferenceErrors) (hm.Type, error) {
			return inferTypeNamesPhaseResilient(ctx, classified.Types, env, fresh, errs)
		}},
		{"directives", func(errs *InferenceErrors) (hm.Type, error) {
			return inferDirectivesPhaseResilient(ctx, classified.Directives, env, fresh, errs)
		}},
		{"constants", func(errs *InferenceErrors) (hm.Type, error) {
			return inferConstantsPhaseResilient(ctx, classified.Constants, env, fresh, errs)
		}},
		{"variable signatures", func(errs *InferenceErrors) (hm.Type, error) {
			return declareVariableSignaturesPhaseResilient(ctx, classified.Variables, env, fresh, errs)
		}},
		{"type signatures", func(errs *InferenceErrors) (hm.Type, error) {
			return declareTypeSignaturesPhaseResilient(ctx, classified.Types, env, fresh, errs)
		}},
		{"function signatures", func(errs *InferenceErrors) (hm.Type, error) {
			return declareFunctionSignaturesPhaseResilient(ctx, classified.Functions, env, fresh, errs)
		}},
	}

	var lastT hm.Type
	for _, phase := range phases {
		t, err := phase.fn(errs)
		if err != nil {
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

// EvaluateDeclaredFormsWithPhases evaluates only the declaration forms made
// safe by DeclareFormsWithPhases. Function and method bodies are captured as
// closures but not executed; variables and non-declarations are skipped.
func EvaluateDeclaredFormsWithPhases(ctx context.Context, forms []Node, env EvalEnv) (Value, error) {
	var result Value = NullValue{}
	classified := classifyForms(forms)

	for _, group := range [][]Node{
		classified.Imports,
		classified.Directives,
		classified.Constants,
		classified.Types,
		classified.Functions,
	} {
		for _, form := range group {
			val, err := EvalNode(ctx, env, form)
			if err != nil {
				return nil, err
			}
			result = val
		}
	}

	return result, nil
}

// InferFormsWithPhases implements phased compilation:
// 1. Parse all files (already done)
// 2. Build dependency graph of all declarations
// 3. Import external schemas
// 4. Hoist local type names so annotations shadow imported types immediately
// 5. Typecheck constants and types (which can reference each other)
// 6. Declare function signatures (without bodies)
// 7. Typecheck variables in dependency order (can now reference function signatures)
// 8. Typecheck function bodies last (can reference all package-level declarations)
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
		{"type names", func(errs *InferenceErrors) (hm.Type, error) {
			return inferTypeNamesPhaseResilient(ctx, classified.Types, env, fresh, errs)
		}},
		{"directives", func(errs *InferenceErrors) (hm.Type, error) {
			return inferDirectivesPhaseResilient(ctx, classified.Directives, env, fresh, errs)
		}},
		{"constants", func(errs *InferenceErrors) (hm.Type, error) {
			return inferConstantsPhaseResilient(ctx, classified.Constants, env, fresh, errs)
		}},
		{"variable signatures", func(errs *InferenceErrors) (hm.Type, error) {
			return declareVariableSignaturesPhaseResilient(ctx, classified.Variables, env, fresh, errs)
		}},
		{"types", func(errs *InferenceErrors) (hm.Type, error) {
			return inferTypesPhaseResilient(ctx, classified.Types, env, fresh, errs)
		}},
		{"function signatures", func(errs *InferenceErrors) (hm.Type, error) {
			return declareFunctionSignaturesPhaseResilient(ctx, classified.Functions, env, fresh, errs)
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

	switch v := value.(type) {
	case *String, *Int, *Boolean, *Null:
		return true
	case *Template:
		return v.IsLiteralOnly()
	default:
		return false
	}
}

// EvaluateFormsWithPhases evaluates forms using the same phased approach as
// inference. Computed variables are installed as lazy slots, forced on first
// read, and then forced in source order before non-declarations run. This is
// used for both module top-levels and class bodies.
func EvaluateFormsWithPhases(ctx context.Context, forms []Node, env EvalEnv) (Value, error) {
	var result Value = NullValue{}
	var err error

	// Classify forms by their compilation requirements
	classified := classifyForms(forms)

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

	// Phase 6: Install lazy slots for computed variables, then force them in
	// source order. Forward references hidden behind constructors or function
	// calls are resolved by the force-on-read mechanism; the source-order pass
	// guarantees side effects still happen.
	if len(classified.Variables) > 0 {
		if err := installAndForceLazyVariables(ctx, classified.Variables, env); err != nil {
			return nil, fmt.Errorf("variable evaluation failed: %w", err)
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

func installAndForceLazyVariables(ctx context.Context, variables []Node, env EvalEnv) error {
	for _, form := range variables {
		slot, ok := form.(*SlotDecl)
		if !ok {
			continue
		}
		env.BindLazy(slot.Name.Name, func(ctx context.Context) (Value, error) {
			return WithEvalErrorHandling(ctx, slot, func() (Value, error) {
				return EvalNode(ctx, env, slot.Value)
			})
		}, slot.Visibility)
	}

	for _, form := range variables {
		slot, ok := form.(*SlotDecl)
		if !ok {
			continue
		}
		if _, _, err := env.Lookup(ctx, slot.Name.Name); err != nil {
			return err
		}
	}

	return nil
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
		// Only the block's trailing form sits in tail position; clear the
		// expected type for the rest so that intermediate let/expression
		// forms don't get coerced against it.
		blockExpected := currentInferExpectedType(ctx)
		childCtx := contextWithoutInferExpectedType(ctx)
		for i, form := range forms {
			formCtx := childCtx
			if i == len(forms)-1 && blockExpected != nil {
				formCtx = contextWithInferExpectedType(childCtx, blockExpected)
			}
			if inferer, ok := form.(hm.Inferer); ok {
				typ, err = inferer.Infer(formCtx, newEnv, fresh)
				if err != nil {
					errs.Add(err)
				}
			}
			// Propagate guard-clause narrowings to subsequent forms: if a
			// conditional's branch diverges (raise/return/break/continue),
			// the opposite branch's refinements are guaranteed to hold.
			if cond, ok := form.(*Conditional); ok {
				applyNarrowings(newEnv, conditionalExitNarrowings(cond, newEnv))
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

	newEnv := env.Derive(false)

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
	mod := NewModule("", ObjectKind)
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
				errs.Add(err)
				continue
			}
		}
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			errs.Add(err)
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
				errs.Add(err)
				continue
			}
		}
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			errs.Add(err)
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
			errs.Add(err)
			assignFallbackType(form, env, fresh)
			continue
		}
		lastT = t
	}
	return lastT, nil
}

func declareVariableSignaturesPhaseResilient(ctx context.Context, variables []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	var lastT hm.Type
	for _, form := range variables {
		slot, ok := form.(*SlotDecl)
		if !ok {
			continue
		}
		t, err := slot.DeclareKnownSignature(ctx, env, fresh)
		if err != nil {
			// Suppress: inferVariablesPhaseResilient will re-attempt the
			// same inference and surface the error there. Install a
			// fallback type so downstream references still resolve.
			assignFallbackType(slot, env, fresh)
			continue
		}
		if t != nil {
			lastT = t
		}
	}
	return lastT, nil
}

func inferTypeNamesPhaseResilient(ctx context.Context, types []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	// Pass 0 only registers local type names. Running it before any phase that
	// resolves annotations ensures a local type shadows an unqualified import as
	// soon as imports are installed.
	for _, form := range types {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 0); err != nil {
				errs.Add(err)
				// Continue to try other types
			}
		}
	}
	return nil, nil
}

func declareTypeSignaturesPhaseResilient(ctx context.Context, types []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	// Pass 1: Declare type signatures (constructors, fields, methods,
	// interface members, union members) without inferring executable bodies.
	for _, form := range types {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(ctx, env, fresh, 1); err != nil {
				errs.Add(err)
				// Continue to try other types.
			}
		}
	}
	return nil, nil
}

func inferTypesPhaseResilient(ctx context.Context, types []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	if _, err := declareTypeSignaturesPhaseResilient(ctx, types, env, fresh, errs); err != nil {
		return nil, err
	}
	return inferTypeBodiesPhaseResilient(ctx, types, env, fresh, errs)
}

func inferTypeBodiesPhaseResilient(ctx context.Context, types []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
	var lastT hm.Type
	for _, form := range types {
		t, err := form.Infer(ctx, env, fresh)
		if err != nil {
			errs.Add(err)
			if t == nil {
				assignFallbackType(form, env, fresh)
			}
			continue
		}
		lastT = t
	}
	return lastT, nil
}

func declareFunctionSignaturesPhaseResilient(ctx context.Context, functions []Node, env hm.Env, fresh hm.Fresher, errs *InferenceErrors) (hm.Type, error) {
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

	orderedVars, err := orderVariablesForInference(variables)
	if err != nil {
		// Can't continue if we can't order dependencies - return critical error
		return nil, err
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
				errs.Add(err)
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
