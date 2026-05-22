package dang

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
)

type FunctionBase struct {
	InferredTypeHolder
	Args       []*SlotDecl
	BlockParam *SlotDecl // Optional block parameter (prefixed with &)
	Body       Node
	Directives []*DirectiveApplication
	Loc        *SourceLocation

	Inferred *hm.FunctionType

	InferredScope Env

	// ExpectedReturnType is set by checkArgumentType for bidirectional type inference
	ExpectedReturnType hm.Type
}

// inferFunctionArguments processes SlotDecl arguments into function type arguments
func (f *FunctionBase) inferFunctionArguments(ctx context.Context, env hm.Env, fresh hm.Fresher) ([]Keyed[*hm.Scheme], []Keyed[[]*DirectiveApplication], map[string]string, error) {
	return f.inferFunctionArgumentsWith(ctx, env, fresh, (*SlotDecl).Infer)
}

func (f *FunctionBase) declareFunctionSignatureArguments(ctx context.Context, env hm.Env, fresh hm.Fresher) ([]Keyed[*hm.Scheme], []Keyed[[]*DirectiveApplication], map[string]string, error) {
	return f.inferFunctionArgumentsWith(ctx, env, fresh, (*SlotDecl).DeclareSignature)
}

func (f *FunctionBase) inferFunctionArgumentsWith(ctx context.Context, env hm.Env, fresh hm.Fresher, inferArg func(*SlotDecl, context.Context, hm.Env, hm.Fresher) (hm.Type, error)) ([]Keyed[*hm.Scheme], []Keyed[[]*DirectiveApplication], map[string]string, error) {
	args := []Keyed[*hm.Scheme]{}
	directives := []Keyed[[]*DirectiveApplication]{}
	docStrings := make(map[string]string)
	for _, arg := range f.Args {
		finalArgType, err := inferArg(arg, ctx, env, fresh)
		if err != nil {
			return nil, nil, nil, err
		}

		if finalArgType == nil {
			scheme, found := env.SchemeOf(arg.Name.Name)
			if !found {
				return nil, nil, nil, fmt.Errorf("argument %q not found in environment after inference", arg.Name.Name)
			}
			var isMono bool
			finalArgType, isMono = scheme.Type()
			if !isMono {
				return nil, nil, nil, fmt.Errorf("argument %q has polymorphic type %s", arg.Name.Name, scheme)
			}
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
		args = append(args, Keyed[*hm.Scheme]{
			Key:        arg.Name.Name,
			Value:      signatureScheme,
			Positional: false,
		})
		if len(arg.Directives) > 0 {
			directives = append(directives, Keyed[[]*DirectiveApplication]{
				Key:        arg.Name.Name,
				Value:      arg.Directives,
				Positional: false,
			})
		}
		// Capture doc string if present
		if arg.DocString != "" {
			docStrings[arg.Name.Name] = arg.DocString
		}
	}
	return args, directives, docStrings, nil
}

func (f *FunctionBase) declareFunctionSignature(ctx context.Context, env hm.Env, fresh hm.Fresher, explicitRetType TypeNode, contextName string) (*hm.FunctionType, error) {
	// Clone environment for closure semantics and so argument names don't leak.
	newEnv := env.Clone()
	signatureCtx := contextWithInferFunctionControlBoundary(ctx)

	if dangEnv, ok := newEnv.(Env); ok {
		f.InferredScope = dangEnv
	}

	args, directives, docStrings, err := f.declareFunctionSignatureArguments(signatureCtx, newEnv, fresh)
	if err != nil {
		return nil, fmt.Errorf("%s.Declare: %w", contextName, err)
	}

	argsRec := NewRecordType("", args...)
	argsRec.Directives = directives
	argsRec.DocStrings = docStrings

	var blockType *hm.FunctionType
	if f.BlockParam != nil {
		blockParamType, err := f.BlockParam.Type_.Infer(signatureCtx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("%s.Declare block parameter: %w", contextName, err)
		}
		bt, ok := blockParamType.(*hm.FunctionType)
		if !ok {
			return nil, fmt.Errorf("%s.Declare: block parameter must be a function type, got %T", contextName, blockParamType)
		}
		blockType = bt
	}

	var retType hm.Type
	if explicitRetType != nil {
		retType, err = explicitRetType.Infer(signatureCtx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("%s.Declare return type: %w", contextName, err)
		}
	} else {
		retType = fresh.Fresh()
	}

	f.Inferred = hm.NewFnType(argsRec, retType)
	if blockType != nil {
		f.Inferred.SetBlock(blockType)
	}
	f.SetInferredType(f.Inferred)
	return f.Inferred, nil
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

	// Check if this function has access to dynamic scope
	_, hasDynamicScope := env.Self()

	return FunctionValue{
		Args:           argNames,
		Body:           f.Body,
		Closure:        env,
		FnType:         fnType,
		Defaults:       defaults,
		ArgDecls:       f.Args, // Preserve original argument declarations with directives
		Directives:     f.Directives,
		BlockParamName: blockParamName,
		IsDynamic:      hasDynamicScope,
	}
}

// inferFunctionType provides shared type inference logic for functions
func (f *FunctionBase) inferFunctionType(ctx context.Context, env hm.Env, fresh hm.Fresher, explicitRetType TypeNode, contextName string) (*hm.FunctionType, error) {
	// Clone environment for closure semantics
	newEnv := env.Clone()
	functionCtx := contextWithInferFunctionControlBoundary(ctx)

	// Assign early so we can still use partial inference results.
	f.InferredScope = newEnv.(Env)

	// Process arguments using shared logic. Defaults are evaluated at function
	// call time, so they must not inherit the caller's return/break/continue targets.
	args, directives, docStrings, err := f.inferFunctionArguments(functionCtx, newEnv, fresh)
	if err != nil {
		return nil, fmt.Errorf("%s.Infer: %w", contextName, err)
	}

	argsRec := NewRecordType("", args...)
	argsRec.Directives = directives
	argsRec.DocStrings = docStrings

	// Process block parameter if present
	var blockType *hm.FunctionType
	if f.BlockParam != nil {
		// Infer block parameter type
		blockParamType, err := f.BlockParam.Type_.Infer(functionCtx, env, fresh)
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

	// Infer return type from function body. A fresh return target lets
	// returns inside block args created in this body contribute to this
	// function, while nested functions/constructors get their own targets.
	// Ordinary functions are control-flow boundaries for break/continue; only
	// block arguments may intentionally hand those effects to an enclosing call.
	returnTarget := NewInferControlTarget(ReturnFrame)
	bodyCtx := contextWithInferReturnTarget(functionCtx, returnTarget)
	inferredRet, err := f.Body.Infer(bodyCtx, newEnv, fresh)
	if err != nil {
		return nil, fmt.Errorf("%s.Infer body: %w", contextName, err)
	}

	inferredRet, err = inferReturnTypeWithEarlyReturns(f.Body, inferredRet, definedRet, returnTarget)
	if err != nil {
		return nil, err
	}

	// Create function type with optional block parameter
	f.Inferred = hm.NewFnType(argsRec, inferredRet)
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
	switch pass {
	case 0:
		// Pass 0: Hoist function signature (declare type without inferring body).
		fnType, err := f.declareFunctionSignature(ctx, env, fresh, f.Ret, fmt.Sprintf("FuncDecl(%s)", f.Named))
		if err != nil {
			return err
		}
		env.Add(f.Named, hm.NewScheme(nil, fnType))
		if e, ok := env.(Env); ok {
			e.SetVisibility(f.Named, f.Visibility)
			if len(f.Directives) > 0 {
				e.SetDirectives(f.Named, f.Directives)
			}
		}
		return nil
	case 1:
		// Pass 1: Infer function body (function signature already available)
		// The actual inference will happen in the normal Infer method.
		return nil
	}
	return nil
}

func (f *FunDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(f, func() (hm.Type, error) {
		return f.inferFunctionType(ctx, env, fresh, f.Ret, fmt.Sprintf("FuncDecl(%s)", f.Named))
	})
}

func (f *FunDecl) Walk(fn func(Node) bool) {
	if !fn(f) {
		return
	}
	for _, d := range f.Directives {
		d.Walk(fn)
	}
	for _, arg := range f.Args {
		if !fn(arg) {
			continue
		}
		// TypeNode doesn't have Walk method - no action needed
		if arg.Value != nil {
			arg.Value.Walk(fn)
		}
	}
	// TypeNode doesn't have Walk method - no action needed
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
		// Infer the type of the target (left-hand side) as a storage location,
		// without auto-calling zero-arity functions stored in that location.
		targetType, err := inferNodeWithoutAutoCall(ctx, env, fresh, r.Target)
		if err != nil {
			return nil, fmt.Errorf("Reassignment.Infer: target: %w", err)
		}

		// Infer the type of the value (right-hand side)
		valueType, err := r.Value.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("Reassignment.Infer: value: %w", err)
		}

		// For simple assignment, check compatibility
		switch r.Modifier {
		case "=":
			if _, err := hm.Assignable(valueType, targetType); err != nil {
				if refErr := r.functionRefAssignmentError(ctx, env, fresh, targetType, valueType); refErr != nil {
					return nil, refErr
				}
				return nil, fmt.Errorf("Reassignment.Infer: cannot assign %s to %s: %w", valueType, targetType, err)
			}
			return targetType, nil
		case "+":
			// For compound assignment, check that it's compatible with addition
			// Create a temporary Addition node to check type compatibility
			tempAddition := NewAddition(r.Target, r.Value, r.Loc)
			_, err := tempAddition.Infer(ctx, env, fresh)
			if err != nil {
				return nil, fmt.Errorf("Reassignment.Infer: compound assignment: %w", err)
			}
			return targetType, nil
		default:
			return nil, fmt.Errorf("Reassignment.Infer: unsupported modifier %q", r.Modifier)
		}
	})
}

