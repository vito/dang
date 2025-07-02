package dash

import (
	"context"
	"fmt"

	"github.com/chewxy/hm"
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
	fun, err := c.Fun.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	switch ft := fun.(type) {
	case *hm.FunctionType:
		// Handle positional argument mapping for type inference
		argMapping, err := c.mapArgumentsForInference(ft)
		if err != nil {
			return nil, err
		}

		for i, arg := range c.Args {
			v := arg.Value
			var k string

			if arg.Positional {
				k = argMapping[i]
			} else {
				k = arg.Key
			}

			it, err := v.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("FunCall.Infer: %w", err)
			}

			scheme, has := ft.Arg().(*RecordType).SchemeOf(k)
			if !has {
				return nil, fmt.Errorf("FunCall.Infer: %q not found in %s", k, ft.Arg())
			}

			dt, isMono := scheme.Type()
			if !isMono {
				return nil, fmt.Errorf("FunCall.Infer: %q is not monomorphic", k)
			}

			if _, err := UnifyWithCompatibility(dt, it); err != nil {
				return nil, NewInferError(err.Error(), c)
			}
		}

		// Check that all required arguments are provided
		// Now that we've transformed types, this validation should work correctly
		err = c.validateRequiredArgumentsInInfer(ft)
		if err != nil {
			return nil, err
		}

		return ft.Ret(false), nil
	case *Module:
		// For modules, use the original logic for now TODO: Add proper positional
		// argument support for modules TODO: delete this actually? we don't
		// initialize modules by calling them anymore, we use 'new' and prototype
		// style cloning with COW
		for _, arg := range c.Args {
			k, v := arg.Key, arg.Value

			it, err := v.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("FunCall.Infer: %w", err)
			}

			scheme, has := ft.SchemeOf(k)
			if !has {
				return nil, fmt.Errorf("FunCall.Infer: %q not found in %s", k, ft)
			}

			dt, isMono := scheme.Type()
			if !isMono {
				return nil, fmt.Errorf("FunCall.Infer: %q is not monomorphic", k)
			}

			if _, err := UnifyWithCompatibility(dt, it); err != nil {
				return nil, NewInferError(err.Error(), c)
			}
		}
		return NonNullType{ft}, nil
	default:
		return nil, fmt.Errorf("FunCall.Infer: expected function, got %s (%T)", fun, fun)
	}
}

var _ hm.Apply = FunCall{}

func (c FunCall) Fn() hm.Expression { return c.Fun }

func (c FunCall) Eval(ctx context.Context, env EvalEnv) (Value, error) {
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

	switch fn := funVal.(type) {
	case BoundMethod:
		// BoundMethod - create new environment with receiver as 'self' and argument bindings
		// Create a composite environment that includes both the receiver and the method's closure
		recv := fn.Receiver.Clone().(*ModuleValue)
		fnEnv := createCompositeEnv(recv, fn.Method.Closure)
		fnEnv.Set("self", recv)

		for _, argName := range fn.Method.Args {
			if val, exists := argValues[argName]; exists {
				// Check if the value is null and we have a default
				if _, isNull := val.(NullValue); isNull {
					if defaultExpr, hasDefault := fn.Method.Defaults[argName]; hasDefault {
						// Use default value instead of null
						defaultVal, err := EvalNode(ctx, fn.Method.Closure, defaultExpr)
						if err != nil {
							return nil, fmt.Errorf("evaluating default value for argument %q: %w", argName, err)
						}
						fnEnv.Set(argName, defaultVal)
					} else {
						fnEnv.Set(argName, val)
					}
				} else {
					fnEnv.Set(argName, val)
				}
			} else if defaultExpr, hasDefault := fn.Method.Defaults[argName]; hasDefault {
				// Evaluate the default value in the function's closure
				defaultVal, err := EvalNode(ctx, fn.Method.Closure, defaultExpr)
				if err != nil {
					return nil, fmt.Errorf("evaluating default value for argument %q: %w", argName, err)
				}
				fnEnv.Set(argName, defaultVal)
			}
		}
		return EvalNode(ctx, fnEnv, fn.Method.Body)

	case FunctionValue:
		// Regular function call - create new environment with argument bindings
		fnEnv := fn.Closure.Clone()

		for _, argName := range fn.Args {
			if val, exists := argValues[argName]; exists {
				// Check if the value is null and we have a default
				if _, isNull := val.(NullValue); isNull {
					if defaultExpr, hasDefault := fn.Defaults[argName]; hasDefault {
						// Use default value instead of null
						defaultVal, err := EvalNode(ctx, fn.Closure, defaultExpr)
						if err != nil {
							return nil, fmt.Errorf("evaluating default value for argument %q: %w", argName, err)
						}
						fnEnv.Set(argName, defaultVal)
					} else {
						fnEnv.Set(argName, val)
					}
				} else {
					fnEnv.Set(argName, val)
				}
			} else if defaultExpr, hasDefault := fn.Defaults[argName]; hasDefault {
				// Evaluate the default value in the function's closure
				defaultVal, err := EvalNode(ctx, fn.Closure, defaultExpr)
				if err != nil {
					return nil, fmt.Errorf("evaluating default value for argument %q: %w", argName, err)
				}
				fnEnv.Set(argName, defaultVal)
			}
		}
		return EvalNode(ctx, fnEnv, fn.Body)

	case *ModuleValue:
		// Module function call - this would integrate with Dagger API
		// For now, return a placeholder
		return StringValue{Val: fmt.Sprintf("module call: %s with args %v", fn.Mod.Named, argValues)}, nil

	case GraphQLFunction:
		// GraphQL function call
		return fn.Call(ctx, env, argValues)

	case BuiltinFunction:
		// Builtin function call
		return fn.Call(ctx, env, argValues)

	default:
		return nil, fmt.Errorf("FunCall.Eval: %T is not callable", funVal)
	}
}

