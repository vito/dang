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

// OnDemandInferenceContext manages recursive symbol inference with cycle detection.
// This experimental approach eliminates the need for topological sorting by 
// resolving dependencies on-demand during type inference.
type OnDemandInferenceContext struct {
	symbolToNode   map[string]Node     // Maps symbol names to their declaring nodes
	inferenceStack map[string]bool     // Tracks currently inferring symbols to detect cycles
	availableForms []Node              // All forms available for inference
}

// OnDemandEvaluationContext manages recursive declaration evaluation with cycle detection.
// This complements on-demand inference by lazily evaluating declarations only when needed.
type OnDemandEvaluationContext struct {
	symbolToNode    map[string]Node     // Maps symbol names to their declaring nodes (shared with inference)
	evaluationStack map[string]bool     // Tracks currently evaluating symbols to detect cycles
	evaluated       map[string]Value    // Memoization cache: symbolName -> Value
}

// NewOnDemandInferenceContext creates a new context for on-demand inference
func NewOnDemandInferenceContext(forms []Node) *OnDemandInferenceContext {
	ctx := &OnDemandInferenceContext{
		symbolToNode:   make(map[string]Node),
		inferenceStack: make(map[string]bool),
		availableForms: forms,
	}

	// Build symbol-to-node mapping using DeclaredSymbols()
	for _, form := range forms {
		if declarer, ok := form.(Declarer); ok && declarer.IsDeclarer() {
			for _, symbol := range form.DeclaredSymbols() {
				ctx.symbolToNode[symbol] = form
			}
		}
	}

	return ctx
}

// NewOnDemandEvaluationContext creates a new context for on-demand evaluation
func NewOnDemandEvaluationContext(forms []Node) *OnDemandEvaluationContext {
	ctx := &OnDemandEvaluationContext{
		symbolToNode:    make(map[string]Node),
		evaluationStack: make(map[string]bool),
		evaluated:       make(map[string]Value),
	}

	// Build symbol-to-node mapping using DeclaredSymbols() (same as inference)
	for _, form := range forms {
		if declarer, ok := form.(Declarer); ok && declarer.IsDeclarer() {
			for _, symbol := range form.DeclaredSymbols() {
				ctx.symbolToNode[symbol] = form
			}
		}
	}

	return ctx
}

// InferSymbolOnDemand attempts to infer a symbol by finding and inferring its declarer
func (ctx *OnDemandInferenceContext) InferSymbolOnDemand(symbolName string, wrappedEnv *OnDemandEnv, fresh hm.Fresher) error {
	// Check for cycles
	if ctx.inferenceStack[symbolName] {
		return errors.New("circular dependency detected in symbol inference: " + symbolName)
	}

	// Find the node that declares this symbol
	declarer, found := ctx.symbolToNode[symbolName]
	if !found {
		return errors.New("symbol not found: " + symbolName)
	}

	// Mark this symbol as being inferred to detect cycles
	ctx.inferenceStack[symbolName] = true
	defer func() {
		delete(ctx.inferenceStack, symbolName)
	}()

	// Recursively infer the declarer using the wrapped environment
	_, err := declarer.Infer(wrappedEnv, fresh)
	return err
}

// EvaluateSymbolOnDemand attempts to evaluate a symbol by finding and evaluating its declarer
func (ctx *OnDemandEvaluationContext) EvaluateSymbolOnDemand(symbolName string, wrappedEnv *OnDemandEvalEnv, evalContext context.Context) (Value, error) {
	// Check cache first
	if val, found := ctx.evaluated[symbolName]; found {
		return val, nil
	}

	// Check for cycles
	if ctx.evaluationStack[symbolName] {
		return nil, fmt.Errorf("circular dependency detected in evaluation: %s", symbolName)
	}

	// Find the node that declares this symbol
	declarer, found := ctx.symbolToNode[symbolName]
	if !found {
		return nil, fmt.Errorf("symbol not found: %s", symbolName)
	}

	// Mark this symbol as being evaluated to detect cycles
	ctx.evaluationStack[symbolName] = true
	defer func() {
		delete(ctx.evaluationStack, symbolName)
	}()

	// Recursively evaluate the declarer using the wrapped environment
	val, err := EvalNode(evalContext, wrappedEnv, declarer)
	if err != nil {
		return nil, err
	}

	// Cache the result
	ctx.evaluated[symbolName] = val
	return val, nil
}

