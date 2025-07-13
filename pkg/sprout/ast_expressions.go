package sprout

import (
	"context"
	"fmt"

	"github.com/vito/sprout/introspection"
	"github.com/vito/sprout/pkg/hm"
	"github.com/vito/sprout/pkg/ioctx"
	"github.com/vito/sprout/pkg/querybuilder"
)

// FunCall represents a function call expression
type FunCall struct {
	Fun  Node
	Args Record
	Loc  *SourceLocation
}

var _ Node = FunCall{}
var _ Evaluator = FunCall{}

func (c FunCall) DeclaredSymbols() []string {
	return nil // Function calls don't declare anything
}

func (c FunCall) ReferencedSymbols() []string {
	var symbols []string

	// Add symbols from the function being called
	symbols = append(symbols, c.Fun.ReferencedSymbols()...)

	// Add symbols from arguments
	for _, arg := range c.Args {
		symbols = append(symbols, arg.Value.ReferencedSymbols()...)
	}

	return symbols
}

func (c FunCall) Body() hm.Expression { return c.Fun }

func (c FunCall) GetSourceLocation() *SourceLocation { return c.Loc }

func (c FunCall) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(c, func() (hm.Type, error) {
		fun, err := c.Fun.Infer(env, fresh)
		if err != nil {
			return nil, err
		}

		switch ft := fun.(type) {
		case *hm.FunctionType:
			return c.inferFunctionType(env, fresh, ft)
		default:
			return nil, fmt.Errorf("FunCall.Infer: expected function, got %s (%T)", fun, fun)
		}
	})
}

// inferFunctionType handles type inference for FunctionType calls
func (c FunCall) inferFunctionType(env hm.Env, fresh hm.Fresher, ft *hm.FunctionType) (hm.Type, error) {
	// Handle positional argument mapping for type inference
	argMapping, err := c.mapArgumentsForInference(ft)
	if err != nil {
		return nil, err
	}

	// Type check each argument
	for i, arg := range c.Args {
		k := c.getArgumentKey(arg, argMapping, i)
		err := c.checkArgumentType(env, fresh, arg.Value, ft.Arg().(*RecordType), k)
		if err != nil {
			return nil, err
		}
	}

	// Check that all required arguments are provided
	err = c.validateRequiredArgumentsInInfer(ft)
	if err != nil {
		return nil, err
	}

	return ft.Ret(false), nil
}

// getArgumentKey determines the argument key for positional or named arguments
func (c FunCall) getArgumentKey(arg Keyed[Node], argMapping map[int]string, index int) string {
	if arg.Positional {
		return argMapping[index]
	}
	return arg.Key
}

// checkArgumentType validates an argument's type against the expected parameter type
func (c FunCall) checkArgumentType(env hm.Env, fresh hm.Fresher, value Node, recordType *RecordType, key string) error {
	it, err := value.Infer(env, fresh)
	if err != nil {
		return fmt.Errorf("FunCall.Infer: %w", err)
	}

	scheme, has := recordType.SchemeOf(key)
	if !has {
		return fmt.Errorf("FunCall.Infer: %q not found in %s", key, recordType)
	}

	dt, isMono := scheme.Type()
	if !isMono {
		return fmt.Errorf("FunCall.Infer: %q is not monomorphic", key)
	}

	if _, err := hm.Unify(dt, it); err != nil {
		return NewInferError(err.Error(), value)
	}
	return nil
}

var _ hm.Apply = FunCall{}

func (c FunCall) Fn() hm.Expression { return c.Fun }

func (c FunCall) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, c, func() (Value, error) {
		funVal, err := EvalNode(ctx, env, c.Fun)
		if err != nil {
			// Don't wrap errors - let the specific node error bubble up
			return nil, err
		}

		// Evaluate arguments and handle positional/named argument mapping
		argValues, err := c.evaluateArguments(ctx, env, funVal)
		if err != nil {
			return nil, err
		}

		// Dispatch to appropriate function call handler
		return c.callFunction(ctx, env, funVal, argValues)
	})
}

// callFunction dispatches function calls to appropriate handlers
func (c FunCall) callFunction(ctx context.Context, env EvalEnv, funVal Value, argValues map[string]Value) (Value, error) {
	switch fn := funVal.(type) {
	case BoundMethod:
		return c.callBoundMethod(ctx, fn, argValues)
	case FunctionValue:
		return c.callFunctionValue(ctx, fn, argValues)
	case GraphQLFunction:
		return fn.Call(ctx, env, argValues)
	case BuiltinFunction:
		return fn.Call(ctx, env, argValues)
	case *ConstructorFunction:
		return fn.Call(ctx, env, argValues)
	default:
		return nil, fmt.Errorf("FunCall.Eval: %T is not callable", funVal)
	}
}

// callBoundMethod handles BoundMethod function calls
func (c FunCall) callBoundMethod(ctx context.Context, fn BoundMethod, argValues map[string]Value) (Value, error) {
	// Create a composite environment that includes both the receiver and the method's closure
	recv := fn.Receiver.Fork()
	fnEnv := CreateCompositeEnv(recv, fn.Method.Closure)
	fnEnv.Set("self", recv)

	// Bind arguments to the function environment
	err := c.bindArgumentsToEnv(ctx, fnEnv, fn.Method.Args, fn.Method.Defaults, argValues, fnEnv)
	if err != nil {
		return nil, err
	}

	return EvalNode(ctx, fnEnv, fn.Method.Body)
}

