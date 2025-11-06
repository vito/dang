package dang

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/introspection"
	"github.com/vito/dang/pkg/hm"
)

type FunctionBase struct {
	InferredTypeHolder
	Args       []*SlotDecl
	BlockParam *SlotDecl // Optional block parameter (prefixed with &)
	Body       Node
	Loc        *SourceLocation

	Inferred *hm.FunctionType

	InferredScope Env
	
	// ExpectedReturnType is set by checkArgumentType for bidirectional type inference
	ExpectedReturnType hm.Type
}

// inferFunctionArguments processes SlotDecl arguments into function type arguments
func (f *FunctionBase) inferFunctionArguments(ctx context.Context, env hm.Env, fresh hm.Fresher) ([]Keyed[*hm.Scheme], error) {
	args := []Keyed[*hm.Scheme]{}
	for _, arg := range f.Args {
		_, err := arg.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}
		scheme, found := env.SchemeOf(arg.Name.Name)
		if !found {
			return nil, fmt.Errorf("argument %q not found in environment after inference", arg.Name.Name)
		}
		finalArgType, isMono := scheme.Type()
		if !isMono {
			return nil, fmt.Errorf("argument %q has polymorphic type %s", arg.Name.Name, scheme)
		}

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
		args = append(args, Keyed[*hm.Scheme]{Key: arg.Name.Name, Value: signatureScheme, Positional: false})
	}
	return args, nil
}

// createFunctionValue creates a FunctionValue from processed arguments
func (f *FunctionBase) createFunctionValue(env EvalEnv, fnType *hm.FunctionType) FunctionValue {
	argNames := make([]string, len(f.Args))
	defaults := make(map[string]Node)

	for i, arg := range f.Args {
		argNames[i] = arg.Name.Name
		if arg.Value != nil {
			defaults[arg.Name.Name] = arg.Value
		}
	}

	var blockParamName string
	if f.BlockParam != nil {
		blockParamName = f.BlockParam.Name.Name
	}

	return FunctionValue{
		Args:           argNames,
		Body:           f.Body,
		Closure:        env,
		FnType:         fnType,
		Defaults:       defaults,
		ArgDecls:       f.Args, // Preserve original argument declarations with directives
		BlockParamName: blockParamName,
	}
}

// inferFunctionType provides shared type inference logic for functions
func (f *FunctionBase) inferFunctionType(ctx context.Context, env hm.Env, fresh hm.Fresher, explicitRetType TypeNode, contextName string) (*hm.FunctionType, error) {
	// Clone environment for closure semantics
	newEnv := env.Clone()

	// Assign early so we can still use partial inference results.
	f.InferredScope = newEnv.(Env)

	// Process arguments using shared logic
	args, err := f.inferFunctionArguments(ctx, newEnv, fresh)
	if err != nil {
		return nil, fmt.Errorf("%s.Infer: %w", contextName, err)
	}

	// Process block parameter if present
	var blockType *hm.FunctionType
	if f.BlockParam != nil {
		// Infer block parameter type
		blockParamType, err := f.BlockParam.Type_.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("%s.Infer block parameter: %w", contextName, err)
		}

		// Block parameter must be a function type
		bt, ok := blockParamType.(*hm.FunctionType)
		if !ok {
			return nil, fmt.Errorf("%s.Infer: block parameter must be a function type, got %T", contextName, blockParamType)
		}
		blockType = bt

		// Add block parameter to environment as a callable value
		blockParamName := f.BlockParam.Name.Name
		newEnv.Add(blockParamName, hm.NewScheme(nil, blockType))
	}

	// Handle explicit return type if provided
	var definedRet hm.Type
	if explicitRetType != nil {
		definedRet, err = explicitRetType.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("%s.Infer return type: %w", contextName, err)
		}
	}

	// Infer return type from function body
	inferredRet, err := f.Body.Infer(ctx, newEnv, fresh)
	if err != nil {
		return nil, fmt.Errorf("%s.Infer body: %w", contextName, err)
	}

	// Unify explicit and inferred return types if both exist
	if definedRet != nil {
		if _, err := hm.Assignable(inferredRet, definedRet); err != nil {
			return nil, NewInferError(fmt.Errorf("return type mismatch: declared %s, inferred %s", definedRet, inferredRet), f.Body)
		}
	}

	// Create function type with optional block parameter
	f.Inferred = hm.NewFnType(NewRecordType("", args...), inferredRet)
	if blockType != nil {
		f.Inferred.SetBlock(blockType)
	}
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
	InferredTypeHolder
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
	// FunDecl doesn't declare symbols - the parent SlotDecl does
	return nil
}

