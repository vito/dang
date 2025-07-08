package sprout

import (
	"context"
	"fmt"
	"strings"

	"github.com/vito/sprout/pkg/hm"
)

type FunctionBase struct {
	Args []SlotDecl
	Body Node
	Loc  *SourceLocation

	Inferred *hm.FunctionType
}

// inferFunctionArguments processes SlotDecl arguments into function type arguments
func (f *FunctionBase) inferFunctionArguments(env hm.Env, fresh hm.Fresher, allowFreshTypes bool) ([]Keyed[*hm.Scheme], error) {
	args := []Keyed[*hm.Scheme]{}
	for _, arg := range f.Args {
		var definedArgType hm.Type
		var err error

		if arg.Type_ != nil {
			definedArgType, err = arg.Type_.Infer(env, fresh)
			if err != nil {
				return nil, WrapInferError(err, arg)
			}
		}

		var inferredValType hm.Type
		if arg.Value != nil {
			inferredValType, err = arg.Value.Infer(env, fresh)
			if err != nil {
				return nil, WrapInferError(err, arg.Value)
			}
		}

		for _, directive := range arg.Directives {
			_, err = directive.Infer(env, fresh)
			if err != nil {
				return nil, WrapInferError(err, arg.Value)
			}
		}

		var finalArgType hm.Type
		if definedArgType != nil && inferredValType != nil {
			if !definedArgType.Eq(inferredValType) {
				return nil, WrapInferError(fmt.Errorf("function arg %q mismatch: defined as %s, inferred as %s", arg.Named, definedArgType, inferredValType), arg)
			}
			finalArgType = definedArgType
		} else if definedArgType != nil {
			finalArgType = definedArgType
		} else if inferredValType != nil {
			finalArgType = inferredValType
		} else if allowFreshTypes {
			// Allow fresh types when no explicit type is given (for lambdas)
			finalArgType = fresh.Fresh()
		} else {
			return nil, fmt.Errorf("function arg %q has no type or value", arg.Named)
		}

		scheme := hm.NewScheme(nil, finalArgType)
		env.Add(arg.Named, scheme)

		// For arguments with defaults, make them nullable in the function signature
		// This allows callers to pass null or omit the argument
		signatureType := finalArgType
		if arg.Value != nil {
			// Argument has a default value - make it nullable in the function signature
			if nonNullType, isNonNull := finalArgType.(hm.NonNullType); isNonNull {
				signatureType = nonNullType.Type
			}
		}

		// Add to function signature with the appropriate type
		signatureScheme := hm.NewScheme(nil, signatureType)
		args = append(args, Keyed[*hm.Scheme]{Key: arg.Named, Value: signatureScheme, Positional: false})
	}
	return args, nil
}

// createFunctionValue creates a FunctionValue from processed arguments
func (f *FunctionBase) createFunctionValue(env EvalEnv, fnType *hm.FunctionType) FunctionValue {
	argNames := make([]string, len(f.Args))
	defaults := make(map[string]Node)

	for i, arg := range f.Args {
		argNames[i] = arg.Named
		if arg.Value != nil {
			defaults[arg.Named] = arg.Value
		}
	}

	return FunctionValue{
		Args:     argNames,
		Body:     f.Body,
		Closure:  env,
		FnType:   fnType,
		Defaults: defaults,
		ArgDecls: f.Args, // Preserve original argument declarations with directives
	}
}

// inferFunctionType provides shared type inference logic for functions
func (f *FunctionBase) inferFunctionType(env hm.Env, fresh hm.Fresher, allowFreshTypes bool, explicitRetType TypeNode, contextName string) (*hm.FunctionType, error) {
	// Clone environment for closure semantics
	newEnv := env.Clone()

	// Process arguments using shared logic
	args, err := f.inferFunctionArguments(newEnv, fresh, allowFreshTypes)
	if err != nil {
		return nil, fmt.Errorf("%s.Infer: %w", contextName, err)
	}

	// Handle explicit return type if provided
	var definedRet hm.Type
	if explicitRetType != nil {
		definedRet, err = explicitRetType.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("%s.Infer return type: %w", contextName, err)
		}
	}

	// Infer return type from function body
	inferredRet, err := f.Body.Infer(newEnv, fresh)
	if err != nil {
		return nil, fmt.Errorf("%s.Infer body: %w", contextName, err)
	}

	// Unify explicit and inferred return types if both exist
	if definedRet != nil {
		if !definedRet.Eq(inferredRet) {
			return nil, NewInferError(fmt.Sprintf("return type mismatch: declared %s, inferred %s", definedRet, inferredRet), f.Body)
		}
	}

	f.Inferred = hm.NewFnType(NewRecordType("", args...), inferredRet)
	return f.Inferred, nil
}

