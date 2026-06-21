package dang

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

type Block struct {
	InferredTypeHolder
	Forms []Node
	Loc   *SourceLocation

	// Filled in during inference phase for non-inline blocks
	TypeScope TypeScope
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

	graph := newSlotDepGraph(variables)
	order, cycle := graph.LinearOrder()
	if cycle != nil {
		names := graph.CycleNames(cycle)
		err := fmt.Errorf("circular module variable initializer: %s", strings.Join(names, " -> "))
		node := variables[cycle[0]]
		if field, ok := node.(*FieldDecl); ok && field.Value != nil {
			return nil, NewInferError(err, field.Value)
		}
		return nil, NewInferError(err, node)
	}

	sorted := make([]Node, len(order))
	for i, idx := range order {
		sorted[i] = variables[idx]
	}
	return sorted, nil
}

// ClassifiedForms holds forms categorized by their compilation phase
type ClassifiedForms struct {
	Imports         []Node // ImportDecl (must be processed before anything else)
	Directives      []Node // DirectiveDecl (must be processed first after imports)
	Constants       []Node // FieldDecl with constant values (literals, no function calls)
	Types           []Node // ObjectDecl
	Variables       []Node // FieldDecl with computed values (function calls, references)
	Functions       []Node // FunDecl and FieldDecl with function bodies
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
		case *ObjectDecl:
			classified.Types = append(classified.Types, f)
		case *EnumDecl:
			classified.Types = append(classified.Types, f)
		case *ScalarDecl:
			classified.Types = append(classified.Types, f)
		case *FieldDecl:
			if isConstantValue(f.Value) {
				classified.Constants = append(classified.Constants, f)
			} else if _, isFunDecl := f.Value.(*FunDecl); isFunDecl {
				// Treat FieldDecl with function bodies as functions for proper hoisting
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
func EvaluateDeclaredFormsWithPhases(ctx context.Context, forms []Node, scope ValueScope) (Value, error) {
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
			val, err := EvalNode(ctx, scope, form)
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
// inference. Computed variables are installed as lazy fields, forced on first
// read, and then forced in source order before non-declarations run. This is
// used for both module top-levels and object bodies.
func EvaluateFormsWithPhases(ctx context.Context, forms []Node, scope ValueScope) (Value, error) {
	var result Value = NullValue{}
	var err error

	// Classify forms by their compilation requirements
	classified := classifyForms(forms)

	// Phase 1: Evaluate imports
	for _, form := range classified.Imports {
		result, err = EvalNode(ctx, scope, form)
		if err != nil {
			return nil, fmt.Errorf("import evaluation failed: %w", err)
		}
	}

	// Phase 2: Evaluate directives (must be available before any usage)
	for _, form := range classified.Directives {
		_, err = EvalNode(ctx, scope, form)
		if err != nil {
			return nil, fmt.Errorf("directive evaluation failed: %w", err)
		}
	}

	// Phase 3: Evaluate constants (can be in any order, no dependencies)
	for _, form := range classified.Constants {
		_, err = EvalNode(ctx, scope, form)
		if err != nil {
			return nil, fmt.Errorf("constant evaluation failed: %w", err)
		}
	}

	// Phase 4: Evaluate types (objects)
	for _, form := range classified.Types {
		_, err = EvalNode(ctx, scope, form)
		if err != nil {
			return nil, fmt.Errorf("type evaluation failed: %w", err)
		}
	}

	// Phase 5: Evaluate functions (establish function values in environment)
	for _, form := range classified.Functions {
		_, err = EvalNode(ctx, scope, form)
		if err != nil {
			return nil, fmt.Errorf("function evaluation failed: %w", err)
		}
	}

	// Phase 6: Install lazy fields for computed variables, then force them in
	// source order. Forward references hidden behind constructors or function
	// calls are resolved by the force-on-read mechanism; the source-order pass
	// guarantees side effects still happen.
	if len(classified.Variables) > 0 {
		if err := installAndForceLazyVariables(ctx, classified.Variables, scope); err != nil {
			return nil, fmt.Errorf("variable evaluation failed: %w", err)
		}
	}

	// Phase 7: Evaluate non-declarations in original order (assignments, expressions, etc.)
	for _, form := range classified.NonDeclarations {
		result, err = EvalNode(ctx, scope, form)
		if err != nil {
			return nil, fmt.Errorf("non-declaration evaluation failed: %w", err)
		}
	}

	return result, nil
}

func installAndForceLazyVariables(ctx context.Context, variables []Node, scope ValueScope) error {
	for _, form := range variables {
		field, ok := form.(*FieldDecl)
		if !ok {
			continue
		}
		scope.BindLazy(field.Name.Name, func(ctx context.Context) (Value, error) {
			return WithEvalErrorHandling(ctx, field, func() (Value, error) {
				return EvalNode(ctx, scope, field.Value)
			})
		}, field.Visibility)
	}

	for _, form := range variables {
		field, ok := form.(*FieldDecl)
		if !ok {
			continue
		}
		if _, _, err := scope.Lookup(ctx, field.Name.Name); err != nil {
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
		if typeScope, ok := newEnv.(TypeScope); ok {
			b.TypeScope = typeScope
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

func (b *Block) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	forms := b.Forms
	if len(forms) == 0 {
		return NullValue{}, nil
	}

	newScope := scope.Derive(false)

	// Blocks evaluate forms in textual order
	var result Value = NullValue{}
	for _, form := range forms {
		val, err := EvalNode(ctx, newScope, form)
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

type ObjectLiteral struct {
	InferredTypeHolder
	Fields []*FieldDecl
	Loc    *SourceLocation

	// Filled in during inference phase
	// This is a little weird but has come up twice, maybe OK pattern?
	// Requires mutating node in-place.
	Mod *Type
}

var _ Node = &ObjectLiteral{}

func (o *ObjectLiteral) DeclaredSymbols() []string {
	return nil // Objects don't declare symbols in the global scope
}

func (o *ObjectLiteral) ReferencedSymbols() []string {
	var symbols []string
	// Objects reference symbols from their fields
	for _, field := range o.Fields {
		symbols = append(symbols, field.ReferencedSymbols()...)
	}
	return symbols
}

func (f *ObjectLiteral) Body() hm.Expression { return f }

func (f *ObjectLiteral) GetSourceLocation() *SourceLocation { return f.Loc }

var _ hm.Inferer = &ObjectLiteral{}

// objectFieldOrder validates the field list and returns field indices in
// dependency order (a field comes after the siblings it references), so
// inference can type-check forward references, and rejects cyclic field
// dependencies. Errors are scoped to the object node so the source location
// points at the literal.
func objectFieldOrder(o *ObjectLiteral) ([]int, error) {
	localNames := make(map[string]int, len(o.Fields))
	nodes := make([]Node, len(o.Fields))
	for i, field := range o.Fields {
		declared := field.DeclaredSymbols()
		if len(declared) != 1 {
			return nil, NewInferError(fmt.Errorf("object field must declare exactly one name"), o)
		}
		name := declared[0]
		if prev, ok := localNames[name]; ok {
			return nil, NewInferError(
				fmt.Errorf("object literal has duplicate field %q (previous declaration at field %d)", name, prev+1),
				o,
			)
		}
		localNames[name] = i
		nodes[i] = field
	}

	graph := newSlotDepGraph(nodes)
	order, cycle := graph.LinearOrder()
	if cycle != nil {
		names := graph.CycleNames(cycle)
		return nil, NewInferError(
			fmt.Errorf("object literal has cyclic field dependencies: %s", strings.Join(names, " -> ")),
			o,
		)
	}
	return order, nil
}

func (o *ObjectLiteral) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod := NewType("", ObjectKind)
	inferTypeScope := &OverlayTypeScope{
		primary: mod,
		lexical: env.(TypeScope),
	}
	order, err := objectFieldOrder(o)
	if err != nil {
		return nil, err
	}
	// Infer in dependency order so a field can reference siblings that are
	// declared later in source order.
	for _, idx := range order {
		if _, err := o.Fields[idx].Infer(ctx, inferTypeScope, fresh); err != nil {
			return nil, err
		}
	}
	o.Mod = mod
	return hm.NonNullType{Type: mod}, nil
}

var _ Evaluator = &ObjectLiteral{}

func (o *ObjectLiteral) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	if o.Mod == nil {
		return nil, errors.New("object has no module inferred")
	}
	newMod := NewObject(o.Mod)
	valueScope := CreateOverlayValueScope(newMod, scope)

	// Bind each field as a lazy initializer in the object, then force them all
	// concurrently. Lazy resolution evaluates fields in dependency order for
	// free: forcing a field that references a sibling forces that sibling first
	// (sharing the single result), while independent fields run in parallel and
	// each runs exactly once. Inference already rejected cyclic fields, so
	// forcing cannot deadlock. force() publishes each field's value, so the
	// resulting object carries every field regardless of completion order.
	for _, field := range o.Fields {
		field := field
		name := field.Name.Name
		newMod.BindLazy(name, func(ctx context.Context) (Value, error) {
			// Fork so a field's incidental local writes stay private; sibling
			// references resolve through newMod, forcing them on demand.
			fieldScope := valueScope.Derive(true)
			// A field's own name refers to the enclosing scope, not the field
			// being defined, so `users: users.{{...}}` reads the outer `users`
			// rather than recursing into itself. Redirect it to the outer scope
			// lazily, so the outer lookup happens only if the field actually
			// references its own name. (A self-reference with no outer binding is
			// already rejected during inference.)
			if scope.Has(name) {
				fieldScope.BindLazy(name, func(ctx context.Context) (Value, error) {
					v, _, err := scope.Lookup(ctx, name)
					return v, err
				}, field.Visibility)
			}
			return WithEvalErrorHandling(ctx, field, func() (Value, error) {
				return field.EvalValue(ctx, fieldScope)
			})
		}, field.Visibility)
	}

	// Force every field concurrently. evalParallel fails fast on the first error
	// (cancelling in-flight siblings) but awaits every field and reports the
	// lowest-source-index error, so the failure surfaced is deterministic
	// regardless of completion order.
	if _, err := evalParallel(ctx, len(o.Fields), func(ctx context.Context, i int) (struct{}, error) {
		_, _, err := newMod.Lookup(ctx, o.Fields[i].Name.Name)
		return struct{}{}, err
	}); err != nil {
		return nil, err
	}
	return newMod, nil
}

func (o *ObjectLiteral) Walk(fn func(Node) bool) {
	if !fn(o) {
		return
	}
	for _, field := range o.Fields {
		field.Walk(fn)
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
		field, ok := form.(*FieldDecl)
		if !ok {
			continue
		}
		t, err := field.DeclareKnownSignature(ctx, env, fresh)
		if err != nil {
			errs.Add(err)
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