func (r *Reassignment) functionRefAssignmentError(ctx context.Context, env hm.Env, fresh hm.Fresher, targetType, valueType hm.Type) error {
	if _, targetIsFn := targetType.(*hm.FunctionType); !targetIsFn {
		return nil
	}

	valueFnType, err := inferNodeWithoutAutoCall(ctx, env, fresh, r.Value)
	if err != nil {
		return nil
	}
	if _, valueIsFn := valueFnType.(*hm.FunctionType); !valueIsFn {
		return nil
	}
	if _, err := hm.Assignable(valueFnType, targetType); err != nil {
		return nil
	}

	expr := strings.TrimSpace(Format(r.Value))
	if expr == "" {
		expr = "this expression"
	}
	return NewInferError(
		fmt.Errorf("assigning `%s` calls it and produces %s; use `&%s` to assign the function itself (%s)", expr, valueType, expr, targetType),
		r.Value,
	)
}

func (r *Reassignment) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, r, func() (Value, error) {
		// Evaluate the value first
		value, err := EvalNode(ctx, env, r.Value)
		if err != nil {
			return nil, fmt.Errorf("Reassignment.Eval: evaluating value: %w", err)
		}

		if r.Modifier == "=" {
			targetType := r.Target.GetInferredType()
			if targetType == nil {
				targetType = r.GetInferredType()
			}
			value, err = materializeValue(ctx, env, value, targetType, materializePathForNode(r.Target))
			if err != nil {
				return nil, err
			}
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
	switch r.Modifier {
	case "=":
		// Simple assignment: x = value
		if !env.Has(varName) {
			return nil, fmt.Errorf("Reassignment.Eval: variable %q not found", varName)
		}
		env.Update(varName, value)
		return value, nil
	case "+":
		// Compound assignment: x += value
		currentValue, found, err := env.Lookup(ctx, varName)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("Reassignment.Eval: variable %q not found", varName)
		}

		// Perform addition using existing Addition logic
		newValue, err := r.performAddition(currentValue, value, varName)
		if err != nil {
			return nil, err
		}

		env.Update(varName, newValue)
		return newValue, nil
	default:
		return nil, fmt.Errorf("Reassignment.Eval: unsupported modifier %q", r.Modifier)
	}
}