// Eval provides shared evaluation logic for functions
func (f *FunctionBase) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	if f.Inferred == nil {
		return nil, fmt.Errorf("%v.Eval: function type not inferred", f)
	}
	return f.createFunctionValue(env, f.Inferred), nil
}

type FunDecl struct {
	FunctionBase
	Named      string
	Ret        TypeNode
	Visibility Visibility
}

var _ Declarer = &FunDecl{}

func (f *FunDecl) IsDeclarer() bool {
	return true
}

var _ hm.Expression = &FunDecl{}
var _ Evaluator = &FunDecl{}

func (f *FunDecl) DeclaredSymbols() []string {
	return []string{f.Named} // Function declarations declare their name
}

func (f *FunDecl) ReferencedSymbols() []string {
	// Function declarations reference symbols from their body
	return f.FunctionBase.Body.ReferencedSymbols()
}

func (f *FunDecl) Body() hm.Expression { return f.FunctionBase.Body }

func (f *FunDecl) GetSourceLocation() *SourceLocation { return f.Loc }

var _ hm.Inferer = &FunDecl{}
var _ Hoister = &FunDecl{}

func (f *FunDecl) Hoist(env hm.Env, fresh hm.Fresher, pass int) error {
	if pass == 0 {
		// Pass 0: Hoist function signature (declare type without inferring body)
		// Clone environment to avoid mutating original during signature inference
		signatureEnv := env.Clone()

		// Process arguments to get function signature
		args, err := f.FunctionBase.inferFunctionArguments(signatureEnv, fresh, false)
		if err != nil {
			return fmt.Errorf("FuncDecl.Hoist: %s signature: %w", f.Named, err)
		}

		// Handle explicit return type if provided
		var retType hm.Type
		if f.Ret != nil {
			retType, err = f.Ret.Infer(env, fresh)
			if err != nil {
				return fmt.Errorf("FuncDecl.Hoist: %s return type: %w", f.Named, err)
			}
		} else {
			// Use a fresh type variable for the return type if not specified
			retType = fresh.Fresh()
		}

		// Create function type and add to environment
		fnType := hm.NewFnType(NewRecordType("", args...), retType)
		env.Add(f.Named, hm.NewScheme(nil, fnType))
		return nil
	} else if pass == 1 {
		// Pass 1: Infer function body (function signature already available)
		// The actual inference will happen in the normal Infer method
		return nil
	}
	return nil
}

func (f *FunDecl) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return f.FunctionBase.inferFunctionType(env, fresh, false, f.Ret, fmt.Sprintf("FuncDecl(%s)", f.Named))
}

type Reassignment struct {
	Target   Node   // Left-hand side expression (Symbol, Select, etc.)
	Modifier string // "=" or "+=" etc.
	Value    Node   // Right-hand side expression
	Loc      *SourceLocation
}

var _ Node = Reassignment{}
var _ Evaluator = Reassignment{}

func (r Reassignment) DeclaredSymbols() []string {
	return nil // Reassignments don't declare new symbols
}

func (r Reassignment) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, r.Target.ReferencedSymbols()...)
	symbols = append(symbols, r.Value.ReferencedSymbols()...)
	return symbols
}

func (r Reassignment) Body() hm.Expression { return r.Target }

func (r Reassignment) GetSourceLocation() *SourceLocation { return r.Loc }

func (r Reassignment) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Infer the type of the target (left-hand side)
	targetType, err := r.Target.Infer(env, fresh)
	if err != nil {
		return nil, fmt.Errorf("Reassignment.Infer: target: %w", err)
	}

	// Infer the type of the value (right-hand side)
	valueType, err := r.Value.Infer(env, fresh)
	if err != nil {
		return nil, fmt.Errorf("Reassignment.Infer: value: %w", err)
	}

	// For simple assignment, check compatibility
	if r.Modifier == "=" {
		if _, err := hm.Unify(targetType, valueType); err != nil {
			return nil, fmt.Errorf("Reassignment.Infer: cannot assign %s to %s: %w", valueType, targetType, err)
		}
		return targetType, nil
	} else if r.Modifier == "+" {
		// For compound assignment, check that it's compatible with addition
		// Create a temporary Addition node to check type compatibility
		tempAddition := Addition{
			Left:  r.Target,
			Right: r.Value,
			Loc:   r.Loc,
		}
		_, err := tempAddition.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("Reassignment.Infer: compound assignment: %w", err)
		}
		return targetType, nil
	}

	return nil, fmt.Errorf("Reassignment.Infer: unsupported modifier %q", r.Modifier)
}