// InferFormsWithOnDemandResolution infers forms using recursive on-demand symbol resolution.
// This is an experimental alternative to topological sorting that resolves dependencies
// lazily as they are encountered during type inference.
func InferFormsWithOnDemandResolution(forms []Node, env hm.Env, fresh hm.Fresher) error {
	ctx := NewOnDemandInferenceContext(forms)
	
	// Create a wrapper environment that can trigger on-demand inference
	wrappedEnv := &OnDemandEnv{
		base: env,
		ctx:  ctx,
		fresh: fresh,
	}

	// Try to infer each form, letting the OnDemandEnv handle symbol resolution
	for _, form := range forms {
		_, err := form.Infer(wrappedEnv, fresh)
		if err != nil {
			return err
		}
	}

	return nil
}

// EvaluateFormsWithOnDemandResolution evaluates forms using recursive on-demand declaration evaluation.
// This complements on-demand inference by lazily evaluating declarations only when they are referenced.
func EvaluateFormsWithOnDemandResolution(ctx context.Context, forms []Node, env EvalEnv) (Value, error) {
	evalCtx := NewOnDemandEvaluationContext(forms)
	
	// Create a wrapper environment that can trigger on-demand evaluation
	wrappedEnv := &OnDemandEvalEnv{
		base:        env,
		ctx:         evalCtx,
		evalContext: ctx,
	}

	// Evaluate forms in order, but dependencies will be resolved on-demand
	var result Value = NullValue{}
	for _, form := range forms {
		val, err := EvalNode(ctx, wrappedEnv, form)
		if err != nil {
			return nil, err
		}
		result = val
	}

	return result, nil
}

// OnDemandEnv wraps a base environment to provide on-demand symbol inference.
// When a symbol is not found, it attempts to find and infer the declaring node recursively.
type OnDemandEnv struct {
	base  hm.Env
	ctx   *OnDemandInferenceContext
	fresh hm.Fresher
}

func (e *OnDemandEnv) SchemeOf(name string) (*hm.Scheme, bool) {
	// First try the base environment
	scheme, found := e.base.SchemeOf(name)
	if found {
		return scheme, true
	}

	// If not found, try on-demand inference
	err := e.ctx.InferSymbolOnDemand(name, e, e.fresh)
	if err != nil {
		return nil, false // Inference failed, symbol truly not available
	}

	// Try again after on-demand inference
	return e.base.SchemeOf(name)
}

// Delegate all other methods to the base environment
func (e *OnDemandEnv) Clone() hm.Env {
	return &OnDemandEnv{
		base:  e.base.Clone(),
		ctx:   e.ctx,
		fresh: e.fresh,
	}
}

func (e *OnDemandEnv) Add(name string, scheme *hm.Scheme) hm.Env {
	e.base.Add(name, scheme)
	return e
}

func (e *OnDemandEnv) Remove(name string) hm.Env {
	e.base.Remove(name)
	return e
}

func (e *OnDemandEnv) Apply(subs hm.Subs) hm.Substitutable {
	return &OnDemandEnv{
		base:  e.base.Apply(subs).(hm.Env),
		ctx:   e.ctx,
		fresh: e.fresh,
	}
}

func (e *OnDemandEnv) FreeTypeVar() hm.TypeVarSet {
	return e.base.FreeTypeVar()
}

// Implement Env interface methods
func (e *OnDemandEnv) NamedType(name string) (Env, bool) {
	if baseEnv, ok := e.base.(Env); ok {
		// First try the base environment
		namedType, found := baseEnv.NamedType(name)
		if found {
			return namedType, true
		}

		// If not found, try on-demand inference
		// Note: NamedTypes are typically declared by ClassDecl nodes
		err := e.ctx.InferSymbolOnDemand(name, e, e.fresh)
		if err != nil {
			return nil, false
		}

		// Try again after on-demand inference
		return baseEnv.NamedType(name)
	}
	return nil, false
}

