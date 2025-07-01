package dash

import (
	"context"
	"errors"

	"github.com/chewxy/hm"
)

type Block struct {
	Forms []Node
	Loc   *SourceLocation
}

var _ hm.Expression = Block{}
var _ Evaluator = Block{}

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
	switch n := node.(type) {
	case SlotDecl:
		return []string{n.Named}
	case ClassDecl:
		return []string{n.Named}
	case FunDecl:
		return []string{n.Named}
	default:
		return nil
	}
}

// getSymbolReferences extracts all symbol references in a node's value/body
func getSymbolReferences(node Node) []string {
	switch n := node.(type) {
	case SlotDecl:
		if n.Value != nil {
			return extractSymbols(n.Value)
		}
		return nil
	case ClassDecl:
		return extractSymbols(n.Value)
	case FunDecl:
		return extractSymbols(n.FunctionBase.Body)
	default:
		return nil
	}
}

// extractSymbols recursively extracts all Symbol references from a node
func extractSymbols(node Node) []string {
	var symbols []string

	switch n := node.(type) {
	case Symbol:
		symbols = append(symbols, n.Name)
	case Select:
		// When Receiver is nil, this is a top-level function call like createPerson()
		if n.Receiver == nil {
			symbols = append(symbols, n.Field)
		} else {
			symbols = append(symbols, extractSymbols(n.Receiver)...)
		}
		if n.Args != nil {
			for _, arg := range *n.Args {
				symbols = append(symbols, extractSymbols(arg.Value)...)
			}
		}
	case FunCall:
		symbols = append(symbols, extractSymbols(n.Fun)...)
		for _, arg := range n.Args {
			symbols = append(symbols, extractSymbols(arg.Value)...)
		}
	case Block:
		for _, form := range n.Forms {
			symbols = append(symbols, extractSymbols(form)...)
		}
	case List:
		for _, elem := range n.Elements {
			symbols = append(symbols, extractSymbols(elem)...)
		}
	case Default:
		symbols = append(symbols, extractSymbols(n.Left)...)
		symbols = append(symbols, extractSymbols(n.Right)...)
	case Equality:
		symbols = append(symbols, extractSymbols(n.Left)...)
		symbols = append(symbols, extractSymbols(n.Right)...)
	case Addition:
		symbols = append(symbols, extractSymbols(n.Left)...)
		symbols = append(symbols, extractSymbols(n.Right)...)
	case Subtraction:
		symbols = append(symbols, extractSymbols(n.Left)...)
		symbols = append(symbols, extractSymbols(n.Right)...)
	case Multiplication:
		symbols = append(symbols, extractSymbols(n.Left)...)
		symbols = append(symbols, extractSymbols(n.Right)...)
	case Division:
		symbols = append(symbols, extractSymbols(n.Left)...)
		symbols = append(symbols, extractSymbols(n.Right)...)
	case Modulo:
		symbols = append(symbols, extractSymbols(n.Left)...)
		symbols = append(symbols, extractSymbols(n.Right)...)
	case Inequality:
		symbols = append(symbols, extractSymbols(n.Left)...)
		symbols = append(symbols, extractSymbols(n.Right)...)
	case LessThan:
		symbols = append(symbols, extractSymbols(n.Left)...)
		symbols = append(symbols, extractSymbols(n.Right)...)
	case GreaterThan:
		symbols = append(symbols, extractSymbols(n.Left)...)
		symbols = append(symbols, extractSymbols(n.Right)...)
	case LessThanEqual:
		symbols = append(symbols, extractSymbols(n.Left)...)
		symbols = append(symbols, extractSymbols(n.Right)...)
	case GreaterThanEqual:
		symbols = append(symbols, extractSymbols(n.Left)...)
		symbols = append(symbols, extractSymbols(n.Right)...)
	case Conditional:
		symbols = append(symbols, extractSymbols(n.Condition)...)
		symbols = append(symbols, extractSymbols(n.Then)...)
		if n.Else != nil {
			symbols = append(symbols, extractSymbols(n.Else.(Block))...)
		}
	case Let:
		symbols = append(symbols, extractSymbols(n.Value)...)
		symbols = append(symbols, extractSymbols(n.Expr)...)
	case Lambda:
		symbols = append(symbols, extractSymbols(n.FunctionBase.Body)...)
	case Reassignment:
		symbols = append(symbols, extractSymbols(n.Target)...)
		symbols = append(symbols, extractSymbols(n.Value)...)
	case Assert:
		symbols = append(symbols, extractSymbols(n.Block)...)
		if n.Message != nil {
			symbols = append(symbols, extractSymbols(n.Message)...)
		}
	case SlotDecl:
		if n.Value != nil {
			symbols = append(symbols, extractSymbols(n.Value)...)
		}
	case ClassDecl:
		symbols = append(symbols, extractSymbols(n.Value)...)
	case FunDecl:
		symbols = append(symbols, extractSymbols(n.FunctionBase.Body)...)
	// Literals don't contain symbol references
	case String, Int, Boolean, Null:
		// No symbols to extract
	}

	return symbols
}

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