// callFunctionValue handles FunctionValue function calls
func (c FunCall) callFunctionValue(ctx context.Context, fn FunctionValue, argValues map[string]Value) (Value, error) {
	// Create new environment with argument bindings
	fnEnv := fn.Closure.Clone()

	// Bind arguments to the function environment
	err := c.bindArgumentsToEnv(ctx, fnEnv, fn.Args, fn.Defaults, argValues, fn.Closure)
	if err != nil {
		return nil, err
	}

	return EvalNode(ctx, fnEnv, fn.Body)
}

// bindArgumentsToEnv handles the common logic of binding arguments to function environments
func (c FunCall) bindArgumentsToEnv(ctx context.Context, fnEnv EvalEnv, paramNames []string,
	defaults map[string]Node, argValues map[string]Value, defaultEvalEnv EvalEnv) error {
	for _, argName := range paramNames {
		if val, exists := argValues[argName]; exists {
			// Handle null values with defaults
			if _, isNull := val.(NullValue); isNull {
				if defaultExpr, hasDefault := defaults[argName]; hasDefault {
					defaultVal, err := EvalNode(ctx, defaultEvalEnv, defaultExpr)
					if err != nil {
						return fmt.Errorf("evaluating default value for argument %q: %w", argName, err)
					}
					fnEnv.Set(argName, defaultVal)
				} else {
					fnEnv.Set(argName, val)
				}
			} else {
				fnEnv.Set(argName, val)
			}
		} else if defaultExpr, hasDefault := defaults[argName]; hasDefault {
			// Use default value when argument not provided
			defaultVal, err := EvalNode(ctx, defaultEvalEnv, defaultExpr)
			if err != nil {
				return fmt.Errorf("evaluating default value for argument %q: %w", argName, err)
			}
			fnEnv.Set(argName, defaultVal)
		}
	}
	return nil
}

// evaluateArguments handles both positional and named arguments
func (c FunCall) evaluateArguments(ctx context.Context, env EvalEnv, funVal Value) (map[string]Value, error) {
	// Validate argument order first
	if err := c.validateArgumentOrder(); err != nil {
		return nil, err
	}

	argValues := make(map[string]Value)
	positionallySet := make(map[string]bool)
	paramNames := c.getParameterNames(funVal)

	// Process all arguments
	positionalIndex := 0
	for _, arg := range c.Args {
		val, err := EvalNode(ctx, env, arg.Value)
		if err != nil {
			return nil, err
		}

		if arg.Positional {
			err := c.handlePositionalArgument(arg, val, argValues, positionallySet, paramNames, &positionalIndex)
			if err != nil {
				return nil, err
			}
		} else {
			err := c.handleNamedArgument(arg, val, argValues, positionallySet)
			if err != nil {
				return nil, err
			}
		}
	}

	return argValues, nil
}

// validateArgumentOrder ensures positional args come before named args
func (c FunCall) validateArgumentOrder() error {
	seenNamed := false
	for _, arg := range c.Args {
		if arg.Positional && seenNamed {
			return fmt.Errorf("positional arguments must come before named arguments")
		}
		if !arg.Positional {
			seenNamed = true
		}
	}
	return nil
}

// handlePositionalArgument processes a positional argument
func (c FunCall) handlePositionalArgument(arg Keyed[Node], val Value, argValues map[string]Value,
	positionallySet map[string]bool, paramNames []string, positionalIndex *int) error {
	if *positionalIndex >= len(paramNames) {
		return fmt.Errorf("too many positional arguments: got %d, expected at most %d",
			*positionalIndex+1, len(paramNames))
	}
	paramName := paramNames[*positionalIndex]
	if _, exists := argValues[paramName]; exists {
		return fmt.Errorf("argument %q specified both positionally and by name", paramName)
	}
	argValues[paramName] = val
	positionallySet[paramName] = true
	*positionalIndex++
	return nil
}

// handleNamedArgument processes a named argument
func (c FunCall) handleNamedArgument(arg Keyed[Node], val Value, argValues map[string]Value,
	positionallySet map[string]bool) error {
	if _, exists := argValues[arg.Key]; exists {
		if positionallySet[arg.Key] {
			return fmt.Errorf("argument %q specified both positionally and by name", arg.Key)
		} else {
			return fmt.Errorf("argument %q specified multiple times", arg.Key)
		}
	}
	argValues[arg.Key] = val
	return nil
}

// getParameterNames extracts parameter names from a function value
func (c FunCall) getParameterNames(funVal Value) []string {
	switch fn := funVal.(type) {
	case BoundMethod:
		return fn.Method.Args
	case FunctionValue:
		return fn.Args
	case GraphQLFunction:
		// For GraphQL functions, get parameter names from the function type
		if ft, ok := fn.FnType.Arg().(*RecordType); ok {
			names := make([]string, len(ft.Fields))
			for i, field := range ft.Fields {
				names[i] = field.Key
			}
			return names
		}
	case BuiltinFunction:
		// For builtin functions, get parameter names from the function type
		if ft, ok := fn.FnType.Arg().(*RecordType); ok {
			names := make([]string, len(ft.Fields))
			for i, field := range ft.Fields {
				names[i] = field.Key
			}
			return names
		}
	case *ConstructorFunction:
		// For constructor functions, get parameter names from the constructor parameters
		names := make([]string, len(fn.Parameters))
		for i, param := range fn.Parameters {
			names[i] = param.Named
		}
		return names
	}
	return nil
}