func (r Reassignment) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Evaluate the value first
	value, err := EvalNode(ctx, env, r.Value)
	if err != nil {
		return nil, fmt.Errorf("Reassignment.Eval: evaluating value: %w", err)
	}

	// Handle different assignment types based on target
	switch target := r.Target.(type) {
	case Symbol:
		// Simple variable assignment: x = value or x += value
		return r.evalVariableAssignment(ctx, env, target.Name, value)

	case Select:
		// Field assignment: obj.field = value or obj.field += value
		return r.evalFieldAssignment(ctx, env, target, value)

	default:
		return nil, fmt.Errorf("Reassignment.Eval: unsupported assignment target type %T", r.Target)
	}
}

func (r Reassignment) evalVariableAssignment(ctx context.Context, env EvalEnv, varName string, value Value) (Value, error) {
	if r.Modifier == "=" {
		// Simple assignment: x = value
		_, found := env.Get(varName)
		if !found {
			return nil, fmt.Errorf("Reassignment.Eval: variable %q not found", varName)
		}
		env.Reassign(varName, value)
		return value, nil
	} else if r.Modifier == "+" {
		// Compound assignment: x += value
		currentValue, found := env.Get(varName)
		if !found {
			return nil, fmt.Errorf("Reassignment.Eval: variable %q not found", varName)
		}

		// Perform addition using existing Addition logic
		newValue, err := r.performAddition(currentValue, value, varName)
		if err != nil {
			return nil, err
		}

		env.Reassign(varName, newValue)
		return newValue, nil
	}

	return nil, fmt.Errorf("Reassignment.Eval: unsupported modifier %q", r.Modifier)
}

func (r Reassignment) evalFieldAssignment(ctx context.Context, env EvalEnv, selectNode Select, value Value) (Value, error) {
	// Traverse the nested Select nodes to find the final receiver and the field to modify
	rootSymbol, path, err := r.getPath(selectNode)
	if err != nil {
		return nil, err
	}

	// Get the root object from the environment
	rootObj, found := env.Get(rootSymbol)
	if !found {
		err := fmt.Errorf("object %q not found", rootSymbol)
		return nil, CreateEvalError(ctx, err, selectNode)
	}

	// Clone the root object to begin the copy-on-write process
	newRoot := rootObj.(EvalEnv).Clone()

	// Traverse the path, cloning objects as we go
	currentObj := newRoot
	for i := range len(path) - 1 {
		fieldName := path[i]
		val, found := currentObj.Get(fieldName)
		if !found {
			err := fmt.Errorf("field %q not found in object", fieldName)
			return nil, CreateEvalError(ctx, err, selectNode)
		}
		clonedVal := val.(EvalEnv).Clone()
		currentObj.Set(fieldName, clonedVal.(Value))
		currentObj = clonedVal
	}

	// Get the final field to modify
	finalField := path[len(path)-1]

	// Now that we have the final receiver, perform the assignment
	if r.Modifier == "=" {
		// Simple assignment: obj.field = value
		currentObj.Set(finalField, value)
	} else if r.Modifier == "+" {
		// Compound assignment: obj.field += value
		currentValue, found := currentObj.Get(finalField)
		if !found {
			err := fmt.Errorf("field %q not found", finalField)
			return nil, CreateEvalError(ctx, err, selectNode)
		}

		// Perform addition using existing Addition logic
		newValue, err := r.performAddition(currentValue, value, finalField)
		if err != nil {
			return nil, err
		}

		currentObj.Set(finalField, newValue)
	} else {
		return nil, fmt.Errorf("Reassignment.Eval: unsupported modifier %q", r.Modifier)
	}

	// Update the root object in the environment (respects Fork boundaries)
	env.Reassign(rootSymbol, newRoot.(Value))

	return newRoot.(Value), nil
}