func (f *FunDecl) ReferencedSymbols() []string {
	// Function declarations reference symbols from their body
	return f.FunctionBase.Body.ReferencedSymbols()
}

func (f *FunDecl) Body() hm.Expression { return f.FunctionBase.Body }

func (f *FunDecl) GetSourceLocation() *SourceLocation { return f.Loc }

var _ hm.Inferer = &FunDecl{}
var _ Hoister = &FunDecl{}

func (f *FunDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	if pass == 0 {
		// Pass 0: Hoist function signature (declare type without inferring body)
		// Clone environment to avoid mutating original during signature inference
		signatureEnv := env.Clone()

		// Process arguments to get function signature
		args, err := f.FunctionBase.inferFunctionArguments(ctx, signatureEnv, fresh)
		if err != nil {
			return fmt.Errorf("FuncDecl.Hoist: %s signature: %w", f.Named, err)
		}

		// Process block parameter if present
		var blockType *hm.FunctionType
		if f.FunctionBase.BlockParam != nil {
			blockParamType, err := f.FunctionBase.BlockParam.Type_.Infer(ctx, env, fresh)
			if err != nil {
				return fmt.Errorf("FuncDecl.Hoist: %s block parameter: %w", f.Named, err)
			}
			bt, ok := blockParamType.(*hm.FunctionType)
			if !ok {
				return fmt.Errorf("FuncDecl.Hoist: %s block parameter must be a function type, got %T", f.Named, blockParamType)
			}
			blockType = bt
		}

		// Handle explicit return type if provided
		var retType hm.Type
		if f.Ret != nil {
			retType, err = f.Ret.Infer(ctx, env, fresh)
			if err != nil {
				return fmt.Errorf("FuncDecl.Hoist: %s return type: %w", f.Named, err)
			}
		} else {
			// Use a fresh type variable for the return type if not specified
			retType = fresh.Fresh()
		}

		// Create function type and add to environment
		fnType := hm.NewFnType(NewRecordType("", args...), retType)
		if blockType != nil {
			fnType.SetBlock(blockType)
		}
		env.Add(f.Named, hm.NewScheme(nil, fnType))
		return nil
	} else if pass == 1 {
		// Pass 1: Infer function body (function signature already available)
		// The actual inference will happen in the normal Infer method
		return nil
	}
	return nil
}

func (f *FunDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(f, func() (hm.Type, error) {
		return f.FunctionBase.inferFunctionType(ctx, env, fresh, f.Ret, fmt.Sprintf("FuncDecl(%s)", f.Named))
	})
}

func (f *FunDecl) Walk(fn func(Node) bool) {
	if !fn(f) {
		return
	}
	for _, arg := range f.Args {
		if !fn(arg) {
			continue
		}
		if arg.Type_ != nil {
			// TypeNode doesn't have Walk method - skip
		}
		if arg.Value != nil {
			arg.Value.Walk(fn)
		}
	}
	if f.Ret != nil {
		// TypeNode doesn't have Walk method - skip
	}
	f.FunctionBase.Body.Walk(fn)
}

type Reassignment struct {
	InferredTypeHolder
	Target   Node   // Left-hand side expression (Symbol, Select, etc.)
	Modifier string // "=" or "+=" etc.
	Value    Node   // Right-hand side expression
	Loc      *SourceLocation
}