// mapArgumentsForInference maps positional arguments to parameter names during type inference
func (c FunCall) mapArgumentsForInference(ft *hm.FunctionType) (map[int]string, error) {
	argMapping := make(map[int]string)

	// Get parameter names from the function type
	paramNames := []string{}
	if rt, ok := ft.Arg().(*RecordType); ok {
		paramNames = make([]string, len(rt.Fields))
		for i, field := range rt.Fields {
			paramNames[i] = field.Key
		}
	}

	// Validate positional args come before named args
	seenNamed := false
	for _, arg := range c.Args {
		if arg.Positional && seenNamed {
			return nil, fmt.Errorf("positional arguments must come before named arguments")
		}
		if !arg.Positional {
			seenNamed = true
		}
	}

	// Map positional arguments to parameter names by index
	positionalIndex := 0
	for i, arg := range c.Args {
		if arg.Positional {
			if positionalIndex >= len(paramNames) {
				return nil, fmt.Errorf("too many positional arguments: got %d, expected at most %d",
					positionalIndex+1, len(paramNames))
			}
			argMapping[i] = paramNames[positionalIndex]
			positionalIndex++
		}
	}

	return argMapping, nil
}

// validateRequiredArgumentsInInfer checks that all required arguments are provided during type inference
func (c FunCall) validateRequiredArgumentsInInfer(ft *hm.FunctionType) error {
	// Get the record type that represents the function arguments
	rt, ok := ft.Arg().(*RecordType)
	if !ok {
		return nil // Not a record type, no validation needed
	}

	// Build a set of provided argument names for quick lookup
	providedArgs := make(map[string]bool)
	argMapping, err := c.mapArgumentsForInference(ft)
	if err != nil {
		return err
	}

	for i, arg := range c.Args {
		var key string
		if arg.Positional {
			key = argMapping[i]
		} else {
			key = arg.Key
		}
		providedArgs[key] = true
	}

	// Check each parameter in the function signature
	for _, field := range rt.Fields {
		paramName := field.Key
		scheme := field.Value

		// Skip if this argument was provided
		if providedArgs[paramName] {
			continue
		}

		// Get the type of this parameter
		paramType, isMono := scheme.Type()
		if !isMono {
			continue // Skip polymorphic parameters for now
		}

		// Check if this parameter is required (hm.NonNullType)
		// With our transformation, arguments with defaults are now nullable in the signature,
		// so only truly required arguments (without defaults) will be NonNull here
		if _, isNonNull := paramType.(hm.NonNullType); isNonNull {
			return NewInferError(fmt.Sprintf("missing required argument: %q", paramName), c)
		}
	}

	return nil
}

// Symbol represents a symbol/variable reference
type Symbol struct {
	Name     string
	AutoCall bool
	Loc      *SourceLocation
}

var _ Node = Symbol{}
var _ Evaluator = Symbol{}

func (s Symbol) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(s, func() (hm.Type, error) {
		scheme, found := env.SchemeOf(s.Name)
		if !found {
			return nil, fmt.Errorf("%q not found", s.Name)
		}
		t, _ := scheme.Type()
		if s.AutoCall {
			t, _ = autoCallFnType(t)
		}
		return t, nil
	})
}

func (s Symbol) Body() hm.Expression { return s }

func (s Symbol) GetSourceLocation() *SourceLocation { return s.Loc }

func (s Symbol) DeclaredSymbols() []string {
	return nil // Symbols don't declare anything
}

func (s Symbol) ReferencedSymbols() []string {
	return []string{s.Name} // Symbols reference themselves
}

func (s Symbol) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, s, func() (Value, error) {
		val, found := env.Get(s.Name)
		if !found {
			return nil, fmt.Errorf("Symbol.Eval: %q not found in env: %+v", s.Name, env)
		}

		if val == nil {
			return nil, fmt.Errorf("Symbol: found nil value for %q", s.Name)
		}

		// Auto-call zero-arity functions when accessed as symbols
		if s.AutoCall && isAutoCallableFn(val) {
			return autoCallFn(ctx, env, val)
		}

		return val, nil
	})
}

// Select represents field selection or method call
type Select struct {
	Receiver Node
	Field    string
	AutoCall bool
	Loc      *SourceLocation
}

var _ Node = Select{}
var _ Evaluator = Select{}

func (d Select) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(d, func() (hm.Type, error) {
		// Handle nil receiver (symbol calls) - look up type in environment
		if d.Receiver == nil {
			scheme, found := env.SchemeOf(d.Field)
			if !found {
				return nil, fmt.Errorf("%q not found in env", d.Field)
			}
			t, _ := scheme.Type()
			return t, nil
		}

		// Handle normal receiver
		lt, err := d.Receiver.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("Receiver.Infer: %w", err)
		}

		// Check if receiver is nullable or non-null
		var rec Env
		var isNullable bool

		if nn, ok := lt.(hm.NonNullType); ok {
			// Non-null receiver
			envType, ok := nn.Type.(Env)
			if !ok {
				return nil, fmt.Errorf("Select.Infer: expected %T, got %T", envType, nn.Type)
			}
			rec = envType
			isNullable = false
		} else if envType, ok := lt.(Env); ok {
			// Nullable receiver - inherit nullability
			rec = envType
			isNullable = true
		} else {
			return nil, fmt.Errorf("Select.Infer: expected NonNullType or Env, got %T", lt)
		}

		scheme, found := rec.SchemeOf(d.Field)
		if !found {
			return nil, fmt.Errorf("field %q not found in record %s", d.Field, rec)
		}
		t, mono := scheme.Type()
		if !mono {
			return nil, fmt.Errorf("Select.Infer: type of field %q is not monomorphic", d.Field)
		}
		if d.AutoCall {
			t, _ = autoCallFnType(t)
		}

		// If receiver was nullable, make result nullable too
		if isNullable {
			// Remove any existing NonNullType wrapper from the field type
			if nnType, ok := t.(hm.NonNullType); ok {
				return nnType.Type, nil
			}
			// Field type is already nullable, return as-is
			return t, nil
		}

		return t, nil
	})
}