func (e *OnDemandEnv) AddClass(name string, class Env) {
	if baseEnv, ok := e.base.(Env); ok {
		baseEnv.AddClass(name, class)
	}
}

func (e *OnDemandEnv) LocalSchemeOf(name string) (*hm.Scheme, bool) {
	if baseEnv, ok := e.base.(Env); ok {
		return baseEnv.LocalSchemeOf(name)
	}
	return nil, false
}

// Implement hm.Type interface methods (needed for Env interface)
func (e *OnDemandEnv) Eq(other hm.Type) bool {
	if baseType, ok := e.base.(hm.Type); ok {
		return baseType.Eq(other)
	}
	return false
}

func (e *OnDemandEnv) Name() string {
	if baseType, ok := e.base.(hm.Type); ok {
		return baseType.Name()
	}
	return ""
}

func (e *OnDemandEnv) Normalize(k, v hm.TypeVarSet) (hm.Type, error) {
	if baseType, ok := e.base.(hm.Type); ok {
		return baseType.Normalize(k, v)
	}
	return e, nil
}

func (e *OnDemandEnv) Types() hm.Types {
	if baseType, ok := e.base.(hm.Type); ok {
		return baseType.Types()
	}
	return nil
}

func (e *OnDemandEnv) String() string {
	if baseType, ok := e.base.(hm.Type); ok {
		return baseType.String()
	}
	return ""
}

func (e *OnDemandEnv) Format(s fmt.State, c rune) {
	if baseType, ok := e.base.(hm.Type); ok {
		if formattable, ok := baseType.(fmt.Formatter); ok {
			formattable.Format(s, c)
			return
		}
	}
	fmt.Fprintf(s, "%s", e.String())
}

// OnDemandEvalEnv wraps a base evaluation environment to provide on-demand declaration evaluation.
// When a symbol is not found, it attempts to find and evaluate the declaring node recursively.
type OnDemandEvalEnv struct {
	base        EvalEnv
	ctx         *OnDemandEvaluationContext
	evalContext context.Context
}

func (e *OnDemandEvalEnv) Get(name string) (Value, bool) {
	// First try the base environment
	val, found := e.base.Get(name)
	if found {
		return val, true
	}

	// If not found, try on-demand evaluation
	val, err := e.ctx.EvaluateSymbolOnDemand(name, e, e.evalContext)
	if err != nil {
		return nil, false // Evaluation failed, symbol truly not available
	}

	// Cache in base environment for future lookups
	e.base.Set(name, val)
	return val, true
}

func (e *OnDemandEvalEnv) Set(name string, value Value) EvalEnv {
	return e.base.Set(name, value)
}

func (e *OnDemandEvalEnv) SetWithVisibility(name string, value Value, visibility Visibility) {
	e.base.SetWithVisibility(name, value, visibility)
}

func (e *OnDemandEvalEnv) Clone() EvalEnv {
	return &OnDemandEvalEnv{
		base:        e.base.Clone(),
		ctx:         e.ctx,
		evalContext: e.evalContext,
	}
}

var _ hm.Inferer = Block{}

func (b Block) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	newEnv := env.Clone()

	forms := b.Forms
	if len(forms) == 0 {
		forms = append(forms, Null{})
	}

	var t hm.Type
	for _, form := range forms {
		et, err := form.Infer(newEnv, fresh)
		if err != nil {
			return nil, err
		}
		t = et
	}

	return t, nil
}

func (b Block) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	forms := b.Forms
	if len(forms) == 0 {
		return NullValue{}, nil
	}

	newEnv := env.Clone()

	var result Value
	for _, form := range forms {
		val, err := EvalNode(ctx, newEnv, form)
		if err != nil {
			return nil, err
		}
		result = val
	}

	return result, nil
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