// evaluateArguments handles both positional and named arguments
func (c FunCall) evaluateArguments(ctx context.Context, env EvalEnv, funVal Value) (map[string]Value, error) {
	argValues := make(map[string]Value)
	positionallySet := make(map[string]bool) // Track which args were set positionally

	// Get parameter names from the function type
	paramNames := c.getParameterNames(funVal)

	// Track positional argument index
	positionalIndex := 0

	// First pass: ensure positional args come before named args
	seenNamed := false
	for _, arg := range c.Args {
		if arg.Positional && seenNamed {
			return nil, fmt.Errorf("positional arguments must come before named arguments")
		}
		if !arg.Positional {
			seenNamed = true
		}
	}

	// Second pass: evaluate and map arguments
	for _, arg := range c.Args {
		val, err := EvalNode(ctx, env, arg.Value)
		if err != nil {
			return nil, err
		}

		if arg.Positional {
			// Map positional argument to parameter name by index
			if positionalIndex >= len(paramNames) {
				return nil, fmt.Errorf("too many positional arguments: got %d, expected at most %d",
					positionalIndex+1, len(paramNames))
			}
			paramName := paramNames[positionalIndex]
			if _, exists := argValues[paramName]; exists {
				return nil, fmt.Errorf("argument %q specified both positionally and by name", paramName)
			}
			argValues[paramName] = val
			positionallySet[paramName] = true
			positionalIndex++
		} else {
			// Named argument
			if _, exists := argValues[arg.Key]; exists {
				if positionallySet[arg.Key] {
					return nil, fmt.Errorf("argument %q specified both positionally and by name", arg.Key)
				} else {
					return nil, fmt.Errorf("argument %q specified multiple times", arg.Key)
				}
			}
			argValues[arg.Key] = val
		}
	}

	return argValues, nil
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

		// Check if this parameter is required (NonNullType)
		// With our transformation, arguments with defaults are now nullable in the signature,
		// so only truly required arguments (without defaults) will be NonNull here
		if _, isNonNull := paramType.(NonNullType); isNonNull {
			return fmt.Errorf("FunCall.Infer: missing required argument: %q", paramName)
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
	scheme, found := env.SchemeOf(s.Name)
	if !found {
		return nil, NewInferError(fmt.Sprintf("%q not found", s.Name), s)
	}
	t, _ := scheme.Type()
	if s.AutoCall {
		return autoCallFnType(t), nil
	}
	return t, nil
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
	val, found := env.Get(s.Name)
	if !found {
		return nil, fmt.Errorf("Symbol.Eval: %q not found in env", s.Name)
	}

	if val == nil {
		return nil, fmt.Errorf("Symbol: found nil value for %q", s.Name)
	}

	// Auto-call zero-arity functions when accessed as symbols
	if s.AutoCall && isAutoCallableFn(val) {
		return autoCallFn(ctx, env, val)
	}

	return val, nil
}

// Select represents field selection or method call
type Select struct {
	Receiver Node
	Field    string
	Args     *Record // Optional: when present, this is a function call
	AutoCall bool
	Loc      *SourceLocation
}

var _ Node = Select{}
var _ Evaluator = Select{}

func (d Select) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// If this is a function call (Args present), delegate to FunCall
	// implementation
	if d.Args != nil {
		t, err := d.AsCall().Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("Select.Infer: %w", err)
		}
		return t, nil
	}

	// Handle nil receiver (symbol calls) - look up type in environment
	if d.Receiver == nil {
		scheme, found := env.SchemeOf(d.Field)
		if !found {
			return nil, fmt.Errorf("Select.Infer: %q not found in env", d.Field)
		}
		t, _ := scheme.Type()
		return t, nil
	}

	// Handle normal receiver
	lt, err := d.Receiver.Infer(env, fresh)
	if err != nil {
		return nil, fmt.Errorf("Receiver.Infer: %w", err)
	}
	nn, ok := lt.(NonNullType)
	if !ok {
		return nil, fmt.Errorf("Select.Infer: expected %T, got %T", nn, lt)
	}
	rec, ok := nn.Type.(Env)
	if !ok {
		return nil, fmt.Errorf("Select.Infer: expected %T, got %T", rec, nn.Type)
	}
	scheme, found := rec.SchemeOf(d.Field)
	if !found {
		return nil, fmt.Errorf("Select.Infer: field %q not found in record %s", d.Field, rec)
	}
	t, mono := scheme.Type()
	if !mono {
		return nil, fmt.Errorf("Select.Infer: type of field %q is not monomorphic", d.Field)
	}
	if d.AutoCall {
		return autoCallFnType(t), nil
	}
	return t, nil
}