func (d Select) DeclaredSymbols() []string {
	return nil // Select expressions don't declare anything
}

func (d Select) ReferencedSymbols() []string {
	var symbols []string

	// When Receiver is nil, this is a top-level function call like createPerson()
	if d.Receiver == nil {
		symbols = append(symbols, d.Field)
	} else {
		symbols = append(symbols, d.Receiver.ReferencedSymbols()...)
	}

	return symbols
}

func (d Select) Body() hm.Expression { return d }

func (d Select) GetSourceLocation() *SourceLocation { return d.Loc }

func (d Select) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, d, func() (Value, error) {
		var receiverVal Value
		var err error

		// Handle normal receiver evaluation
		if d.Receiver != nil {
			receiverVal, err = EvalNode(ctx, env, d.Receiver)
			if err != nil {
				// Don't wrap SourceErrors - let them bubble up directly
				if _, isSourceError := err.(*SourceError); isSourceError {
					return nil, err
				}
				return nil, fmt.Errorf("evaluating receiver: %w", err)
			}
		} else {
			receiverVal = env
		}

		val, err := (func() (Value, error) {
			switch rec := receiverVal.(type) {
			case NullValue:
				// Null propagation: if receiver is null, result is null
				return NullValue{}, nil

			case EvalEnv:
				if val, found := rec.Get(d.Field); found {
					// If this is a FunctionValue accessed from a module, bind it to the receiver
					if fnVal, isFunctionValue := val.(FunctionValue); isFunctionValue {
						return BoundMethod{Method: fnVal, Receiver: rec}, nil
					}
					return val, nil
				}
				// this shouldn't happen (should be caught at type checking)
				return nil, fmt.Errorf("module %q does not have a field %q", rec, d.Field)

			case GraphQLValue:
				// Handle GraphQL field selection
				return rec.SelectField(ctx, d.Field)

			default:
				return nil, fmt.Errorf("Select.Eval: cannot select field %q from %T (value: %q). Expected a record or module value, but got %T", d.Field, receiverVal, receiverVal.String(), receiverVal)
			}
		})()
		if err != nil {
			return nil, err
		}

		// Auto-call zero-arity functions when accessed as symbols
		if d.AutoCall && isAutoCallableFn(val) {
			return autoCallFn(ctx, env, val)
		}

		return val, nil
	})
}

// Index represents list indexing operations like foo[0]
type Index struct {
	Receiver Node
	Index    Node
	AutoCall bool
	Loc      *SourceLocation
}

var _ Node = Index{}
var _ Evaluator = Index{}

func (i Index) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(i, func() (hm.Type, error) {
		// Infer the type of the receiver (should be a list)
		receiverType, err := i.Receiver.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("Index.Infer receiver: %w", err)
		}

		// Infer the type of the index (should be Int!)
		indexType, err := i.Index.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("Index.Infer index: %w", err)
		}

		// Check that index is Int!
		intType, err := NonNullTypeNode{NamedTypeNode{"Int"}}.Infer(env, fresh)
		if err != nil {
			return nil, err
		}
		if _, err := hm.Unify(indexType, intType); err != nil {
			return nil, fmt.Errorf("index must be Int!, got %s", indexType)
		}

		// Extract element type from list type
		var elementType hm.Type
		var isNullable bool

		if nonNull, ok := receiverType.(hm.NonNullType); ok {
			// Non-null list
			if listType, ok := nonNull.Type.(ListType); ok {
				elementType = listType.Type
				isNullable = false // Non-null list, but indexing could be out of bounds
			} else {
				return nil, fmt.Errorf("cannot index non-list type %s", receiverType)
			}
		} else if listType, ok := receiverType.(ListType); ok {
			// Nullable list
			elementType = listType.Type
			isNullable = true
		} else {
			return nil, fmt.Errorf("cannot index non-list type %s", receiverType)
		}

		// Apply auto-call if needed
		if i.AutoCall {
			elementType, _ = autoCallFnType(elementType)
		}

		// Return nullable element type since indexing can fail (out of bounds)
		// or if the original list was nullable
		if isNullable {
			// Remove NonNull wrapper if present, since nullable list means nullable result
			if nonNullElem, ok := elementType.(hm.NonNullType); ok {
				return nonNullElem.Type, nil
			}
			return elementType, nil
		} else {
			// Even for non-null lists, indexing can fail, so return nullable
			if nonNullElem, ok := elementType.(hm.NonNullType); ok {
				return nonNullElem.Type, nil
			}
			return elementType, nil
		}
	})
}

func (i Index) DeclaredSymbols() []string {
	return nil // Index expressions don't declare anything
}

func (i Index) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, i.Receiver.ReferencedSymbols()...)
	symbols = append(symbols, i.Index.ReferencedSymbols()...)
	return symbols
}

func (i Index) Body() hm.Expression { return i }

func (i Index) GetSourceLocation() *SourceLocation { return i.Loc }