func (r *Reassignment) evalFieldAssignment(ctx context.Context, env EvalEnv, selectNode *Select, value Value) (Value, error) {
	// Traverse the nested Select nodes to find the final receiver and the field to modify
	rootNode, path, err := r.getPath(selectNode)
	if err != nil {
		return nil, err
	}

	// Get the root object from the environment or dynamic scope
	var rootObj Value
	var found bool
	var rootSymbolName string

	if _, isSelf := rootNode.(*SelfKeyword); isSelf {
		rootObj, found = env.Self()
		if !found {
			return nil, fmt.Errorf("'self' is not available in this context")
		}
	} else if rootSymbol, ok := rootNode.(*Symbol); ok {
		rootSymbolName = rootSymbol.Name
		rootObj, found, err = env.Lookup(ctx, rootSymbolName)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("object %q not found", rootSymbolName)
		}
	} else {
		return nil, fmt.Errorf("unexpected root node type: %T", rootNode)
	}

	// Clone the root object to begin the copy-on-write process
	newRoot := rootObj.(EvalEnv).Derive(false)

	// Traverse the path, cloning objects as we go
	currentObj := newRoot
	for i := range len(path) - 1 {
		fieldName := path[i]
		val, found, err := currentObj.Lookup(ctx, fieldName)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("field %q not found in object", fieldName)
		}
		clonedVal := val.(EvalEnv).Derive(false)
		currentObj.Bind(fieldName, clonedVal.(Value), currentObj.Visibility(fieldName))
		currentObj = clonedVal
	}

	// Get the final field to modify
	finalField := path[len(path)-1]

	// Now that we have the final receiver, perform the assignment
	switch r.Modifier {
	case "=":
		// Simple assignment: obj.field = value
		currentObj.Bind(finalField, value, currentObj.Visibility(finalField))
	case "+":
		// Compound assignment: obj.field += value
		currentValue, found, err := currentObj.Lookup(ctx, finalField)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("field %q not found", finalField)
		}

		// Perform addition using existing Addition logic
		newValue, err := r.performAddition(currentValue, value, finalField)
		if err != nil {
			return nil, err
		}

		currentObj.Bind(finalField, newValue, currentObj.Visibility(finalField))
	default:
		return nil, fmt.Errorf("Reassignment.Eval: unsupported modifier %q", r.Modifier)
	}

	// Update the root object in the environment (respects sealed scope boundaries)
	// For self, update the dynamic scope so subsequent references see the change
	if _, isSelf := rootNode.(*SelfKeyword); isSelf {
		env.MutateSelf(newRoot.(Value))
	} else {
		env.Update(rootSymbolName, newRoot.(Value))
	}

	return newRoot.(Value), nil
}