func (r Reassignment) getPath(selectNode Select) (string, []string, error) {
	var path []string
	var currentNode Node = selectNode

	// Traverse down the chain of Select nodes, collecting field names
	for {
		if s, ok := currentNode.(Select); ok {
			path = append([]string{s.Field}, path...)
			currentNode = s.Receiver
		} else {
			break
		}
	}

	// The final node in the chain should be a Symbol (the root object)
	rootSymbol, ok := currentNode.(Symbol)
	if !ok {
		return "", nil, fmt.Errorf("complex receivers must start with a symbol")
	}

	return rootSymbol.Name, path, nil
}

func (r Reassignment) performAddition(left, right Value, varName string) (Value, error) {
	switch l := left.(type) {
	case IntValue:
		if r, ok := right.(IntValue); ok {
			return IntValue{Val: l.Val + r.Val}, nil
		}
		return nil, fmt.Errorf("Reassignment.Eval: cannot add %T to int variable %q", right, varName)

	case StringValue:
		if r, ok := right.(StringValue); ok {
			return StringValue{Val: l.Val + r.Val}, nil
		}
		return nil, fmt.Errorf("Reassignment.Eval: cannot add %T to string variable %q", right, varName)

	case ListValue:
		if r, ok := right.(ListValue); ok {
			// Concatenate the lists
			combined := make([]Value, len(l.Elements)+len(r.Elements))
			copy(combined, l.Elements)
			copy(combined[len(l.Elements):], r.Elements)

			// Use the element type from the left operand, or right if left is empty
			elemType := l.ElemType
			if len(l.Elements) == 0 && len(r.Elements) > 0 {
				elemType = r.ElemType
			}

			return ListValue{Elements: combined, ElemType: elemType}, nil
		}
		return nil, fmt.Errorf("Reassignment.Eval: cannot add %T to list variable %q", right, varName)

	default:
		return nil, fmt.Errorf("Reassignment.Eval: addition not supported for type %T", left)
	}
}

func (r Reassignment) createValueNode(value Value) Node {
	switch v := value.(type) {
	case IntValue:
		return Int{Value: int64(v.Val), Loc: r.Loc}
	case StringValue:
		return String{Value: v.Val, Loc: r.Loc}
	case BoolValue:
		return Boolean{Value: v.Val, Loc: r.Loc}
	case NullValue:
		return Null{Loc: r.Loc}
	default:
		// For complex values, we'll need a more sophisticated approach
		// For now, just create a Symbol that references the value
		return Symbol{Name: fmt.Sprintf("__temp_value_%p", value), Loc: r.Loc}
	}
}

type Reopen struct {
	Name  string
	Block Block
	Loc   *SourceLocation
}

var _ Node = Reopen{}
var _ Evaluator = Reopen{}

func (r Reopen) DeclaredSymbols() []string {
	return nil // Reopen expressions don't declare new symbols
}

func (r Reopen) ReferencedSymbols() []string {
	var symbols []string
	// Reopen references the module being reopened
	symbols = append(symbols, r.Name)
	// And any symbols from the block
	symbols = append(symbols, r.Block.ReferencedSymbols()...)
	return symbols
}

func (r Reopen) Body() hm.Expression { return r.Block }

func (r Reopen) GetSourceLocation() *SourceLocation { return r.Loc }

func (r Reopen) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	sym := Symbol{
		Name: r.Name,
		// low conviction, but allow shadowing?
		// though this might be type-incompatible...? () -> Module vs. Module
		// AutoCall: true,
	}

	// Infer the type of the base term
	termType, err := sym.Infer(env, fresh)
	if err != nil {
		return nil, fmt.Errorf("Reopen.Infer: base term: %w", err)
	}

	// The term must be a module that can be reopened
	nonNullType, ok := termType.(hm.NonNullType)
	if !ok {
		return nil, fmt.Errorf("Reopen.Infer: cannot reopen nullable type %s", termType)
	}

	module, ok := nonNullType.Type.(Env)
	if !ok {
		return nil, fmt.Errorf("Reopen.Infer: cannot reopen non-module type %s", termType)
	}

	// Create a composite environment that allows access to both the reopened module
	// and the current lexical environment for type checking
	compositeTypeEnv := &CompositeModule{
		primary: module.Clone().(Env),
		lexical: env.(Env),
	}

	// Type check the block in the composite context
	_, err = r.Block.Infer(compositeTypeEnv, fresh)
	if err != nil {
		return nil, fmt.Errorf("Reopen.Infer: block: %w", err)
	}

	// Return the same type as the base term (copy-on-write semantics)
	return termType, nil
}