func (i Index) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, i, func() (Value, error) {
		// Evaluate the receiver
		receiverVal, err := EvalNode(ctx, env, i.Receiver)
		if err != nil {
			return nil, fmt.Errorf("evaluating receiver: %w", err)
		}

		// Handle null receiver
		if _, ok := receiverVal.(NullValue); ok {
			return NullValue{}, nil
		}

		// Evaluate the index
		indexVal, err := EvalNode(ctx, env, i.Index)
		if err != nil {
			return nil, fmt.Errorf("evaluating index: %w", err)
		}

		// Check that receiver is a list
		listVal, ok := receiverVal.(ListValue)
		if !ok {
			return nil, fmt.Errorf("cannot index non-list value of type %T", receiverVal)
		}

		// Check that index is an integer
		intVal, ok := indexVal.(IntValue)
		if !ok {
			return nil, fmt.Errorf("index must be an integer, got %T", indexVal)
		}

		// Check bounds
		idx := int(intVal.Val)
		if idx < 0 || idx >= len(listVal.Elements) {
			// Return null for out-of-bounds access (nullable behavior)
			return NullValue{}, nil
		}

		// Get the element
		element := listVal.Elements[idx]

		// Auto-call zero-arity functions when accessed
		if i.AutoCall && isAutoCallableFn(element) {
			return autoCallFn(ctx, env, element)
		}

		return element, nil
	})
}

// FieldSelection represents a field name in an object selection
type FieldSelection struct {
	Name      string
	Selection *ObjectSelection // For nested selections like profile.{bio, avatar}
	Loc       *SourceLocation
}

func (f FieldSelection) GetSourceLocation() *SourceLocation { return f.Loc }

// ObjectSelection represents multi-field selection like obj.{field1, field2}
type ObjectSelection struct {
	Receiver Node
	Fields   []FieldSelection
	Loc      *SourceLocation

	Inferred *Module
	IsList   bool // TODO respect
}

var _ Node = (*ObjectSelection)(nil)
var _ Evaluator = (*ObjectSelection)(nil)

func (o *ObjectSelection) DeclaredSymbols() []string {
	return nil // Object selections don't declare anything
}

func (o *ObjectSelection) ReferencedSymbols() []string {
	return o.Receiver.ReferencedSymbols()
}

func (o *ObjectSelection) Body() hm.Expression { return o }

func (o *ObjectSelection) GetSourceLocation() *SourceLocation { return o.Loc }

func (o *ObjectSelection) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(o, func() (hm.Type, error) {
		// Infer the type of the receiver
		receiverType, err := o.Receiver.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("ObjectSelection.Infer: %w", err)
		}

		// Handle regular object types
		t, err := o.inferSelectionType(receiverType, env, fresh)
		if err != nil {
			return nil, err
		}

		// If this is a list selection, wrap the result in a list type
		if o.IsList {
			listType := ListType{Type: t}

			// If receiver was nullable, make result nullable too
			if _, ok := receiverType.(hm.NonNullType); ok {
				// Receiver was non-null, result should be non-null list
				return hm.NonNullType{Type: listType}, nil
			} else {
				// Receiver was nullable, result should be nullable list
				return listType, nil
			}
		}

		// If receiver was nullable, make result nullable too
		if _, ok := receiverType.(hm.NonNullType); ok {
			// Receiver was non-null, result should be non-null
			return hm.NonNullType{Type: t}, nil
		} else {
			// Receiver was nullable, result should be nullable
			return t, nil
		}
	})
}

func (o *ObjectSelection) inferSelectionType(receiverType hm.Type, env hm.Env, fresh hm.Fresher) (*Module, error) {
	// Check if receiver is nullable or non-null
	var rec Env

	// Handle list types - apply selection to each element
	var innerType hm.Type
	if listType, ok := receiverType.(hm.NonNullType); ok {
		if innerListType, ok := listType.Type.(ListType); ok {
			innerType = innerListType.Type
		}
	} else if listType, ok := receiverType.(ListType); ok {
		innerType = listType.Type
	}

	if innerType != nil {
		elementType, err := o.inferSelectionType(innerType, env, fresh)
		if err != nil {
			return nil, err
		}
		o.Inferred = elementType
		o.IsList = true
		return elementType, nil
	}

	if nn, ok := receiverType.(hm.NonNullType); ok {
		// Non-null receiver
		envType, ok := nn.Type.(Env)
		if !ok {
			return nil, fmt.Errorf("ObjectSelection.inferSelectionType: expected Env, got %T", nn.Type)
		}
		rec = envType
	} else if envType, ok := receiverType.(Env); ok {
		// Nullable receiver - we can still infer the selection type from the underlying type
		rec = envType
	} else {
		return nil, fmt.Errorf("ObjectSelection.inferSelectionType: expected NonNullType or Env, got %T", receiverType)
	}

	mod := NewModule("")
	for _, field := range o.Fields {
		fieldType, err := o.inferFieldType(field, rec, env, fresh)
		if err != nil {
			return nil, err
		}
		mod.Add(field.Name, hm.NewScheme(nil, fieldType))
	}
	o.Inferred = mod
	return mod, nil
}

func (o *ObjectSelection) inferFieldType(field FieldSelection, rec Env, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	fieldType, err := Symbol{
		Name:     field.Name,
		AutoCall: true,
		Loc:      o.Loc,
	}.Infer(rec, fresh)
	if err != nil {
		return nil, err
	}

	ret, _ := autoCallFnType(fieldType)

	// Handle nested selections
	if field.Selection != nil {
		t, err := field.Selection.inferSelectionType(fieldType, env, fresh)
		if err != nil {
			return nil, err
		}

		// If the nested selection is on a list, wrap the result in a list type
		if field.Selection.IsList {
			listType := ListType{Type: t}
			return hm.NonNullType{Type: listType}, nil
		}

		return hm.NonNullType{Type: t}, nil
	}

	return ret, nil
}