var _ Node = (*Reassignment)(nil)
var _ Evaluator = (*Reassignment)(nil)

func (r *Reassignment) DeclaredSymbols() []string {
	return nil // Reassignments don't declare new symbols
}

func (r *Reassignment) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, r.Target.ReferencedSymbols()...)
	symbols = append(symbols, r.Value.ReferencedSymbols()...)
	return symbols
}

func (r *Reassignment) Body() hm.Expression { return r.Target }

func (r *Reassignment) GetSourceLocation() *SourceLocation { return r.Loc }

func (r *Reassignment) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(r, func() (hm.Type, error) {
		// Infer the type of the target (left-hand side)
		targetType, err := r.Target.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("Reassignment.Infer: target: %w", err)
		}

		// Infer the type of the value (right-hand side)
		valueType, err := r.Value.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("Reassignment.Infer: value: %w", err)
		}

		// For simple assignment, check compatibility
		if r.Modifier == "=" {
			if _, err := hm.Assignable(valueType, targetType); err != nil {
				return nil, fmt.Errorf("Reassignment.Infer: cannot assign %s to %s: %w", valueType, targetType, err)
			}
			return targetType, nil
		} else if r.Modifier == "+" {
			// For compound assignment, check that it's compatible with addition
			// Create a temporary Addition node to check type compatibility
			tempAddition := NewAddition(r.Target, r.Value, r.Loc)
			_, err := tempAddition.Infer(ctx, env, fresh)
			if err != nil {
				return nil, fmt.Errorf("Reassignment.Infer: compound assignment: %w", err)
			}
			return targetType, nil
		}

		return nil, fmt.Errorf("Reassignment.Infer: unsupported modifier %q", r.Modifier)
	})
}

func (r *Reassignment) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, r, func() (Value, error) {
		// Evaluate the value first
		value, err := EvalNode(ctx, env, r.Value)
		if err != nil {
			return nil, fmt.Errorf("Reassignment.Eval: evaluating value: %w", err)
		}

		// Handle different assignment types based on target
		switch target := r.Target.(type) {
		case *Symbol:
			// Simple variable assignment: x = value or x += value
			return r.evalVariableAssignment(ctx, env, target.Name, value)

		case *Select:
			// Field assignment: obj.field = value or obj.field += value
			return r.evalFieldAssignment(ctx, env, target, value)

		default:
			return nil, fmt.Errorf("Reassignment.Eval: unsupported assignment target type %T", r.Target)
		}
	})
}