func (r Reopen) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	reopened, found := env.Get(r.Name)
	if !found {
		return nil, fmt.Errorf("cannot reopen %q: not found", r.Name)
	}
	reopenedEnv, ok := reopened.(EvalEnv)
	if !ok {
		return nil, fmt.Errorf("cannot reopen %T: not an environment", reopened)
	}

	compositeEnv := CreateCompositeEnv(reopenedEnv, env)
	for _, node := range r.Block.Forms {
		_, err := EvalNode(ctx, compositeEnv, node)
		if err != nil {
			return nil, fmt.Errorf("Reopen.Eval: %w", err)
		}
	}
	val := compositeEnv.primary.(*ModuleValue)
	return val, nil
}

type Assert struct {
	Message Node  // Optional message expression
	Block   Block // Block containing the assertion expression
	Loc     *SourceLocation
}

var _ Node = Assert{}
var _ Evaluator = Assert{}

func (a Assert) DeclaredSymbols() []string {
	return nil // Assert expressions don't declare anything
}

func (a Assert) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, a.Block.ReferencedSymbols()...)
	if a.Message != nil {
		symbols = append(symbols, a.Message.ReferencedSymbols()...)
	}
	return symbols
}

func (a Assert) Body() hm.Expression { return a.Block }

func (a Assert) GetSourceLocation() *SourceLocation { return a.Loc }

func (a Assert) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Infer the block type - the assertion will be evaluated
	_, err := a.Block.Infer(env, fresh)
	if err != nil {
		return nil, WrapInferError(err, a)
	}

	// Infer the message type if present
	if a.Message != nil {
		_, err := a.Message.Infer(env, fresh)
		if err != nil {
			return nil, WrapInferError(err, a)
		}
	}

	// Assert returns nothing (unit type / null)
	return hm.TypeVariable('a'), nil
}

func (a Assert) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Evaluate the block (gets the last expression's value)
	blockVal, err := EvalNode(ctx, env, a.Block)
	if err != nil {
		return nil, CreateEvalError(ctx, err, a)
	}

	// Check if assertion passed
	if isTruthy(blockVal) {
		return NullValue{}, nil
	}

	// Assertion failed - analyze the last expression
	if len(a.Block.Forms) == 0 {
		return nil, &AssertionError{Message: "Empty assertion block", Location: a.Loc}
	}

	lastExpr := a.Block.Forms[len(a.Block.Forms)-1]
	return nil, CreateEvalError(ctx, a.createAssertionError(ctx, env, lastExpr), a)
}

// createAssertionError builds a detailed error message with child node values
func (a Assert) createAssertionError(ctx context.Context, env EvalEnv, expr Node) error {
	var message strings.Builder

	// Optional user message
	if a.Message != nil {
		msgVal, err := EvalNode(ctx, env, a.Message)
		if err == nil {
			message.WriteString(fmt.Sprintf("Assertion failed: %s\n", msgVal.String()))
		} else {
			message.WriteString("Assertion failed\n")
		}
	} else {
		message.WriteString("Assertion failed\n")
	}

	// Show the failed expression
	message.WriteString(fmt.Sprintf("  Expression: %s\n", a.nodeToString(expr)))

	// Extract and evaluate immediate children
	children := a.getImmediateChildren(expr)
	if len(children) > 0 {
		message.WriteString("  Values:\n")
		for _, child := range children {
			if val, err := EvalNode(ctx, env, child.Node); err == nil {
				message.WriteString(fmt.Sprintf("    %s: %s\n", child.Name, val.String()))
			}
		}
	}

	return &AssertionError{
		Message:  message.String(),
		Location: expr.GetSourceLocation(),
	}
}

type ChildNode struct {
	Name string
	Node Node
}