func (o *ObjectSelection) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, o, func() (Value, error) {
		receiverVal, err := EvalNode(ctx, env, o.Receiver)
		if err != nil {
			return nil, fmt.Errorf("ObjectSelection.Eval: %w", err)
		}

		// Handle null values - propagate null
		if _, ok := receiverVal.(NullValue); ok {
			return NullValue{}, nil
		}

		// Handle list types - apply selection to each element
		if listVal, ok := receiverVal.(ListValue); ok {
			var results []Value
			for _, elem := range listVal.Elements {
				result, err := o.evalSelectionOnValue(elem, ctx, env)
				if err != nil {
					return nil, err
				}
				results = append(results, result)
			}
			return ListValue{Elements: results}, nil
		}

		// Handle regular object types
		return o.evalSelectionOnValue(receiverVal, ctx, env)
	})
}

func (o *ObjectSelection) evalSelectionOnValue(val Value, ctx context.Context, env EvalEnv) (Value, error) {
	switch v := val.(type) {
	case NullValue:
		// Null propagation for individual values in lists
		return NullValue{}, nil
	case *ModuleValue:
		return o.evalModuleSelection(v, ctx, env)
	case GraphQLValue:
		return o.evalGraphQLSelection(v, ctx, env)
	default:
		return nil, fmt.Errorf("ObjectSelection.evalSelectionOnValue: expected *ModuleValue or GraphQLValue, got %T", val)
	}
}

func (o *ObjectSelection) evalModuleSelection(objVal *ModuleValue, ctx context.Context, env EvalEnv) (Value, error) {
	if o.Inferred == nil {
		return nil, fmt.Errorf("ObjectSelection.evalModuleSelection: inferred type is nil")
	}

	resultModuleValue := NewModuleValue(o.Inferred)

	// Build result object with selected fields
	for _, field := range o.Fields {
		fieldVal, exists := objVal.Get(field.Name)
		if !exists {
			return nil, fmt.Errorf("ObjectSelection.evalModuleSelection: field %q not found", field.Name)
		}

		// Handle nested selections
		if field.Selection != nil {
			fieldVal, err := field.Selection.evalSelectionOnValue(fieldVal, ctx, env)
			if err != nil {
				return nil, err
			}
			resultModuleValue.Set(field.Name, fieldVal)
		} else {
			resultModuleValue.Set(field.Name, fieldVal)
		}
	}

	return resultModuleValue, nil
}

func (o *ObjectSelection) evalGraphQLSelection(gqlVal GraphQLValue, ctx context.Context, env EvalEnv) (Value, error) {
	if o.Inferred == nil {
		return nil, fmt.Errorf("ObjectSelection.evalModuleSelection: inferred type is nil")
	}

	// Build optimized GraphQL query for all selected fields
	query, err := o.buildGraphQLQuery(gqlVal.QueryChain, o.Fields)
	if err != nil {
		return nil, fmt.Errorf("ObjectSelection.evalGraphQLSelection: %w", err)
	}

	q, err := query.Build(ctx)
	fmt.Fprintln(ioctx.StderrFromContext(ctx), q, err)

	// Execute the single optimized query
	var result any
	err = query.Client(gqlVal.Client).Bind(&result).Execute(ctx)
	if err != nil {
		return nil, fmt.Errorf("ObjectSelection.evalGraphQLSelection: executing GraphQL query: %w", err)
	}

	// Convert GraphQL result to ModuleValue
	return o.convertGraphQLResultToModule(result, o.Fields, gqlVal.Schema, gqlVal.Field)
}

func (o *ObjectSelection) buildGraphQLQuery(baseQuery *querybuilder.Selection, fields []FieldSelection) (*querybuilder.Selection, error) {
	// Start with the base query (which contains the context like "serverInfo")
	builder := baseQuery
	if builder == nil {
		builder = querybuilder.Query()
	}

	// Check if we have any nested selections
	hasNestedSelections := false
	for _, field := range fields {
		if field.Selection != nil {
			hasNestedSelections = true
			break
		}
	}

	if !hasNestedSelections {
		// Simple case: just select all fields using SelectFields
		fieldNames := make([]string, len(fields))
		for i, field := range fields {
			fieldNames[i] = field.Name
		}
		return builder.SelectFields(fieldNames...), nil
	}

	// Complex case: mix of simple fields and nested selections
	// Use SelectMixed to handle both types in a single selection set

	// Collect simple fields
	var simpleFields []string
	nestedSelections := make(map[string]*querybuilder.QueryBuilder)

	for _, field := range fields {
		if field.Selection == nil {
			simpleFields = append(simpleFields, field.Name)
		} else {
			// Handle nested selections
			nestedBuilder := querybuilder.Query()
			result, err := o.buildGraphQLQuery(nestedBuilder, field.Selection.Fields)
			if err != nil {
				return nil, err
			}
			// Use the result instead of the nestedBuilder
			nestedSelections[field.Name] = result
		}
	}

	builder = builder.SelectMixed(simpleFields, nestedSelections)

	return builder, nil
}