func (r *Reassignment) getPath(selectNode *Select) (Node, []string, error) {
	var path []string
	var currentNode Node = selectNode

	// Traverse down the chain of Select nodes, collecting field names
	for {
		if s, ok := currentNode.(*Select); ok {
			path = append([]string{s.Field.Name}, path...)
			currentNode = s.Receiver
		} else {
			break
		}
	}

	// The final node in the chain should be a Symbol or SelfKeyword (the root object)
	if _, ok := currentNode.(*Symbol); ok {
		return currentNode, path, nil
	}
	if _, ok := currentNode.(*SelfKeyword); ok {
		return currentNode, path, nil
	}
	return nil, nil, fmt.Errorf("complex receivers must start with a symbol or self keyword")
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

func (r *Reassignment) Walk(fn func(Node) bool) {
	if !fn(r) {
		return
	}
	r.Target.Walk(fn)
	r.Value.Walk(fn)
}

// DirectiveLocation represents a valid location where a directive can be applied
type DirectiveLocation struct {
	InferredTypeHolder
	Name string
}

// DirectiveDecl represents a directive declaration
type DirectiveDecl struct {
	InferredTypeHolder
	Name       string
	Args       []*SlotDecl
	Locations  []DirectiveLocation
	Directives []*DirectiveApplication
	DocString  string
	Loc        *SourceLocation
}

var _ Node = &DirectiveDecl{}
var _ Hoister = &DirectiveDecl{}

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
	Scope    *NamedTypeNode
	Name     string
	Args     []Keyed[Node]
	IsPrefix bool // True if directive appeared before the name (prefix position)
	Loc      *SourceLocation
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
		env := env.(Env)
		if d.Scope != nil {
			// If the directive is scoped, resolve the scope type
			scopeType, err := d.Scope.Infer(ctx, env, fresh)
			if err != nil {
				return nil, fmt.Errorf("DirectiveApplication.Infer: scope type: %w", err)
			}
			env = scopeType.(Env)
		}

		// Check for import conflicts before resolving
		if conflicts := env.CheckDirectiveConflict(d.Name); len(conflicts) > 0 {
			return nil, fmt.Errorf("ambiguous reference to directive @%s: provided by imports %v", d.Name, conflicts)
		}

		// Validate that the directive exists and arguments match
		directiveDecl, found := env.GetDirective(d.Name)
		if !found {
			return nil, fmt.Errorf("DirectiveApplication.Infer: directive @%s not declared", d.Name)
		}

		// Validate arguments match the directive declaration
		err := d.validateArguments(ctx, directiveDecl, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("DirectiveApplication.Infer: %w", err)
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
	// Validate positional args come before named args
	seenNamed := false
	for _, arg := range d.Args {
		if arg.Positional && seenNamed {
			return fmt.Errorf("positional arguments must come before named arguments")
		}
		if !arg.Positional {
			seenNamed = true
		}
	}

	// Map positional arguments to parameter names by index and resolve their keys
	positionalIndex := 0
	providedArgs := make(map[string]Node)

	for i, arg := range d.Args {
		if arg.Positional {
			if positionalIndex >= len(decl.Args) {
				return fmt.Errorf("too many positional arguments: got %d, expected at most %d",
					positionalIndex+1, len(decl.Args))
			}
			paramName := decl.Args[positionalIndex].Name.Name
			d.Args[i].Key = paramName
			providedArgs[paramName] = arg.Value
			// Resolve the positional argument's key for downstream consumers
			d.Args[i].Key = paramName
			positionalIndex++
		} else {
			providedArgs[arg.Key] = arg.Value
		}
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
	Name *Symbol // The import name (e.g., "Dagger", "Test")
	Loc  *SourceLocation

	client   graphql.Client
	schema   *introspection.Schema
	inferred Env
}

type ImportConfig struct {
	Name   string
	Client graphql.Client
	Schema *introspection.Schema

	// AutoImport causes this import to be injected into every file
	// without an explicit import statement. Used by SDKs (e.g. Dagger)
	// that want their import available implicitly.
	AutoImport bool

	// Dagger indicates this import connects to a Dagger Engine session.
	// Used by the LSP to find the client for module introspection.
	Dagger bool
}

type importConfigsKey struct{}
type schemaModuleCacheKey struct{}

// WithSchemaModuleCache attaches a name-keyed schema-module cache to ctx. The
// cache is consulted by ImportDecl.Infer so every ImportDecl with the same
// name — whether at file top level, in a sibling file, or nested inside a
// block — gets the same *Module identity. Without a shared cache, each
// NewEnv call produces a distinct module and types fail to unify.
//
// Callers that want the cache to persist across multiple inference passes
// (the LSP, for instance, where each keystroke is a fresh pass over the same
// directory) construct the cache themselves and reuse it. ContextWithImportConfigs
// auto-creates a per-call cache when none is attached, so one-shot callers
// don't have to.
func WithSchemaModuleCache(ctx context.Context, cache *sync.Map) context.Context {
	return context.WithValue(ctx, schemaModuleCacheKey{}, cache)
}

func ContextWithImportConfigs(ctx context.Context, configs ...ImportConfig) context.Context {
	if _, ok := ctx.Value(schemaModuleCacheKey{}).(*sync.Map); !ok {
		ctx = WithSchemaModuleCache(ctx, &sync.Map{})
	}
	return context.WithValue(ctx, importConfigsKey{}, configs)
}

func importConfigsFromContext(ctx context.Context) []ImportConfig {
	configs, _ := ctx.Value(importConfigsKey{}).([]ImportConfig)
	return configs
}

// sharedImportModule looks up the cached schema module for name in ctx. The
// cache is populated lazily by ImportDecl.Infer the first time a given import
// name is resolved, so subsequent inferences reuse the same module.
func sharedImportModule(ctx context.Context, name string) Env {
	cache, _ := ctx.Value(schemaModuleCacheKey{}).(*sync.Map)
	if cache == nil {
		return nil
	}
	v, ok := cache.Load(name)
	if !ok {
		return nil
	}
	return v.(Env)
}

func cacheImportModule(ctx context.Context, name string, mod Env) {
	if cache, ok := ctx.Value(schemaModuleCacheKey{}).(*sync.Map); ok {
		cache.Store(name, mod)
	}
}

var _ Node = &ImportDecl{}

func (i *ImportDecl) DeclaredSymbols() []string {
	return []string{i.Name.Name}
}

func (i *ImportDecl) ReferencedSymbols() []string {
	return nil // Imports don't reference existing symbols
}

func (i *ImportDecl) Body() hm.Expression { return nil }

func (i *ImportDecl) GetSourceLocation() *SourceLocation { return i.Loc }

var _ hm.Inferer = &ImportDecl{}

func (i *ImportDecl) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(i, func() (hm.Type, error) {
		// Resolve i.inferred to the shared schema module for this import name
		// in ctx. The context-scoped cache ensures every ImportDecl referring to
		// the same name — whether at file top level, in a sibling file, or
		// nested inside a block — gets the same *Module identity. Without it,
		// each NewEnv produces a distinct module and types fail to unify.
		//
		// Subsequent calls (e.g. LSP reusing cached blocks) reuse the
		// per-node i.inferred but still install into the current env, since
		// the env changes per file or per nested scope.
		if i.inferred == nil {
			if mod := sharedImportModule(ctx, i.Name.Name); mod != nil {
				i.inferred = mod
				if i.client == nil {
					config, err := i.loadImportConfig(ctx)
					if err != nil {
						return nil, err
					}
					i.client = config.Client
					i.schema = config.Schema
				}
			} else {
				config, err := i.loadImportConfig(ctx)
				if err != nil {
					return nil, err
				}
				i.client = config.Client
				i.schema = config.Schema
				i.inferred = NewEnv(i.Name.Name, config.Schema)
				cacheImportModule(ctx, i.Name.Name, i.inferred)
			}
		}

		if dangEnv, ok := env.(Env); ok {
			installImportedTypeEnvironment(dangEnv, i.Name.Name, i.inferred)
		}

		return NonNull(i.inferred), nil
	})
}

func (i *ImportDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	if i.inferred == nil {
		return nil, fmt.Errorf("ImportDecl.Eval: import not properly inferred")
	}

	// Create evaluation environment for the imported schema
	moduleEnv := NewEvalEnvWithSchema(i.inferred, i.client, i.schema)

	installImportedEvalEnvironment(env, i.Name.Name, moduleEnv)

	return moduleEnv, nil
}

// createSchemaProvider creates a GraphQLClientProvider for the import source
func (i *ImportDecl) loadImportConfig(ctx context.Context) (ImportConfig, error) {
	for _, config := range importConfigsFromContext(ctx) {
		if config.Name == i.Name.Name {
			if config.Schema == nil {
				var err error
				config.Schema, err = introspectSchema(ctx, config.Client)
				if err != nil {
					return ImportConfig{}, fmt.Errorf("failed to introspect schema for import %q: %w", i.Name.Name, err)
				}
			}
			return config, nil
		}
	}
	return ImportConfig{}, fmt.Errorf("no import config found for %q", i.Name.Name)
}

func (i *ImportDecl) Walk(fn func(Node) bool) {
	fn(i)
	// ImportDecl has no child nodes to walk
}