// getImmediateChildren extracts immediate child nodes for error reporting
func (a Assert) getImmediateChildren(expr Node) []ChildNode {
	switch n := expr.(type) {
	case Select:
		// Handle both field access and method calls
		var children []ChildNode

		// Add receiver if present
		if n.Receiver != nil {
			children = append(children, ChildNode{"receiver", n.Receiver})
		}

		// Add arguments if this is a method call
		if n.Args != nil {
			for i, arg := range *n.Args {
				if arg.Positional {
					children = append(children, ChildNode{
						Name: fmt.Sprintf("arg%d", i),
						Node: arg.Value,
					})
				} else {
					children = append(children, ChildNode{
						Name: arg.Key,
						Node: arg.Value,
					})
				}
			}
		}
		return children

	case FunCall:
		// Function call arguments
		var children []ChildNode
		for i, arg := range n.Args {
			if arg.Positional {
				children = append(children, ChildNode{
					Name: fmt.Sprintf("arg%d", i),
					Node: arg.Value,
				})
			} else {
				children = append(children, ChildNode{
					Name: arg.Key,
					Node: arg.Value,
				})
			}
		}
		return children

	case List:
		// List elements
		var children []ChildNode
		for i, elem := range n.Elements {
			children = append(children, ChildNode{
				Name: fmt.Sprintf("[%d]", i),
				Node: elem,
			})
		}
		return children

	case Default:
		// Default operator children
		return []ChildNode{
			{"left", n.Left},
			{"right", n.Right},
		}

	case Equality:
		// Equality operator children
		return []ChildNode{
			{"left", n.Left},
			{"right", n.Right},
		}

	case Conditional:
		// Conditional expression children
		return []ChildNode{
			{"condition", n.Condition},
		}

	case Let:
		// Let expression children
		return []ChildNode{
			{"value", n.Value},
		}
	}

	return nil
}

// nodeToString converts a node to its string representation
func (a Assert) nodeToString(node Node) string {
	switch n := node.(type) {
	case Symbol:
		return n.Name
	case Select:
		if n.Receiver == nil {
			if n.Args != nil {
				return fmt.Sprintf("%s(...)", n.Field)
			}
			return n.Field
		}
		receiver := a.nodeToString(n.Receiver)
		if n.Args != nil {
			return fmt.Sprintf("%s.%s(...)", receiver, n.Field)
		}
		return fmt.Sprintf("%s.%s", receiver, n.Field)
	case FunCall:
		fun := a.nodeToString(n.Fun)
		return fmt.Sprintf("%s(...)", fun)
	case String:
		return fmt.Sprintf("\"%s\"", n.Value)
	case Int:
		return fmt.Sprintf("%d", n.Value)
	case Boolean:
		return fmt.Sprintf("%t", n.Value)
	case Null:
		return "null"
	case List:
		return "[...]"
	case Default:
		left := a.nodeToString(n.Left)
		right := a.nodeToString(n.Right)
		return fmt.Sprintf("%s ? %s", left, right)
	case Equality:
		left := a.nodeToString(n.Left)
		right := a.nodeToString(n.Right)
		return fmt.Sprintf("%s == %s", left, right)
	case Conditional:
		condition := a.nodeToString(n.Condition)
		return fmt.Sprintf("if %s { ... }", condition)
	case Let:
		return fmt.Sprintf("let %s = %s in ...", n.Name, a.nodeToString(n.Value))
	default:
		return fmt.Sprintf("%T", node)
	}
}

// DirectiveLocation represents a valid location where a directive can be applied
type DirectiveLocation struct {
	Name string
}

// DirectiveDecl represents a directive declaration
type DirectiveDecl struct {
	Name      string
	Args      []SlotDecl
	Locations []DirectiveLocation
	Loc       *SourceLocation
}

var _ Node = &DirectiveDecl{}
var _ Declarer = &DirectiveDecl{}
var _ Hoister = &DirectiveDecl{}

func (d *DirectiveDecl) IsDeclarer() bool {
	return true
}

func (d *DirectiveDecl) DeclaredSymbols() []string {
	return []string{d.Name} // Directive declarations declare their name
}

func (d *DirectiveDecl) ReferencedSymbols() []string {
	var symbols []string
	// Add symbols from argument types and default values
	for _, arg := range d.Args {
		if arg.Type_ != nil {
			symbols = append(symbols, arg.Type_.ReferencedSymbols()...)
		}
		if arg.Value != nil {
			symbols = append(symbols, arg.Value.ReferencedSymbols()...)
		}
	}
	return symbols
}

func (d *DirectiveDecl) Body() hm.Expression { return nil }

func (d *DirectiveDecl) GetSourceLocation() *SourceLocation { return d.Loc }