func (o *ObjectSelection) convertGraphQLResultToModule(result any, fields []FieldSelection, schema *introspection.Schema, parentField *introspection.Field) (Value, error) {
	// Check if the result is a list/slice
	if resultSlice, ok := result.([]any); ok {
		var elements []Value
		for _, item := range resultSlice {
			itemValue, err := o.convertGraphQLResultToModule(item, fields, schema, parentField)
			if err != nil {
				return nil, err
			}
			elements = append(elements, itemValue)
		}
		return ListValue{Elements: elements}, nil
	}

	resultModuleValue := NewModuleValue(o.Inferred)

	// Convert GraphQL result to Sprout values
	if resultMap, ok := result.(map[string]any); ok {
		for _, field := range fields {
			if fieldValue, exists := resultMap[field.Name]; exists {
				// Handle nested selections
				if field.Selection != nil {
					// Check if fieldValue is a list that needs selection applied to each element
					// Get the field information for the nested selection
					var nestedField *introspection.Field
					if parentField != nil && schema != nil {
						nestedField = o.getFieldFromParent(field.Name, parentField, schema)
					}

					if fieldSlice, isSlice := fieldValue.([]any); isSlice && field.Selection.IsList {
						var elements []Value
						for _, item := range fieldSlice {
							itemResult, err := field.Selection.convertGraphQLResultToModule(item, field.Selection.Fields, schema, nestedField)
							if err != nil {
								return nil, fmt.Errorf("ObjectSelection.convertGraphQLResultToModule: nested field %q item: %w", field.Name, err)
							}
							elements = append(elements, itemResult)
						}
						resultModuleValue.Set(field.Name, ListValue{Elements: elements})
					} else {
						nestedResult, err := field.Selection.convertGraphQLResultToModule(fieldValue, field.Selection.Fields, schema, nestedField)
						if err != nil {
							return nil, fmt.Errorf("ObjectSelection.convertGraphQLResultToModule: nested field %q: %w", field.Name, err)
						}
						resultModuleValue.Set(field.Name, nestedResult)
					}
				} else {
					// Convert GraphQL value to Sprout value using proper TypeRef
					var fieldTypeRef *introspection.TypeRef
					if parentField != nil && schema != nil {
						if gqlField := o.getFieldFromParent(field.Name, parentField, schema); gqlField != nil {
							fieldTypeRef = gqlField.TypeRef
						}
					}

					sproutVal, err := goValueToSprout(fieldValue, fieldTypeRef)
					if err != nil {
						return nil, fmt.Errorf("ObjectSelection.convertGraphQLResultToModule: converting field %q: %w", field.Name, err)
					}
					resultModuleValue.Set(field.Name, sproutVal)
				}
			}
		}
	}

	return resultModuleValue, nil
}

// getFieldFromParent finds a field by name in the parent field's return type
func (o *ObjectSelection) getFieldFromParent(fieldName string, parentField *introspection.Field, schema *introspection.Schema) *introspection.Field {
	if parentField == nil || schema == nil {
		return nil
	}

	// Get the return type of the parent field (unwrapping lists and non-nulls)
	returnType := o.unwrapType(parentField.TypeRef)
	if returnType == nil {
		return nil
	}

	// Find the type in the schema
	schemaType := schema.Types.Get(returnType.Name)
	if schemaType == nil {
		return nil
	}

	// Find the field in the type
	for _, field := range schemaType.Fields {
		if field.Name == fieldName {
			return field
		}
	}

	return nil
}

// unwrapType recursively unwraps LIST and NON_NULL wrappers to get the underlying named type
func (o *ObjectSelection) unwrapType(typeRef *introspection.TypeRef) *introspection.TypeRef {
	if typeRef == nil {
		return nil
	}

	switch typeRef.Kind {
	case "NON_NULL", "LIST":
		return o.unwrapType(typeRef.OfType)
	default:
		return typeRef
	}
}

// Conditional represents an if-then-else expression
type Conditional struct {
	Condition Node
	Then      Block
	Else      any
	Loc       *SourceLocation
}

var _ Node = Conditional{}
var _ Evaluator = Conditional{}

func (c Conditional) DeclaredSymbols() []string {
	return nil // Conditionals don't declare anything
}

func (c Conditional) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, c.Condition.ReferencedSymbols()...)
	symbols = append(symbols, c.Then.ReferencedSymbols()...)
	if c.Else != nil {
		elseBlock := c.Else.(Block)
		symbols = append(symbols, elseBlock.ReferencedSymbols()...)
	}
	return symbols
}

func (c Conditional) Body() hm.Expression { return c }

func (c Conditional) GetSourceLocation() *SourceLocation { return c.Loc }

func (c Conditional) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(c, func() (hm.Type, error) {
		condType, err := c.Condition.Infer(env, fresh)
		if err != nil {
			return nil, err
		}

		boolType, err := NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
		if err != nil {
			return nil, err
		}

		if _, err := hm.Unify(condType, boolType); err != nil {
			return nil, NewInferError(fmt.Sprintf("condition must be Boolean, got %s", condType), c.Condition)
		}

		// Analyze null assertions in the condition for flow-sensitive type checking
		assertions := AnalyzeNullAssertions(c.Condition)
		thenRefinements, elseRefinements, err := CreateTypeRefinements(assertions, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("creating type refinements: %w", err)
		}

		// Apply type refinements to the then branch
		thenEnv := ApplyTypeRefinements(env, thenRefinements)
		thenType, err := c.Then.Infer(thenEnv, fresh)
		if err != nil {
			return nil, err
		}

		if c.Else != nil {
			elseBlock := c.Else.(Block)

			// Apply type refinements to the else branch
			elseEnv := ApplyTypeRefinements(env, elseRefinements)
			elseType, err := elseBlock.Infer(elseEnv, fresh)
			if err != nil {
				return nil, err
			}

			if _, err := hm.Unify(thenType, elseType); err != nil {
				// Point to the specific else block for better error targeting
				var errorNode Node = elseBlock
				if len(elseBlock.Forms) > 0 {
					errorNode = elseBlock.Forms[len(elseBlock.Forms)-1] // Use the last form (the return value)
				}
				return nil, NewInferError(err.Error(), errorNode)
			}
		}

		return thenType, nil
	})
}