func (r *Reassignment) evalVariableAssignment(ctx context.Context, env EvalEnv, varName string, value Value) (Value, error) {
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

func (r *Reassignment) evalFieldAssignment(ctx context.Context, env EvalEnv, selectNode *Select, value Value) (Value, error) {
	// Traverse the nested Select nodes to find the final receiver and the field to modify
	rootSymbol, path, err := r.getPath(selectNode)
	if err != nil {
		return nil, err
	}

	// Get the root object from the environment
	rootObj, found := env.Get(rootSymbol)
	if !found {
		return nil, fmt.Errorf("object %q not found", rootSymbol)
	}

	// Clone the root object to begin the copy-on-write process
	newRoot := rootObj.(EvalEnv).Clone()

	// Traverse the path, cloning objects as we go
	currentObj := newRoot
	for i := range len(path) - 1 {
		fieldName := path[i]
		val, found := currentObj.Get(fieldName)
		if !found {
			return nil, fmt.Errorf("field %q not found in object", fieldName)
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
			return nil, fmt.Errorf("field %q not found", finalField)
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

func (r *Reassignment) getPath(selectNode *Select) (string, []string, error) {
	var path []string
	var currentNode Node = selectNode

	// Traverse down the chain of Select nodes, collecting field names
	for {
		if s, ok := currentNode.(*Select); ok {
			path = append([]string{s.Field}, path...)
			currentNode = s.Receiver
		} else {
			break
		}
	}

	// The final node in the chain should be a Symbol (the root object)
	rootSymbol, ok := currentNode.(*Symbol)
	if !ok {
		return "", nil, fmt.Errorf("complex receivers must start with a symbol")
	}

	return rootSymbol.Name, path, nil
}

func (r *Reassignment) performAddition(left, right Value, varName string) (Value, error) {
	switch l := left.(type) {
	case IntValue:
		switch r := right.(type) {
		case IntValue:
			return IntValue{Val: l.Val + r.Val}, nil
		case FloatValue:
			return FloatValue{Val: float64(l.Val) + r.Val}, nil
		}
		return nil, fmt.Errorf("Reassignment.Eval: cannot add %T to int variable %q", right, varName)

	case FloatValue:
		switch r := right.(type) {
		case IntValue:
			return FloatValue{Val: l.Val + float64(r.Val)}, nil
		case FloatValue:
			return FloatValue{Val: l.Val + r.Val}, nil
		}
		return nil, fmt.Errorf("Reassignment.Eval: cannot add %T to float variable %q", right, varName)

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

func (r *Reassignment) createValueNode(value Value) Node {
	switch v := value.(type) {
	case IntValue:
		return &Int{Value: int64(v.Val), Loc: r.Loc}
	case StringValue:
		return &String{Value: v.Val, Loc: r.Loc}
	case BoolValue:
		return &Boolean{Value: v.Val, Loc: r.Loc}
	case NullValue:
		return &Null{Loc: r.Loc}
	default:
		// For complex values, we'll need a more sophisticated approach
		// For now, just create a Symbol that references the value
		return &Symbol{Name: fmt.Sprintf("__temp_value_%p", value), Loc: r.Loc}
	}
}

func (r *Reassignment) Walk(fn func(Node) bool) {
	if !fn(r) {
		return
	}
	r.Target.Walk(fn)
	r.Value.Walk(fn)
}

type Assert struct {
	InferredTypeHolder
	Message Node   // Optional message expression
	Block   *Block // Block containing the assertion expression
	Loc     *SourceLocation
}

var _ Node = (*Assert)(nil)
var _ Evaluator = (*Assert)(nil)

func (a *Assert) DeclaredSymbols() []string {
	return nil // Assert expressions don't declare anything
}

func (a *Assert) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, a.Block.ReferencedSymbols()...)
	if a.Message != nil {
		symbols = append(symbols, a.Message.ReferencedSymbols()...)
	}
	return symbols
}

func (a *Assert) Body() hm.Expression { return a.Block }

func (a *Assert) GetSourceLocation() *SourceLocation { return a.Loc }

func (a *Assert) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(a, func() (hm.Type, error) {
		// Infer the block type - the assertion will be evaluated
		_, err := a.Block.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// Infer the message type if present
		if a.Message != nil {
			_, err := a.Message.Infer(ctx, env, fresh)
			if err != nil {
				return nil, err
			}
		}

		// Assert returns nothing (unit type / null)
		return hm.TypeVariable('a'), nil
	})
}

func (a *Assert) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, a, func() (Value, error) {
		// Evaluate the block (gets the last expression's value)
		blockVal, err := EvalNode(ctx, env, a.Block)
		if err != nil {
			return nil, err
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
		return nil, a.createAssertionError(ctx, env, lastExpr)
	})
}

// createAssertionError builds a detailed error message with child node values
func (a *Assert) createAssertionError(ctx context.Context, env EvalEnv, expr Node) error {
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
	InferredTypeHolder
	Name string
	Node Node
}

// getImmediateChildren extracts immediate child nodes for error reporting
func (a *Assert) getImmediateChildren(expr Node) []ChildNode {
	switch n := expr.(type) {
	case *Select:
		// Handle both field access and method calls
		var children []ChildNode

		// Add receiver if present
		if n.Receiver != nil {
			children = append(children, ChildNode{
				Name: "receiver",
				Node: n.Receiver,
			})
		}

		return children

	case *FunCall:
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

	case *List:
		// List elements
		var children []ChildNode
		for i, elem := range n.Elements {
			children = append(children, ChildNode{
				Name: fmt.Sprintf("[%d]", i),
				Node: elem,
			})
		}
		return children

	case *Default:
		// Default operator children
		return []ChildNode{
			{Name: "left", Node: n.Left},
			{Name: "right", Node: n.Right},
		}

	case *Equality:
		// Equality operator children
		return []ChildNode{
			{Name: "left", Node: n.Left},
			{Name: "right", Node: n.Right},
		}

	case *Conditional:
		// Conditional expression children
		return []ChildNode{
			{Name: "condition", Node: n.Condition},
		}

	case *Let:
		// Let expression children
		return []ChildNode{
			{Name: "value", Node: n.Value},
		}
	}

	return nil
}

// nodeToString converts a node to its string representation
func (a *Assert) nodeToString(node Node) string {
	switch n := node.(type) {
	case *Symbol:
		return n.Name
	case *Select:
		if n.Receiver == nil {
			return n.Field
		}
		receiver := a.nodeToString(n.Receiver)
		return fmt.Sprintf("%s.%s", receiver, n.Field)
	case *FunCall:
		fun := a.nodeToString(n.Fun)
		return fmt.Sprintf("%s(...)", fun)
	case *String:
		return fmt.Sprintf("\"%s\"", n.Value)
	case *Int:
		return fmt.Sprintf("%d", n.Value)
	case *Boolean:
		return fmt.Sprintf("%t", n.Value)
	case *Null:
		return "null"
	case *List:
		return "[...]"
	case *Default:
		left := a.nodeToString(n.Left)
		right := a.nodeToString(n.Right)
		return fmt.Sprintf("%s ?? %s", left, right)
	case *Equality:
		left := a.nodeToString(n.Left)
		right := a.nodeToString(n.Right)
		return fmt.Sprintf("%s == %s", left, right)
	case *Conditional:
		condition := a.nodeToString(n.Condition)
		return fmt.Sprintf("if %s { ... }", condition)
	case *Let:
		return fmt.Sprintf("let %s = %s in ...", n.Name, a.nodeToString(n.Value))
	default:
		return fmt.Sprintf("%T", node)
	}
}

func (a *Assert) Walk(fn func(Node) bool) {
	if !fn(a) {
		return
	}
	if a.Message != nil {
		a.Message.Walk(fn)
	}
	a.Block.Walk(fn)
}

// DirectiveLocation represents a valid location where a directive can be applied
type DirectiveLocation struct {
	InferredTypeHolder
	Name string
}

// DirectiveDecl represents a directive declaration
type DirectiveDecl struct {
	InferredTypeHolder
	Name      string
	Args      []*SlotDecl
	Locations []DirectiveLocation
	DocString string
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

func (d *DirectiveDecl) Hoist(ctx context.Context, env hm.Env, fresh hm.Fresher, pass int) error {
	if pass == 0 {
		// Add directive to environment during hoisting so it's available for later use
		if e, ok := env.(Env); ok {
			e.AddDirective(d.Name, d)
		}
	}
	return nil
}

func (d *DirectiveDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Validate argument types
	for _, arg := range d.Args {
		if arg.Type_ != nil {
			_, err := arg.Type_.Infer(ctx, env, fresh)
			if err != nil {
				return nil, fmt.Errorf("DirectiveDecl.Infer: arg %q type: %w", arg.Name.Name, err)
			}
		}
		if arg.Value != nil {
			_, err := arg.Value.Infer(ctx, env, fresh)
			if err != nil {
				return nil, fmt.Errorf("DirectiveDecl.Infer: arg %q default value: %w", arg.Name.Name, err)
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

func (d *DirectiveDecl) Walk(fn func(Node) bool) {
	if !fn(d) {
		return
	}
	for _, arg := range d.Args {
		if arg.Value != nil {
			arg.Value.Walk(fn)
		}
		// TypeNode doesn't have Walk method - skip arg.Type_
	}
}

// DirectiveApplication represents the application of a directive
type DirectiveApplication struct {
	InferredTypeHolder
	Name string
	Args []Keyed[Node]
	Loc  *SourceLocation
}

var _ Node = (*DirectiveApplication)(nil)

func (d *DirectiveApplication) DeclaredSymbols() []string {
	return nil // Directive applications don't declare anything
}

func (d *DirectiveApplication) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, d.Name) // Reference the directive name
	for _, arg := range d.Args {
		symbols = append(symbols, arg.Value.ReferencedSymbols()...)
	}
	return symbols
}

func (d *DirectiveApplication) Body() hm.Expression { return nil }

func (d *DirectiveApplication) GetSourceLocation() *SourceLocation { return d.Loc }

func (d *DirectiveApplication) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(d, func() (hm.Type, error) {
		// Validate that the directive exists and arguments match
		if e, ok := env.(Env); ok {
			directiveDecl, found := e.GetDirective(d.Name)
			if !found {
				return nil, fmt.Errorf("DirectiveApplication.Infer: directive @%s not declared", d.Name)
			}

			// Validate arguments match the directive declaration
			err := d.validateArguments(ctx, directiveDecl, env, fresh)
			if err != nil {
				return nil, fmt.Errorf("DirectiveApplication.Infer: %w", err)
			}
		}

		// Directive applications don't have a meaningful type for inference
		return hm.TypeVariable('d'), nil
	})
}

func (d *DirectiveApplication) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Directive applications are compile-time annotations, no runtime evaluation
	return NullValue{}, nil
}

func (d *DirectiveApplication) Walk(fn func(Node) bool) {
	if !fn(d) {
		return
	}
	for _, arg := range d.Args {
		arg.Value.Walk(fn)
	}
}

// validateArguments checks that directive application arguments match the declaration
func (d *DirectiveApplication) validateArguments(ctx context.Context, decl *DirectiveDecl, env hm.Env, fresh hm.Fresher) error {
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
		providedArg, provided := providedArgs[declArg.Name.Name]

		if !provided {
			// Check if argument has a default value
			if declArg.Value == nil {
				// Check if the argument type is nullable (optional)
				if declArg.Type_ != nil {
					argType, err := declArg.Type_.Infer(ctx, env, fresh)
					if err != nil {
						return err
					}
					if _, isNonNull := argType.(hm.NonNullType); isNonNull {
						return fmt.Errorf("required argument %q not provided", declArg.Name.Name)
					}
				}
			}
			continue
		}

		// Validate provided argument type matches declared type
		if declArg.Type_ != nil {
			expectedType, err := declArg.Type_.Infer(ctx, env, fresh)
			if err != nil {
				return fmt.Errorf("failed to infer expected type for argument %q: %w", declArg.Name.Name, err)
			}

			providedType, err := providedArg.Infer(ctx, env, fresh)
			if err != nil {
				return fmt.Errorf("failed to infer type for provided argument %q: %w", declArg.Name.Name, err)
			}

			// Use type unification instead of equality to allow non-null types to be provided where nullable types are expected
			if _, err := hm.Assignable(providedType, expectedType); err != nil {
				return fmt.Errorf("argument %q type mismatch: expected %s, got %s", declArg.Name.Name, expectedType, providedType)
			}
		}
	}

	// Check for unexpected arguments
	for argName := range providedArgs {
		found := false
		for _, declArg := range decl.Args {
			if declArg.Name.Name == argName {
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

// ImportDecl represents a GraphQL schema import statement
type ImportDecl struct {
	InferredTypeHolder
	Source string  // The source identifier (e.g., "api.github.com", "dagger")
	Alias  *string // Optional alias (e.g., "GH")
	Loc    *SourceLocation

	client   graphql.Client
	schema   *introspection.Schema
	inferred Env
}

var _ Node = &ImportDecl{}
var _ Declarer = &ImportDecl{}

func (i *ImportDecl) IsDeclarer() bool {
	return true
}

func (i *ImportDecl) DeclaredSymbols() []string {
	if i.Alias != nil {
		return []string{*i.Alias} // Import with alias declares the alias symbol
	}
	return nil // Import without alias doesn't declare symbols (imported globally)
}

func (i *ImportDecl) ReferencedSymbols() []string {
	return nil // Imports don't reference existing symbols
}

func (i *ImportDecl) Body() hm.Expression { return nil }

func (i *ImportDecl) GetSourceLocation() *SourceLocation { return i.Loc }

var _ hm.Inferer = &ImportDecl{}

func (i *ImportDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	if err := i.loadSchema(ctx, env); err != nil {
		return nil, err
	}
	return i.inferred, nil
}

func (i *ImportDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	if i.inferred == nil {
		return nil, fmt.Errorf("ImportDecl.Eval: import not properly inferred")
	}

	// Create evaluation environment for the imported schema
	moduleEnv := NewEvalEnvWithSchema(i.inferred, i.client, i.schema)

	if i.Alias != nil {
		// Set the module in the evaluation environment
		env.Set(*i.Alias, moduleEnv)
	} else {
		// For global imports, the functions should already be in the global scope
		// Nothing to do at evaluation time for global imports
	}

	return moduleEnv, nil
}

// loadSchema performs the actual GraphQL schema loading and type environment setup
func (i *ImportDecl) loadSchema(ctx context.Context, env hm.Env) error {
	if i.inferred != nil {
		// If we've already inferred, skip.
		// (This is defensive.)
		return nil
	}

	// Create the schema provider and perform introspection during hoisting
	provider, err := i.createSchemaProvider()
	if err != nil {
		return fmt.Errorf("ImportDecl.loadSchema: failed to create schema provider: %w", err)
	}

	// Perform actual schema introspection now during hoisting
	client, schema, err := provider.GetClientAndSchema(ctx)
	if err != nil {
		return fmt.Errorf("ImportDecl.loadSchema: failed to introspect schema for %q: %w", i.Source, err)
	}

	// Add the import declaration to the environment for later resolution
	if dangEnv, ok := env.(Env); ok {
		if i.Alias != nil {
			// Import with alias - check for conflicts and create schema module
			if err := i.checkAliasConflicts(dangEnv, *i.Alias); err != nil {
				return err
			}

			// Create module with GraphQL schema types and functions
			schemaModule := NewEnv(schema)

			// Store client and schema information for runtime
			i.client = client
			i.schema = schema

			// Register type for the module
			// TOOD: is this useful? i guess it means we can pass GH around which is
			// kinda neat?
			dangEnv.AddClass(*i.Alias, schemaModule)

			// Also add as a scheme so the type checker can find it
			dangEnv.Add(*i.Alias, hm.NewScheme(nil, schemaModule))

			i.inferred = schemaModule
		} else {
			panic("TODO")
			// Import without alias - check for global import conflicts and add to global scope
			if err := i.checkGlobalImportConflicts(dangEnv, i.Source); err != nil {
				return err
			}

			// Add GraphQL functions directly to global scope
			err := i.addSchemaToGlobalScope(schema, dangEnv)
			if err != nil {
				return fmt.Errorf("ImportDecl.loadSchema: failed to add schema to global scope: %w", err)
			}

			// Store client and schema information for global imports
			i.client = client
			i.schema = schema
		}
	}

	return nil
}

// createSchemaProvider creates a GraphQLClientProvider for the import source
func (i *ImportDecl) createSchemaProvider() (*GraphQLClientProvider, error) {
	if i.Source == "dagger" {
		// Special case for Dagger - use empty config to trigger Dagger connection
		return NewGraphQLClientProvider(GraphQLConfig{}), nil
	}

	// For other sources, create config based on source URL and environment variables
	config := i.createConfigForSource(i.Source)
	return NewGraphQLClientProvider(config), nil
}

// createConfigForSource creates a GraphQLConfig for a given source
func (i *ImportDecl) createConfigForSource(source string) GraphQLConfig {
	// Map common API sources to their GraphQL endpoints
	endpoint := i.resolveEndpoint(source)

	// Get credentials from environment variables based on source
	auth := i.resolveAuth(source)

	return GraphQLConfig{
		Endpoint:      endpoint,
		Authorization: auth,
		Headers:       i.resolveHeaders(source),
	}
}

// resolveEndpoint maps import sources to GraphQL endpoints
func (i *ImportDecl) resolveEndpoint(source string) string {
	switch source {
	case "api.github.com":
		return "https://api.github.com/graphql"
	default:
		// For other sources, assume they're direct URLs or domain names
		if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
			return source
		}
		// Respect an alternative path
		if strings.Contains(source, "/") {
			return fmt.Sprintf("https://%s", source)
		}
		// Assume it's a domain and append /graphql
		return fmt.Sprintf("https://%s/graphql", source)
	}
}

// resolveAuth resolves authentication for the given source
func (i *ImportDecl) resolveAuth(source string) string {
	switch source {
	case "api.github.com":
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			return fmt.Sprintf("Bearer %s", token)
		}
	}

	// Check for generic auth environment variable
	envVar := fmt.Sprintf("DANG_%s_TOKEN", strings.ToUpper(strings.ReplaceAll(source, ".", "_")))
	if token := os.Getenv(envVar); token != "" {
		return fmt.Sprintf("Bearer %s", token)
	}

	return ""
}

// resolveHeaders resolves additional headers for the given source
func (i *ImportDecl) resolveHeaders(source string) map[string]string {
	headers := make(map[string]string)

	// Add common headers based on source
	switch source {
	case "api.github.com":
		headers["User-Agent"] = "Dang-GraphQL-Client/1.0"
	}

	return headers
}

func (i *ImportDecl) Walk(fn func(Node) bool) {
	fn(i)
	// ImportDecl has no child nodes to walk
}

// checkAliasConflicts checks if an import alias conflicts with existing symbols
func (i *ImportDecl) checkAliasConflicts(env Env, alias string) error {
	// Check if alias conflicts with existing classes (types)
	if _, exists := env.NamedType(alias); exists {
		return fmt.Errorf("import alias %q conflicts with existing type", alias)
	}

	// Check if alias conflicts with existing variables/functions
	if _, exists := env.LocalSchemeOf(alias); exists {
		return fmt.Errorf("import alias %q conflicts with existing symbol", alias)
	}

	// Check if alias conflicts with built-in types
	builtinTypes := []string{"String", "Int", "Boolean"}
	for _, builtin := range builtinTypes {
		if alias == builtin {
			return fmt.Errorf("import alias %q conflicts with built-in type %s", alias, builtin)
		}
	}

	return nil
}

// checkGlobalImportConflicts checks if a global import conflicts with existing imports
func (i *ImportDecl) checkGlobalImportConflicts(env Env, source string) error {
	// For now, allow multiple global imports - conflict detection can be enhanced later
	// TODO: Implement proper global import conflict detection

	return nil
}

// addSchemaToGlobalScope adds GraphQL functions directly to the global scope
func (i *ImportDecl) addSchemaToGlobalScope(schema *introspection.Schema, env Env) error {
	// Add Query type functions to global scope
	for _, t := range schema.Types {
		if t.Name == schema.QueryType.Name {
			for _, f := range t.Fields {
				ret, err := gqlToTypeNode(env, f.TypeRef)
				if err != nil {
					continue
				}

				args := NewRecordType("")
				for _, arg := range f.Args {
					argType, err := gqlToTypeNode(env, arg.TypeRef)
					if err != nil {
						continue
					}
					args.Add(arg.Name, hm.NewScheme(nil, argType))
				}

				fnType := hm.NewFnType(args, ret)
				env.Add(f.Name, hm.NewScheme(nil, fnType))
			}
			break
		}
	}

	return nil
}