func (d *DirectiveDecl) Hoist(env hm.Env, fresh hm.Fresher, pass int) error {
	if pass == 0 {
		// Add directive to environment during hoisting so it's available for later use
		if bindEnv, ok := env.(Env); ok {
			bindEnv.AddDirective(d.Name, d)
		}
	}
	return nil
}

func (d *DirectiveDecl) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Validate argument types
	for _, arg := range d.Args {
		if arg.Type_ != nil {
			_, err := arg.Type_.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("DirectiveDecl.Infer: arg %q type: %w", arg.Named, err)
			}
		}
		if arg.Value != nil {
			_, err := arg.Value.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("DirectiveDecl.Infer: arg %q default value: %w", arg.Named, err)
			}
		}
	}

	// Directive declarations don't have a meaningful runtime type
	return hm.TypeVariable('d'), nil
}

func (d *DirectiveDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Directives are compile-time constructs, they don't evaluate to runtime values
	return NullValue{}, nil
}

// DirectiveApplication represents the application of a directive
type DirectiveApplication struct {
	Name string
	Args []Keyed[Node]
	Loc  *SourceLocation
}

var _ Node = DirectiveApplication{}

func (d DirectiveApplication) DeclaredSymbols() []string {
	return nil // Directive applications don't declare anything
}

func (d DirectiveApplication) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, d.Name) // Reference the directive name
	for _, arg := range d.Args {
		symbols = append(symbols, arg.Value.ReferencedSymbols()...)
	}
	return symbols
}

func (d DirectiveApplication) Body() hm.Expression { return nil }

func (d DirectiveApplication) GetSourceLocation() *SourceLocation { return d.Loc }

func (d DirectiveApplication) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Validate that the directive exists and arguments match
	if bindEnv, ok := env.(Env); ok {
		directiveDecl, found := bindEnv.GetDirective(d.Name)
		if !found {
			return nil, WrapInferError(fmt.Errorf("DirectiveApplication.Infer: directive @%s not declared", d.Name), d)
		}

		// Validate arguments match the directive declaration
		err := d.validateArguments(directiveDecl, env, fresh)
		if err != nil {
			return nil, WrapInferError(fmt.Errorf("DirectiveApplication.Infer: %w", err), d)
		}
	}

	// Directive applications don't have a meaningful type for inference
	return hm.TypeVariable('d'), nil
}

func (d DirectiveApplication) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Directive applications are compile-time annotations, no runtime evaluation
	return NullValue{}, nil
}

// validateArguments checks that directive application arguments match the declaration
func (d DirectiveApplication) validateArguments(decl *DirectiveDecl, env hm.Env, fresh hm.Fresher) error {
	// Create map of provided arguments
	providedArgs := make(map[string]Node)
	for _, arg := range d.Args {
		if arg.Positional {
			return fmt.Errorf("directive arguments must be named, not positional")
		}
		providedArgs[arg.Key] = arg.Value
	}

	// Check each declared argument
	for _, declArg := range decl.Args {
		providedArg, provided := providedArgs[declArg.Named]

		if !provided {
			// Check if argument has a default value
			if declArg.Value == nil {
				// Check if the argument type is nullable (optional)
				if declArg.Type_ != nil {
					argType, err := declArg.Type_.Infer(env, fresh)
					if err != nil {
						return err
					}
					if _, isNonNull := argType.(hm.NonNullType); isNonNull {
						return fmt.Errorf("required argument %q not provided", declArg.Named)
					}
				}
			}
			continue
		}

		// Validate provided argument type matches declared type
		if declArg.Type_ != nil {
			expectedType, err := declArg.Type_.Infer(env, fresh)
			if err != nil {
				return fmt.Errorf("failed to infer expected type for argument %q: %w", declArg.Named, err)
			}

			providedType, err := providedArg.Infer(env, fresh)
			if err != nil {
				return fmt.Errorf("failed to infer type for provided argument %q: %w", declArg.Named, err)
			}

			// Use type unification instead of equality to allow non-null types to be provided where nullable types are expected
			if _, err := hm.Unify(expectedType, providedType); err != nil {
				return fmt.Errorf("argument %q type mismatch: expected %s, got %s", declArg.Named, expectedType, providedType)
			}
		}
	}

	// Check for unexpected arguments
	for argName := range providedArgs {
		found := false
		for _, declArg := range decl.Args {
			if declArg.Named == argName {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unexpected argument %q", argName)
		}
	}

	return nil
}