func (c Conditional) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, c, func() (Value, error) {
		condVal, err := EvalNode(ctx, env, c.Condition)
		if err != nil {
			return nil, fmt.Errorf("evaluating condition: %w", err)
		}

		boolVal, ok := condVal.(BoolValue)
		if !ok {
			return nil, fmt.Errorf("condition must evaluate to boolean, got %T", condVal)
		}

		if boolVal.Val {
			return EvalNode(ctx, env, c.Then)
		} else if c.Else != nil {
			elseBlock := c.Else.(Block)
			return EvalNode(ctx, env, elseBlock)
		} else {
			return NullValue{}, nil
		}
	})
}

// Let represents a let binding expression
type Let struct {
	Name  string
	Value Node
	Expr  Node
	Loc   *SourceLocation
}

var _ Node = Let{}
var _ Evaluator = Let{}

func (l Let) DeclaredSymbols() []string {
	return nil // Let expressions don't declare symbols in the global scope
}

func (l Let) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, l.Value.ReferencedSymbols()...)
	symbols = append(symbols, l.Expr.ReferencedSymbols()...)
	return symbols
}

func (l Let) Body() hm.Expression { return l }

func (l Let) GetSourceLocation() *SourceLocation { return l.Loc }

func (l Let) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(l, func() (hm.Type, error) {
		valueType, err := l.Value.Infer(env, fresh)
		if err != nil {
			return nil, err
		}

		newEnv := env.Clone()
		newEnv.Add(l.Name, hm.NewScheme(nil, valueType))

		return l.Expr.Infer(newEnv, fresh)
	})
}

func (l Let) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, l, func() (Value, error) {
		val, err := EvalNode(ctx, env, l.Value)
		if err != nil {
			return nil, fmt.Errorf("evaluating let value: %w", err)
		}

		newEnv := env.Clone()
		newEnv.Set(l.Name, val)

		return EvalNode(ctx, newEnv, l.Expr)
	})
}

// TypeHint represents a type hint expression using :: syntax
type TypeHint struct {
	Expr Node
	Type TypeNode
	Loc  *SourceLocation
}

var _ Node = TypeHint{}
var _ Evaluator = TypeHint{}

func (t TypeHint) DeclaredSymbols() []string {
	return nil // Type hints don't declare symbols
}

func (t TypeHint) ReferencedSymbols() []string {
	return t.Expr.ReferencedSymbols()
}

func (t TypeHint) Body() hm.Expression { return t }

func (t TypeHint) GetSourceLocation() *SourceLocation { return t.Loc }

func (t TypeHint) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(t, func() (hm.Type, error) {
		// Infer the type of the expression
		exprType, err := t.Expr.Infer(env, fresh)
		if err != nil {
			return nil, err
		}

		// Infer the type of the type hint
		hintType, err := t.Type.Infer(env, fresh)
		if err != nil {
			return nil, err
		}

		// For type hints, we want to bind type variables from the expression to concrete types in the hint
		// while allowing the hint to override things like nullability

		// Try to extract the "core" types for unification (removing nullability wrappers)
		exprCore := exprType
		hintCore := hintType

		// If expression is NonNull, extract the inner type for unification
		if exprNonNull, ok := exprType.(hm.NonNullType); ok {
			exprCore = exprNonNull.Type
		}

		// If hint is NonNull, extract the inner type for unification
		if hintNonNull, ok := hintType.(hm.NonNullType); ok {
			hintCore = hintNonNull.Type
		}

		// Try to unify the core types to bind type variables
		if subs, err := hm.Unify(exprCore, hintCore); err == nil {
			// Unification succeeded - apply substitutions to the hint and return it
			// This allows the hint to override the expression's type (including nullability)
			result := hintType.Apply(subs).(hm.Type)
			return result, nil
		}

		// Core unification failed, try the original approach with subtyping
		subs, err := hm.Unify(exprType, hintType)
		if err != nil {
			return nil, NewInferError(fmt.Sprintf("type hint mismatch: expression has type %s, but hint expects %s", exprType, hintType), t.Expr)
		}

		// Apply substitutions to the hint type and return it
		result := hintType.Apply(subs).(hm.Type)
		return result, nil
	})
}

func (t TypeHint) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, t, func() (Value, error) {
		// Type hints don't change runtime behavior - just evaluate the expression
		return EvalNode(ctx, env, t.Expr)
	})
}

// Lambda represents a lambda function expression
type Lambda struct {
	FunctionBase
}

var _ Node = &Lambda{}
var _ Evaluator = &Lambda{}

func (l *Lambda) DeclaredSymbols() []string {
	return nil // Lambdas don't declare symbols in the global scope
}

func (l *Lambda) ReferencedSymbols() []string {
	// Lambdas reference symbols from their body
	return l.FunctionBase.Body.ReferencedSymbols()
}

func (l *Lambda) Body() hm.Expression { return l }

func (l *Lambda) GetSourceLocation() *SourceLocation { return l.FunctionBase.Loc }

func (l *Lambda) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(l, func() (hm.Type, error) {
		return l.FunctionBase.inferFunctionType(env, fresh, true, nil, "Lambda")
	})
}

func (l *Lambda) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, l, func() (Value, error) {
		return l.FunctionBase.Eval(ctx, env)
	})
}