func (d Select) AsCall() FunCall {
	var args Record
	if d.Args != nil {
		args = *d.Args
	}
	return FunCall{
		Fun: Select{
			Receiver: d.Receiver,
			Field:    d.Field,
			Loc:      d.Loc,
		},
		Args: args,
		Loc:  d.Loc,
	}
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

	// Add symbols from arguments
	if d.Args != nil {
		for _, arg := range *d.Args {
			symbols = append(symbols, arg.Value.ReferencedSymbols()...)
		}
	}

	return symbols
}

func (d Select) Body() hm.Expression { return d }

func (d Select) GetSourceLocation() *SourceLocation { return d.Loc }

func (d Select) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// If this is a function call (Args present), delegate to FunCall
	// implementation, which will properly handle setting 'self'
	if d.Args != nil {
		return d.AsCall().Eval(ctx, env)
	}

	// Handle nil receiver (symbol followed by ()) - select from environment
	if d.Receiver == nil {
		val, found := env.Get(d.Field)
		if !found {
			return nil, fmt.Errorf("Select.Eval: %q not found in env", d.Field)
		}
		return val, nil
	}

	var receiverVal Value
	var err error

	// Handle normal receiver evaluation
	receiverVal, err = EvalNode(ctx, env, d.Receiver)
	if err != nil {
		// Don't wrap SourceErrors - let them bubble up directly
		if _, isSourceError := err.(*SourceError); isSourceError {
			return nil, err
		}
		return nil, fmt.Errorf("evaluating receiver: %w", err)
	}

	val, err := (func() (Value, error) {
		switch rec := receiverVal.(type) {
		case EvalEnv:
			if val, found := rec.Get(d.Field); found {
				// If this is a FunctionValue accessed from a module, bind it to the receiver
				if fnVal, isFunctionValue := val.(FunctionValue); isFunctionValue {
					// Only bind if the receiver is a ModuleValue (class instance)
					if modVal, isModuleValue := receiverVal.(*ModuleValue); isModuleValue {
						return BoundMethod{Method: fnVal, Receiver: modVal}, nil
					}
				}
				return val, nil
			}
			// this shouldn't happen (should be caught at type checking)
			return nil, fmt.Errorf("module %q does not have a field %q", rec, d.Field)

		case GraphQLValue:
			// Handle GraphQL field selection
			return rec.SelectField(ctx, d.Field)

		default:
			err := fmt.Errorf("Select.Eval: cannot select field %q from %T (value: %q). Expected a record or module value, but got %T", d.Field, receiverVal, receiverVal.String(), receiverVal)
			return nil, CreateEvalError(ctx, err, d)
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
}

// getParameterNames extracts parameter names from a function value (similar to FunCall)
func (d Select) getParameterNames(funVal Value) []string {
	switch fn := funVal.(type) {
	case FunctionValue:
		return fn.Args
	case GraphQLFunction:
		if ft, ok := fn.FnType.Arg().(*RecordType); ok {
			names := make([]string, len(ft.Fields))
			for i, field := range ft.Fields {
				names[i] = field.Key
			}
			return names
		}
	case BuiltinFunction:
		if ft, ok := fn.FnType.Arg().(*RecordType); ok {
			names := make([]string, len(ft.Fields))
			for i, field := range ft.Fields {
				names[i] = field.Key
			}
			return names
		}
	}
	return nil
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
	condType, err := c.Condition.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	boolType, err := NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	if _, err := UnifyWithCompatibility(condType, boolType); err != nil {
		return nil, fmt.Errorf("Conditional.Infer: condition must be Boolean, got %s", condType)
	}

	thenType, err := c.Then.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	if c.Else != nil {
		elseBlock := c.Else.(Block)
		elseType, err := elseBlock.Infer(env, fresh)
		if err != nil {
			return nil, err
		}

		if _, err := UnifyWithCompatibility(thenType, elseType); err != nil {
			return nil, NewInferError("then and else branches have different types", c)
		}
	}

	return thenType, nil
}

func (c Conditional) Eval(ctx context.Context, env EvalEnv) (Value, error) {
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
	valueType, err := l.Value.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	newEnv := env.Clone()
	newEnv.Add(l.Name, hm.NewScheme(nil, valueType))

	return l.Expr.Infer(newEnv, fresh)
}

func (l Let) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	val, err := EvalNode(ctx, env, l.Value)
	if err != nil {
		return nil, fmt.Errorf("evaluating let value: %w", err)
	}

	newEnv := env.Clone()
	newEnv.Set(l.Name, val)

	return EvalNode(ctx, newEnv, l.Expr)
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
	return l.FunctionBase.inferFunctionType(env, fresh, true, nil, "Lambda")
}

func (l *Lambda) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return l.FunctionBase.Eval(ctx, env)
}