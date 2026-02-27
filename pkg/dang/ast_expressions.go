package dang

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
	"github.com/vito/dang/pkg/querybuilder"
)

// Grouped represents a parenthesized expression - used to preserve explicit grouping
type Grouped struct {
	InferredTypeHolder
	Expr Node
	Loc  *SourceLocation
}

var _ Node = (*Grouped)(nil)
var _ Evaluator = (*Grouped)(nil)

func (g *Grouped) DeclaredSymbols() []string {
	return g.Expr.DeclaredSymbols()
}

func (g *Grouped) ReferencedSymbols() []string {
	return g.Expr.ReferencedSymbols()
}

func (g *Grouped) Body() hm.Expression { return g.Expr }

func (g *Grouped) GetSourceLocation() *SourceLocation { return g.Loc }

func (g *Grouped) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	t, err := g.Expr.Infer(ctx, env, fresh)
	if err != nil {
		return nil, err
	}
	g.SetInferredType(t)
	return t, nil
}

func (g *Grouped) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return EvalNode(ctx, env, g.Expr)
}

func (g *Grouped) Walk(fn func(Node) bool) {
	if fn(g) {
		g.Expr.Walk(fn)
	}
}

// FunCall represents a function call expression
type FunCall struct {
	InferredTypeHolder
	Fun      Node
	Args     Record
	BlockArg *BlockArg // Optional block argument for bidirectional inference
	Loc      *SourceLocation
}

var _ Node = (*FunCall)(nil)
var _ Evaluator = (*FunCall)(nil)

func (c *FunCall) DeclaredSymbols() []string {
	return nil // Function calls don't declare anything
}

func (c *FunCall) ReferencedSymbols() []string {
	var symbols []string

	// Add symbols from the function being called
	symbols = append(symbols, c.Fun.ReferencedSymbols()...)

	// Add symbols from arguments
	for _, arg := range c.Args {
		symbols = append(symbols, arg.Value.ReferencedSymbols()...)
	}

	// Add symbols from block arg if present
	if c.BlockArg != nil {
		symbols = append(symbols, c.BlockArg.ReferencedSymbols()...)
	}

	return symbols
}

func (c *FunCall) Body() hm.Expression { return c.Fun }

func (c *FunCall) GetSourceLocation() *SourceLocation { return c.Loc }

func (c *FunCall) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(c, func() (hm.Type, error) {
		fun, err := c.Fun.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		switch ft := fun.(type) {
		case *hm.FunctionType:
			t, err := c.inferFunctionType(ctx, env, fresh, ft)
			if err != nil {
				return nil, err
			}

			// If the function came from a nullable receiver (e.g. t.foo()
			// where t is nullable), taint the return type as nullable too.
			if sel, ok := c.Fun.(*Select); ok && sel.NullableReceiver {
				if nnType, ok := t.(hm.NonNullType); ok {
					t = nnType.Type
				}
			}

			c.SetInferredType(t)
			return t, nil
		default:
			return nil, fmt.Errorf("FunCall.Infer: expected function, got %s (%T)", fun, fun)
		}
	})
}

// inferFunctionType handles type inference for FunctionType calls
func (c *FunCall) inferFunctionType(ctx context.Context, env hm.Env, fresh hm.Fresher, ft *hm.FunctionType) (hm.Type, error) {
	// Handle positional argument mapping for type inference
	argMapping, err := c.mapArgumentsForInference(ft)
	if err != nil {
		return nil, err
	}

	// Type check each argument first, collecting substitutions from unifying
	// actual argument types with expected parameter types. This resolves type
	// variables (e.g. 'b' in reduce's initial parameter) before we infer the
	// block arg, which may depend on those type variables.
	var argSubs hm.Subs
	for i, arg := range c.Args {
		k := c.getArgumentKey(arg, argMapping, i)
		subs, err := c.checkArgumentTypeWithSubs(ctx, env, fresh, arg.Value, ft.Arg().(*RecordType), k)
		if err != nil {
			return nil, fmt.Errorf("argument %q: %w", k, err)
		}
		if subs != nil {
			if argSubs == nil {
				argSubs = subs
			} else {
				argSubs = argSubs.Compose(subs)
			}
		}
	}

	// Apply argument substitutions to the function type before inferring the
	// block arg, so that type variables resolved from arguments (like 'b' from
	// reduce's initial arg) are concrete in the block's expected param types.
	if argSubs != nil {
		ft = ft.Apply(argSubs).(*hm.FunctionType)
	}

	// Handle block arg if present (needs special bidirectional inference)
	var blockArgSubs hm.Subs
	if c.BlockArg != nil {
		subs, err := c.inferBlockArg(ctx, env, fresh, ft)
		if err != nil {
			return nil, fmt.Errorf("block argument: %w", err)
		}
		blockArgSubs = subs
	}

	// Check that all required arguments are provided
	err = c.validateRequiredArgumentsInInfer(ft)
	if err != nil {
		return nil, err
	}

	// Check that a block arg is provided if the function requires one
	if ft.Block() != nil && c.BlockArg == nil {
		return nil, NewInferError(fmt.Errorf("function requires a block argument"), c)
	}

	// Apply block arg substitutions to the return type
	retType := ft.Ret(false)
	if blockArgSubs != nil {
		retType = retType.Apply(blockArgSubs).(hm.Type)
	}
	return retType, nil
}

// inferBlockArg performs bidirectional type inference for block arguments.
// Returns the substitutions produced by unifying the block arg's inferred type
// with the expected function type.
func (c *FunCall) inferBlockArg(ctx context.Context, env hm.Env, fresh hm.Fresher, ft *hm.FunctionType) (hm.Subs, error) {
	// Get the expected block type from the function type
	// The block type is now stored directly on the FunctionType
	expectedFnType := ft.Block()

	// If there's no block type, the function doesn't accept a block argument
	if expectedFnType == nil {
		return nil, fmt.Errorf("function does not accept a block argument")
	}

	// Extract expected parameter types from the function type
	expectedArgRecord, ok := expectedFnType.Arg().(*RecordType)
	if !ok {
		return nil, fmt.Errorf("expected record type for block arg parameters, got %T", expectedFnType.Arg())
	}

	// Set expected parameter types on the block arg
	c.BlockArg.ExpectedParamTypes = make([]hm.Type, len(expectedArgRecord.Fields))
	for i := range expectedArgRecord.Fields {
		expectedField := expectedArgRecord.Fields[i]
		expectedType, _ := expectedField.Value.Type()
		c.BlockArg.ExpectedParamTypes[i] = expectedType
	}

	// Set expected return type on the block arg
	c.BlockArg.ExpectedReturnType = expectedFnType.Ret(false)

	// Now infer the block arg with these constraints
	inferredType, err := c.BlockArg.Infer(ctx, env, fresh)
	if err != nil {
		return nil, err
	}

	// Verify the inferred type matches the expected function type
	// and capture the substitutions produced by unification
	subs, err := hm.Assignable(inferredType, expectedFnType)
	if err != nil {
		return nil, NewInferError(
			fmt.Errorf("block argument has type %s but expected %s", inferredType, expectedFnType),
			c.BlockArg,
		)
	}

	return subs, nil
}

// getArgumentKey determines the argument key for positional or named arguments
func (c *FunCall) getArgumentKey(arg Keyed[Node], argMapping map[int]string, index int) string {
	if arg.Positional {
		return argMapping[index]
	}
	return arg.Key
}

// checkArgumentType validates an argument's type against the expected parameter type
func (c *FunCall) checkArgumentType(ctx context.Context, env hm.Env, fresh hm.Fresher, value Node, recordType *RecordType, key string) error {
	_, err := c.checkArgumentTypeWithSubs(ctx, env, fresh, value, recordType, key)
	return err
}

// checkArgumentTypeWithSubs validates an argument's type against the expected
// parameter type and returns any substitutions produced by unification. This is
// used to resolve type variables from argument types before inferring block args.
func (c *FunCall) checkArgumentTypeWithSubs(ctx context.Context, env hm.Env, fresh hm.Fresher, value Node, recordType *RecordType, key string) (hm.Subs, error) {
	scheme, has := recordType.SchemeOf(key)
	if !has {
		return nil, fmt.Errorf("FunCall.Infer: %q not found in %s", key, recordType)
	}

	dt, isMono := scheme.Type()
	if !isMono {
		return nil, fmt.Errorf("FunCall.Infer: %q is not monomorphic", key)
	}

	// Infer the argument type
	it, err := value.Infer(ctx, env, fresh)
	if err != nil {
		return nil, fmt.Errorf("FunCall.Infer: %w", err)
	}

	subs, err := hm.Assignable(it, dt)
	if err != nil {
		return nil, NewInferError(err, value)
	}
	return subs, nil
}

var _ hm.Apply = (*FunCall)(nil)

func (c *FunCall) Fn() hm.Expression { return c.Fun }

func (c *FunCall) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, c, func() (Value, error) {
		funVal, err := EvalNode(ctx, env, c.Fun)
		if err != nil {
			// Don't wrap errors - let the specific node error bubble up
			return nil, err
		}

		if funVal == (NullValue{}) {
			// If the function came from a nullable receiver, short-circuit to null
			if sel, ok := c.Fun.(*Select); ok && sel.NullableReceiver {
				return NullValue{}, nil
			}
			return nil, NewSourceError(fmt.Errorf("cannot call null"), c.Fun.GetSourceLocation(), "")
		}

		// Evaluate arguments and handle positional/named argument mapping
		argValues, err := c.evaluateArguments(ctx, env, funVal)
		if err != nil {
			return nil, err
		}

		// Evaluate block arg if present and add to context
		if c.BlockArg != nil {
			blockVal, err := c.BlockArg.Eval(ctx, env)
			if err != nil {
				return nil, err
			}
			// Store block in context for builtin functions to access
			ctx = context.WithValue(ctx, blockArgContextKey, blockVal)
		}

		// Dispatch to appropriate function call handler
		return c.callFunction(ctx, env, funVal, argValues)
	})
}

// callFunction dispatches function calls to appropriate handlers
func (c *FunCall) callFunction(ctx context.Context, env EvalEnv, funVal Value, argValues map[string]Value) (Value, error) {
	callable, ok := funVal.(Callable)
	if !ok {
		return nil, fmt.Errorf("FunCall.Eval: %T is not callable", funVal)
	}
	return callable.Call(ctx, env, argValues)
}

// evaluateArguments handles both positional and named arguments
func (c *FunCall) evaluateArguments(ctx context.Context, env EvalEnv, funVal Value) (map[string]Value, error) {
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

	// Don't add block arg to argValues - it will be handled specially
	// via context and the Args.Block field

	return argValues, nil
}

// validateArgumentOrder ensures positional args come before named args
func (c *FunCall) validateArgumentOrder() error {
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
func (c *FunCall) handlePositionalArgument(arg Keyed[Node], val Value, argValues map[string]Value,
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
func (c *FunCall) handleNamedArgument(arg Keyed[Node], val Value, argValues map[string]Value,
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
func (c *FunCall) getParameterNames(funVal Value) []string {
	if callable, ok := funVal.(Callable); ok {
		return callable.ParameterNames()
	}
	return nil
}

// mapArgumentsForInference maps positional arguments to parameter names during type inference
func (c *FunCall) mapArgumentsForInference(ft *hm.FunctionType) (map[int]string, error) {
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
func (c *FunCall) validateRequiredArgumentsInInfer(ft *hm.FunctionType) error {
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
			return NewInferError(fmt.Errorf("missing required argument: %q", paramName), c)
		}
	}

	return nil
}

func (c *FunCall) Walk(fn func(Node) bool) {
	if !fn(c) {
		return
	}
	c.Fun.Walk(fn)
	for _, arg := range c.Args {
		arg.Value.Walk(fn)
	}
	// Walk block arg if present
	if c.BlockArg != nil {
		c.BlockArg.Walk(fn)
	}
}

// Symbol represents a symbol/variable reference
type Symbol struct {
	InferredTypeHolder
	Name     string
	AutoCall bool
	Loc      *SourceLocation
}

var _ Node = (*Symbol)(nil)
var _ Evaluator = (*Symbol)(nil)

func (s *Symbol) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(s, func() (hm.Type, error) {
		// Check for import conflicts before resolving
		if dangEnv, ok := env.(Env); ok {
			if conflicts := dangEnv.CheckTypeConflict(s.Name); len(conflicts) > 0 {
				return nil, fmt.Errorf("ambiguous reference to %q: provided by imports %v", s.Name, conflicts)
			}
		}

		scheme, found := env.SchemeOf(s.Name)
		if !found {
			return nil, fmt.Errorf("%q not found", s.Name)
		}
		t, _ := scheme.Type()
		if s.AutoCall {
			t, _ = autoCallFnType(t)
		}
		s.SetInferredType(t)
		return t, nil
	})
}

func (s *Symbol) Body() hm.Expression { return s }

func (s *Symbol) GetSourceLocation() *SourceLocation { return s.Loc }

func (s *Symbol) DeclaredSymbols() []string {
	return nil // Symbols don't declare anything
}

func (s *Symbol) ReferencedSymbols() []string {
	return []string{s.Name} // Symbols reference themselves
}

func (s *Symbol) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, s, func() (Value, error) {
		// Check for import conflicts before resolving
		if modVal, ok := env.(*ModuleValue); ok {
			if conflicts := modVal.Mod.CheckValueConflict(s.Name); len(conflicts) > 0 {
				return nil, fmt.Errorf("ambiguous reference to %q: provided by imports %v", s.Name, conflicts)
			}
		}

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

func (s *Symbol) Walk(fn func(Node) bool) {
	fn(s)
}

// Select represents field selection or method call
type Select struct {
	InferredTypeHolder
	Receiver         Node
	Field            *Symbol
	AutoCall         bool
	NullableReceiver bool // set during Infer when receiver is nullable
	Loc              *SourceLocation
}

var _ Node = (*Select)(nil)
var _ Evaluator = (*Select)(nil)

func (d *Select) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(d, func() (hm.Type, error) {
		// Handle nil receiver (symbol calls) - look up type in environment
		if d.Receiver == nil {
			scheme, found := env.SchemeOf(d.Field.Name)
			if !found {
				return nil, fmt.Errorf("%q not found in env", d.Field.Name)
			}
			t, _ := scheme.Type()
			d.SetInferredType(t)
			return t, nil
		}

		// Handle normal receiver
		lt, err := d.Receiver.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// Check if receiver is a list type - handle methods on lists specially
		if nn, ok := lt.(hm.NonNullType); ok {
			if listType, ok := nn.Type.(ListType); ok {
				// Special handling for list methods
				elemType := listType.Type

				// Look up method definition
				def, found := LookupMethod(ListTypeModule, d.Field.Name)
				if !found {
					tv := fresh.Fresh()
					d.SetInferredType(tv)
					return tv, fmt.Errorf("list does not have method %q", d.Field.Name)
				}

				// Build method type with element type substituted for type variable
				methodType := instantiateListMethod(def, elemType)

				if d.AutoCall {
					methodType, _ = autoCallFnType(methodType)
				}

				d.SetInferredType(methodType)
				return methodType, nil
			}
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
			d.NullableReceiver = true
		} else {
			return nil, fmt.Errorf("Select.Infer: expected NonNullType or Env, got %T", lt)
		}

		scheme, found := rec.SchemeOf(d.Field.Name)
		if !found {
			// Return a type variable to allow downstream inference to continue
			tv := fresh.Fresh()
			d.SetInferredType(tv)
			return tv, fmt.Errorf("field %q not found in %s", d.Field.Name, rec)
		}
		t, mono := scheme.Type()
		if !mono {
			return nil, fmt.Errorf("Select.Infer: type of field %q is not monomorphic", d.Field.Name)
		}
		if d.AutoCall {
			t, _ = autoCallFnType(t)
		}

		// If receiver was nullable, make result nullable too
		if isNullable {
			// Remove any existing NonNullType wrapper from the field type
			if nnType, ok := t.(hm.NonNullType); ok {
				d.SetInferredType(nnType.Type)
				return nnType.Type, nil
			}
			// Field type is already nullable, return as-is
			d.SetInferredType(t)
			return t, nil
		}

		d.SetInferredType(t)
		return t, nil
	})
}

func (d *Select) DeclaredSymbols() []string {
	return nil // Select expressions don't declare anything
}

func (d *Select) ReferencedSymbols() []string {
	var symbols []string

	// When Receiver is nil, this is a top-level function call like createPerson()
	if d.Receiver == nil {
		symbols = append(symbols, d.Field.Name)
	} else {
		symbols = append(symbols, d.Receiver.ReferencedSymbols()...)
	}

	return symbols
}

func (d *Select) Body() hm.Expression { return d }

func (d *Select) GetSourceLocation() *SourceLocation { return d.Loc }

func (d *Select) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, d, func() (Value, error) {
		var receiverVal Value
		var err error

		// Handle normal receiver evaluation
		if d.Receiver != nil {
			receiverVal, err = EvalNode(ctx, env, d.Receiver)
			if err != nil {
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
				if val, found := rec.Get(d.Field.Name); found {
					// If this is a FunctionValue accessed from a module, bind it to the receiver
					if fnVal, isFunctionValue := val.(FunctionValue); isFunctionValue {
						return BoundMethod{Method: fnVal, Receiver: rec}, nil
					}
					return val, nil
				}
				// this shouldn't happen (should be caught at type checking)
				return nil, fmt.Errorf("module %q does not have a field %q", rec, d.Field.Name)

			case GraphQLValue:
				// Handle GraphQL field selection
				return rec.SelectField(ctx, d.Field.Name)

			case StringValue:
				// Handle methods on string values by looking them up in the evaluation environment
				// The builtin is registered with a special name
				methodKey := fmt.Sprintf("_string_%s_builtin", d.Field.Name)
				if method, found := env.Get(methodKey); found {
					if builtinFn, ok := method.(BuiltinFunction); ok {
						// Create a bound method that will pass the string as self
						return BoundBuiltinMethod{Method: builtinFn, Receiver: rec}, nil
					}
				}
				return nil, fmt.Errorf("string value does not have method %q", d.Field.Name)

			case FloatValue:
				// Handle methods on float values by looking them up in the evaluation environment
				// The builtin is registered with a special name
				methodKey := fmt.Sprintf("_float_%s_builtin", d.Field.Name)
				if method, found := env.Get(methodKey); found {
					if builtinFn, ok := method.(BuiltinFunction); ok {
						// Create a bound method that will pass the float as self
						return BoundBuiltinMethod{Method: builtinFn, Receiver: rec}, nil
					}
				}
				return nil, fmt.Errorf("float value does not have method %q", d.Field.Name)

			case ListValue:
				// Handle methods on list values by looking them up in the evaluation environment
				// The builtin is registered with a special name
				methodKey := fmt.Sprintf("_list_%s_builtin", d.Field.Name)
				if method, found := env.Get(methodKey); found {
					if builtinFn, ok := method.(BuiltinFunction); ok {
						// Create a bound method that will pass the list as self
						return BoundBuiltinMethod{Method: builtinFn, Receiver: rec}, nil
					}
				}
				return nil, fmt.Errorf("list value does not have method %q", d.Field.Name)

			default:
				return nil, fmt.Errorf("Select.Eval: cannot select field %q from %T (value: %q). Expected a record or module value, but got %T", d.Field.Name, receiverVal, receiverVal.String(), receiverVal)
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

func (d *Select) Walk(fn func(Node) bool) {
	if !fn(d) {
		return
	}
	if d.Receiver != nil {
		d.Receiver.Walk(fn)
	}
}

// Index represents list indexing operations like foo[0]
type Index struct {
	InferredTypeHolder
	Receiver Node
	Index    Node
	AutoCall bool
	Loc      *SourceLocation
}

var _ Node = (*Index)(nil)
var _ Evaluator = (*Index)(nil)

func (i *Index) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(i, func() (hm.Type, error) {
		// Infer the type of the receiver (should be a list)
		receiverType, err := i.Receiver.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("Index.Infer receiver: %w", err)
		}

		// Infer the type of the index (should be Int!)
		indexType, err := i.Index.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("Index.Infer index: %w", err)
		}

		// Check that index is Int!
		intType, err := NonNullTypeNode{&NamedTypeNode{nil, "Int", i.Index.GetSourceLocation()}}.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}
		if _, err := hm.Assignable(indexType, intType); err != nil {
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
		var resultType hm.Type
		if isNullable {
			// Remove NonNull wrapper if present, since nullable list means nullable result
			if nonNullElem, ok := elementType.(hm.NonNullType); ok {
				resultType = nonNullElem.Type
			} else {
				resultType = elementType
			}
		} else {
			// Even for non-null lists, indexing can fail, so return nullable
			if nonNullElem, ok := elementType.(hm.NonNullType); ok {
				resultType = nonNullElem.Type
			} else {
				resultType = elementType
			}
		}
		i.SetInferredType(resultType)
		return resultType, nil
	})
}

func (i *Index) DeclaredSymbols() []string {
	return nil // Index expressions don't declare anything
}

func (i *Index) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, i.Receiver.ReferencedSymbols()...)
	symbols = append(symbols, i.Index.ReferencedSymbols()...)
	return symbols
}

func (i *Index) Body() hm.Expression { return i }

func (i *Index) GetSourceLocation() *SourceLocation { return i.Loc }

func (i *Index) Eval(ctx context.Context, env EvalEnv) (Value, error) {
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

func (i *Index) Walk(fn func(Node) bool) {
	if !fn(i) {
		return
	}
	i.Receiver.Walk(fn)
	i.Index.Walk(fn)
}

// FieldSelection represents a field name in an object selection
type FieldSelection struct {
	InferredTypeHolder
	Name      string
	Args      Record           // For arguments like repositories(first: 100)
	Selection *ObjectSelection // For nested selections like profile.{bio, avatar}
	Loc       *SourceLocation
}

func (f *FieldSelection) GetSourceLocation() *SourceLocation { return f.Loc }

// InlineFragment represents a type-conditional selection like ... on User { name, email }
type InlineFragment struct {
	TypeName *Symbol
	Fields   []*FieldSelection
	Loc      *SourceLocation

	Inferred *Module // The concrete type module for this fragment
}

func (f *InlineFragment) GetSourceLocation() *SourceLocation { return f.Loc }

// ObjectSelection represents multi-field selection like obj.{field1, field2}
// or union selection like obj.{... on User { name }, ... on Post { title }}
type ObjectSelection struct {
	InferredTypeHolder
	Receiver        Node
	Fields          []*FieldSelection
	InlineFragments []*InlineFragment // For union/interface inline fragments
	Loc             *SourceLocation

	Inferred         *Module
	IsList           bool // TODO respect
	ElementIsNonNull bool // Whether list elements are non-null (only used when IsList is true)
}

var _ Node = (*ObjectSelection)(nil)
var _ Evaluator = (*ObjectSelection)(nil)

func (o *ObjectSelection) DeclaredSymbols() []string {
	return nil // Object selections don't declare anything
}

func (o *ObjectSelection) ReferencedSymbols() []string {
	symbols := o.Receiver.ReferencedSymbols()
	for _, frag := range o.InlineFragments {
		symbols = append(symbols, frag.TypeName.Name)
	}
	return symbols
}

func (o *ObjectSelection) Body() hm.Expression { return o }

func (o *ObjectSelection) GetSourceLocation() *SourceLocation { return o.Loc }

func (o *ObjectSelection) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(o, func() (hm.Type, error) {
		// Infer the type of the receiver
		receiverType, err := o.Receiver.Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("ObjectSelection.Infer: %w", err)
		}

		// Handle inline fragments (union/interface selection)
		if len(o.InlineFragments) > 0 {
			return o.inferInlineFragments(ctx, receiverType, env, fresh)
		}

		// Handle regular object types
		t, err := o.inferSelectionType(ctx, receiverType, env, fresh)
		if err != nil {
			return nil, err
		}

		// If this is a list selection, wrap the result in a list type
		var resultType hm.Type
		if o.IsList {
			// Wrap the element module in NonNullType if elements are non-null
			var elementType hm.Type = t
			if o.ElementIsNonNull {
				elementType = hm.NonNullType{Type: t}
			}

			listType := ListType{Type: elementType}

			// If receiver was nullable, make result nullable too
			if _, ok := receiverType.(hm.NonNullType); ok {
				// Receiver was non-null, result should be non-null list
				resultType = hm.NonNullType{Type: listType}
			} else {
				// Receiver was nullable, result should be nullable list
				resultType = listType
			}
		} else {
			// If receiver was nullable, make result nullable too
			if _, ok := receiverType.(hm.NonNullType); ok {
				// Receiver was non-null, result should be non-null
				resultType = hm.NonNullType{Type: t}
			} else {
				// Receiver was nullable, result should be nullable
				resultType = t
			}
		}
		o.SetInferredType(resultType)
		return resultType, nil
	})
}

// inferInlineFragments validates inline fragment selections on a union or interface type.
// The result type preserves the union/interface type and list wrapping from the receiver.
func (o *ObjectSelection) inferInlineFragments(ctx context.Context, receiverType hm.Type, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	modEnv, ok := env.(Env)
	if !ok {
		return nil, fmt.Errorf("inline fragments require a module environment")
	}

	// Unwrap list and non-null to find the element type
	unwrapped := receiverType
	isList := false
	elementIsNonNull := false
	receiverIsNonNull := false
	if nn, ok := unwrapped.(hm.NonNullType); ok {
		receiverIsNonNull = true
		unwrapped = nn.Type
	}
	if lt, ok := unwrapped.(ListType); ok {
		isList = true
		unwrapped = lt.Type
		if nn, ok := unwrapped.(hm.NonNullType); ok {
			elementIsNonNull = true
			unwrapped = nn.Type
		}
	} else if lt, ok := unwrapped.(GraphQLListType); ok {
		isList = true
		unwrapped = lt.Type
		if nn, ok := unwrapped.(hm.NonNullType); ok {
			elementIsNonNull = true
			unwrapped = nn.Type
		}
	}

	// The element type must be a union or interface
	unionOrIface, ok := unwrapped.(*Module)
	if !ok {
		return nil, NewInferError(fmt.Errorf("inline fragments require a union or interface type, got %s", unwrapped), o.Receiver)
	}
	if unionOrIface.Kind != UnionKind && unionOrIface.Kind != InterfaceKind {
		return nil, NewInferError(fmt.Errorf("inline fragments require a union or interface type, got %s type %s", unionOrIface.Kind, unionOrIface.Name()), o.Receiver)
	}

	// Create a narrowed union whose members only have the selected fields.
	// This ensures downstream code (e.g. case type patterns) can only access
	// fields that were actually selected in the inline fragment.
	narrowedUnion := NewModule(unionOrIface.Name(), UnionKind)

	// Validate each fragment
	for _, frag := range o.InlineFragments {
		memberType, found := modEnv.NamedType(frag.TypeName.Name)
		if !found {
			return nil, NewInferError(fmt.Errorf("unknown type %s in inline fragment", frag.TypeName.Name), frag.TypeName)
		}

		memberMod, ok := memberType.(*Module)
		if !ok {
			return nil, NewInferError(fmt.Errorf("type %s in inline fragment is not an object type", frag.TypeName.Name), frag.TypeName)
		}

		// Check membership
		if unionOrIface.Kind == UnionKind {
			if !unionOrIface.HasMember(memberMod) {
				return nil, NewInferError(fmt.Errorf("type %s is not a member of union %s", frag.TypeName.Name, unionOrIface.Name()), frag.TypeName)
			}
		}

		// Create a narrowed module with only the selected fields.
		// Link back to the canonical type for runtime matching.
		narrowedMember := NewModule(frag.TypeName.Name, ObjectKind)
		narrowedMember.Canonical = memberMod

		// Validate each field in the fragment exists on the concrete type
		// and add it to the narrowed module (including nested selections)
		for _, field := range frag.Fields {
			fieldType, err := o.inferFieldType(ctx, field, memberMod, env, fresh)
			if err != nil {
				return nil, NewInferError(fmt.Errorf("field %s not found on type %s", field.Name, frag.TypeName.Name), field)
			}
			narrowedMember.Add(field.Name, hm.NewScheme(nil, fieldType))
		}

		narrowedUnion.AddMember(narrowedMember)
		frag.Inferred = narrowedMember
	}

	o.IsList = isList
	o.ElementIsNonNull = elementIsNonNull

	// The result type is the narrowed union, preserving list/non-null wrapping
	var resultType hm.Type = narrowedUnion
	if isList {
		var elemType hm.Type = narrowedUnion
		if elementIsNonNull {
			elemType = hm.NonNullType{Type: elemType}
		}
		resultType = ListType{Type: elemType}
	}
	if receiverIsNonNull {
		resultType = hm.NonNullType{Type: resultType}
	}

	o.SetInferredType(resultType)
	return resultType, nil
}

func (o *ObjectSelection) inferSelectionType(ctx context.Context, receiverType hm.Type, env hm.Env, fresh hm.Fresher) (*Module, error) {
	// Check if receiver is nullable or non-null
	var rec Env

	// Handle list types - apply selection to each element
	var innerType hm.Type
	var elementIsNonNull bool
	if nonNullType, ok := receiverType.(hm.NonNullType); ok {
		receiverType = nonNullType.Type
	}
	if listType, ok := receiverType.(ListType); ok {
		innerType = listType.Type
		// Check if element type is non-null
		_, elementIsNonNull = innerType.(hm.NonNullType)
	} else if gqlListType, ok := receiverType.(GraphQLListType); ok {
		// GraphQL lists can be converted to regular lists via object selection
		innerType = gqlListType.Type
		// Check if element type is non-null
		_, elementIsNonNull = innerType.(hm.NonNullType)
	}

	if innerType != nil {
		elementType, err := o.inferSelectionType(ctx, innerType, env, fresh)
		if err != nil {
			return nil, err
		}

		// Store both the module and whether elements are non-null
		// We'll use elementIsNonNull in the caller to wrap appropriately
		o.Inferred = elementType
		o.IsList = true
		o.ElementIsNonNull = elementIsNonNull
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

	mod := NewModule("", ObjectKind)
	for _, field := range o.Fields {
		fieldType, err := o.inferFieldType(ctx, field, rec, env, fresh)
		if err != nil {
			return nil, err
		}
		mod.Add(field.Name, hm.NewScheme(nil, fieldType))
	}
	o.Inferred = mod
	return mod, nil
}

func (o *ObjectSelection) inferFieldType(ctx context.Context, field *FieldSelection, rec Env, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var fieldType hm.Type
	var err error

	if len(field.Args) > 0 {
		// Field has arguments - create a Select then FunCall to infer the type
		// But use a synthetic receiver to get the field from the correct environment
		selectNode := &Select{
			Receiver: nil, // Will be handled by using rec environment directly
			Field:    &Symbol{Name: field.Name, Loc: field.Loc},
			AutoCall: false,
			Loc:      field.Loc,
		}

		funCall := &FunCall{
			Fun:  selectNode,
			Args: field.Args,
			Loc:  field.Loc,
		}

		// Create a synthetic environment that combines rec and env
		// Use rec for symbol lookup, env for argument evaluation
		synthEnv := &CompositeModule{
			primary: rec,
			lexical: env.(Env),
		}
		fieldType, err = funCall.Infer(ctx, synthEnv, fresh)
		if err != nil {
			return nil, err
		}
	} else {
		// No arguments - use symbol directly as before
		fieldType, err = (&Symbol{
			Name:     field.Name,
			AutoCall: true,
			Loc:      field.Loc,
		}).Infer(ctx, rec, fresh)
		if err != nil {
			return nil, err
		}
	}

	ret, _ := autoCallFnType(fieldType)

	// Handle nested selections
	if field.Selection != nil {
		// Set the receiver for the nested selection to match the field we're selecting from
		field.Selection.Receiver = nil // Will be inferred from the receiver type

		if len(field.Selection.InlineFragments) > 0 {
			// Nested inline fragments: infer as union/interface type
			t, err := field.Selection.inferInlineFragments(ctx, ret, env, fresh)
			if err != nil {
				return nil, err
			}
			return t, nil
		}

		t, err := field.Selection.inferSelectionType(ctx, ret, env, fresh)
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

		// Handle inline fragments on GraphQL values
		if len(o.InlineFragments) > 0 {
			if gqlVal, ok := receiverVal.(GraphQLValue); ok {
				return o.evalGraphQLInlineFragments(gqlVal, ctx, env)
			}
			// For Dang-native values, inline fragments work like regular selection
			// but we need to handle lists
			if listVal, ok := receiverVal.(ListValue); ok {
				var results []Value
				for _, elem := range listVal.Elements {
					result, err := o.evalInlineFragmentOnValue(elem, ctx, env)
					if err != nil {
						return nil, err
					}
					results = append(results, result)
				}
				return ListValue{Elements: results, ElemType: o.GetInferredType()}, nil
			}
			return o.evalInlineFragmentOnValue(receiverVal, ctx, env)
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

// evalInlineFragmentOnValue handles inline fragment selection on a Dang-native value.
// The value passes through with its original type identity — fragments just validate at compile time.
func (o *ObjectSelection) evalInlineFragmentOnValue(val Value, ctx context.Context, env EvalEnv) (Value, error) {
	modVal, ok := val.(*ModuleValue)
	if !ok {
		return nil, fmt.Errorf("inline fragment selection requires an object value, got %T", val)
	}

	// Find the matching fragment based on the concrete type name
	mod, ok := modVal.Mod.(*Module)
	if !ok {
		return nil, fmt.Errorf("inline fragment selection: expected Module, got %T", modVal.Mod)
	}

	for _, frag := range o.InlineFragments {
		if frag.TypeName.Name == mod.Name() {
			// Build a result with the selected fields, preserving the concrete type
			resultModuleValue := NewModuleValue(frag.Inferred)
			for _, field := range frag.Fields {
				fieldVal, exists := modVal.Get(field.Name)
				if !exists {
					return nil, fmt.Errorf("field %s not found on %s", field.Name, mod.Name())
				}
				resultModuleValue.Set(field.Name, fieldVal)
			}
			return resultModuleValue, nil
		}
	}

	return nil, fmt.Errorf("no inline fragment matched type %s", mod.Name())
}

// evalGraphQLInlineFragments builds a GraphQL query with __typename and inline fragments,
// executes it, and returns properly-typed results.
func (o *ObjectSelection) evalGraphQLInlineFragments(gqlVal GraphQLValue, ctx context.Context, env EvalEnv) (Value, error) {
	// Build the query with __typename and inline fragments
	query := gqlVal.QueryChain
	if query == nil {
		return nil, fmt.Errorf("GraphQL inline fragments: no query chain")
	}

	// Build inline fragment query string
	// We need: { __typename ... on User { name email } ... on Post { title author { name } } }
	var fragParts []string
	fragParts = append(fragParts, "__typename")
	for _, frag := range o.InlineFragments {
		var fieldParts []string
		for _, field := range frag.Fields {
			if field.Selection != nil {
				fieldParts = append(fieldParts, field.Name+" "+buildSelectionString(field.Selection))
			} else {
				fieldParts = append(fieldParts, field.Name)
			}
		}
		fragParts = append(fragParts, fmt.Sprintf("... on %s { %s }", frag.TypeName.Name, strings.Join(fieldParts, " ")))
	}

	// Use SelectMultiple to inject the raw fragment selection
	query = query.SelectMultiple(fragParts...)

	// Execute the query
	var result any
	err := query.Client(gqlVal.Client).Bind(&result).Execute(ctx)
	if err != nil {
		return nil, fmt.Errorf("GraphQL inline fragments: executing query: %w", err)
	}

	// Convert results to properly-typed ModuleValues
	return o.convertInlineFragmentResult(result, gqlVal.Schema, gqlVal.TypeEnv)
}

// convertInlineFragmentResult converts a GraphQL response with __typename into typed ModuleValues.
func (o *ObjectSelection) convertInlineFragmentResult(result any, schema *introspection.Schema, typeEnv Env) (Value, error) {
	// Handle list results
	if resultSlice, ok := result.([]any); ok {
		var elements []Value
		for _, item := range resultSlice {
			elem, err := o.convertInlineFragmentResult(item, schema, typeEnv)
			if err != nil {
				return nil, err
			}
			elements = append(elements, elem)
		}
		return ListValue{Elements: elements, ElemType: o.GetInferredType()}, nil
	}

	// Handle single object result
	resultMap, ok := result.(map[string]any)
	if !ok {
		if result == nil {
			return NullValue{}, nil
		}
		return nil, fmt.Errorf("expected map or slice from GraphQL inline fragment query, got %T", result)
	}

	typeName, ok := resultMap["__typename"].(string)
	if !ok {
		return nil, fmt.Errorf("GraphQL inline fragment response missing __typename")
	}

	// Find the matching fragment
	for _, frag := range o.InlineFragments {
		if frag.TypeName.Name == typeName {
			// Look up the concrete type module
			concreteType, found := typeEnv.NamedType(typeName)
			if !found {
				return nil, fmt.Errorf("type %s not found in type environment", typeName)
			}

			resultModuleValue := NewModuleValue(concreteType)
			for _, field := range frag.Fields {
				if fieldValue, exists := resultMap[field.Name]; exists {
					// Handle nested selections recursively
					if field.Selection != nil {
						nestedVal, err := o.convertNestedSelectionResult(fieldValue, field.Selection, schema, typeName, field.Name, typeEnv)
						if err != nil {
							return nil, fmt.Errorf("converting nested field %s: %w", field.Name, err)
						}
						resultModuleValue.Set(field.Name, nestedVal)
						continue
					}

					// Look up field type info for enum conversion
					concreteSchemaType := schema.Types.Get(typeName)
					if concreteSchemaType != nil {
						for _, schemaField := range concreteSchemaType.Fields {
							if schemaField.Name == field.Name {
								fieldType := schema.Types.Get(o.unwrapType(schemaField.TypeRef).Name)
								if fieldType != nil && fieldType.Kind == introspection.TypeKindEnum {
									if strVal, ok := fieldValue.(string); ok {
										enumType, found := typeEnv.NamedType(fieldType.Name)
										if found {
											resultModuleValue.Set(field.Name, EnumValue{Val: strVal, EnumType: enumType})
											continue
										}
									}
								}
								break
							}
						}
					}

					dangVal, err := ToValue(fieldValue)
					if err != nil {
						return nil, fmt.Errorf("converting field %s: %w", field.Name, err)
					}
					resultModuleValue.Set(field.Name, dangVal)
				}
			}

			return resultModuleValue, nil
		}
	}

	return nil, fmt.Errorf("no inline fragment matched __typename %q", typeName)
}

// convertNestedSelectionResult converts a nested JSON value using the
// inferred type from a nested ObjectSelection inside an inline fragment.
func (o *ObjectSelection) convertNestedSelectionResult(value any, sel *ObjectSelection, schema *introspection.Schema, parentTypeName string, fieldName string, typeEnv Env) (Value, error) {
	if value == nil {
		return NullValue{}, nil
	}
	resultMap, ok := value.(map[string]any)
	if !ok {
		return ToValue(value)
	}
	if sel.Inferred == nil {
		return ToValue(value)
	}
	mod := NewModuleValue(sel.Inferred)
	for _, f := range sel.Fields {
		if fv, exists := resultMap[f.Name]; exists {
			if f.Selection != nil {
				nested, err := o.convertNestedSelectionResult(fv, f.Selection, schema, "", f.Name, typeEnv)
				if err != nil {
					return nil, err
				}
				mod.Set(f.Name, nested)
			} else {
				dv, err := ToValue(fv)
				if err != nil {
					return nil, fmt.Errorf("converting field %s: %w", f.Name, err)
				}
				mod.Set(f.Name, dv)
			}
		}
	}
	return mod, nil
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
		var fieldVal Value
		var err error

		if len(field.Args) > 0 {
			// Field has arguments - create a Select then FunCall to evaluate
			selectNode := &Select{
				Receiver: createValueNode(objVal),
				Field:    &Symbol{Name: field.Name, Loc: field.Loc},
				AutoCall: false,
				Loc:      field.Loc,
			}

			funCall := &FunCall{
				Fun:  selectNode,
				Args: field.Args,
				Loc:  field.Loc,
			}
			fieldVal, err = funCall.Eval(ctx, env)
			if err != nil {
				return nil, fmt.Errorf("ObjectSelection.evalModuleSelection: evaluating field %q with args: %w", field.Name, err)
			}
		} else {
			// No arguments - get field value directly
			var exists bool
			fieldVal, exists = objVal.Get(field.Name)
			if !exists {
				return nil, fmt.Errorf("ObjectSelection.evalModuleSelection: field %q not found", field.Name)
			}
		}

		// Handle nested selections
		if field.Selection != nil {
			fieldVal, err = field.Selection.evalSelectionOnValue(fieldVal, ctx, env)
			if err != nil {
				return nil, err
			}
		}

		resultModuleValue.Set(field.Name, fieldVal)
	}

	return resultModuleValue, nil
}

func (o *ObjectSelection) evalGraphQLSelection(gqlVal GraphQLValue, ctx context.Context, env EvalEnv) (Value, error) {
	if o.Inferred == nil {
		return nil, fmt.Errorf("ObjectSelection.evalModuleSelection: inferred type is nil")
	}

	// Build optimized GraphQL query for all selected fields
	query, err := o.buildGraphQLQuery(ctx, env, gqlVal.QueryChain, o.Fields)
	if err != nil {
		return nil, fmt.Errorf("ObjectSelection.evalGraphQLSelection: %w", err)
	}

	// Execute the single optimized query
	var result any
	err = query.Client(gqlVal.Client).Bind(&result).Execute(ctx)
	if err != nil {
		return nil, fmt.Errorf("ObjectSelection.evalGraphQLSelection: executing GraphQL query: %w", err)
	}

	// Convert GraphQL result to ModuleValue
	return o.convertGraphQLResultToModule(result, o.Fields, gqlVal.Schema, gqlVal.Field, gqlVal.TypeEnv)
}

func (o *ObjectSelection) buildGraphQLQuery(ctx context.Context, env EvalEnv, baseQuery *querybuilder.Selection, fields []*FieldSelection) (*querybuilder.Selection, error) {
	// Start with the base query (which contains the context like "serverInfo")
	builder := baseQuery
	if builder == nil {
		builder = querybuilder.Query()
	}

	// Check if we have any nested selections or fields with arguments
	hasNestedSelectionsOrArgs := false
	for _, field := range fields {
		if field.Selection != nil || len(field.Args) > 0 {
			hasNestedSelectionsOrArgs = true
			break
		}
	}

	if !hasNestedSelectionsOrArgs {
		// Simple case: just select all fields using SelectFields (no args, no nested selections)
		fieldNames := make([]string, len(fields))
		for i, field := range fields {
			fieldNames[i] = field.Name
		}
		return builder.SelectFields(fieldNames...), nil
	}

	// Complex case: mix of simple fields, fields with arguments, and nested selections
	// Use SelectMixed to handle all types in a single selection set

	// Collect simple fields (no args, no nested selections)
	var simpleFields []string
	fieldsWithSelections := make(map[string]*querybuilder.QueryBuilder)

	for _, field := range fields {
		if field.Selection == nil && len(field.Args) == 0 {
			// Simple field - no arguments, no nested selection
			simpleFields = append(simpleFields, field.Name)
		} else {
			// Field has either arguments or nested selection (or both)
			fieldBuilder := querybuilder.Query().Select(field.Name)

			// Add arguments if present
			if len(field.Args) > 0 {
				for _, arg := range field.Args {
					val, err := EvalNode(ctx, env, arg.Value)
					if err != nil {
						return nil, fmt.Errorf("evaluating argument %s: %w", arg.Key, err)
					}
					// Convert Dang value to Go value for GraphQL
					goVal, err := dangValueToGo(val)
					if err != nil {
						return nil, fmt.Errorf("converting argument %s: %w", arg.Key, err)
					}
					fieldBuilder = fieldBuilder.Arg(arg.Key, goVal)
				}
			}

			// Handle nested selections
			if field.Selection != nil {
				if len(field.Selection.InlineFragments) > 0 {
					// Nested inline fragments: build __typename + ... on Type { fields }
					nestedResult, err := field.Selection.buildInlineFragmentQuery(querybuilder.Query())
					if err != nil {
						return nil, err
					}
					fieldBuilder = nestedResult
				} else {
					nestedResult, err := field.Selection.buildGraphQLQuery(ctx, env, querybuilder.Query(), field.Selection.Fields)
					if err != nil {
						return nil, err
					}
					fieldBuilder = nestedResult
				}
			}

			fieldsWithSelections[field.Name] = fieldBuilder
		}
	}

	builder = builder.SelectMixed(simpleFields, fieldsWithSelections)

	return builder, nil
}

// buildInlineFragmentQuery builds a query with __typename and inline fragments
// for use as a nested selection inside a parent field.
func (o *ObjectSelection) buildInlineFragmentQuery(baseQuery *querybuilder.Selection) (*querybuilder.Selection, error) {
	var fragParts []string
	fragParts = append(fragParts, "__typename")
	for _, frag := range o.InlineFragments {
		var fieldParts []string
		for _, field := range frag.Fields {
			if field.Selection != nil {
				// Nested selection: build sub-selection string
				fieldParts = append(fieldParts, field.Name+" "+buildSelectionString(field.Selection))
			} else {
				fieldParts = append(fieldParts, field.Name)
			}
		}
		fragParts = append(fragParts, fmt.Sprintf("... on %s { %s }", frag.TypeName.Name, strings.Join(fieldParts, " ")))
	}
	return baseQuery.SelectMultiple(fragParts...), nil
}

// buildSelectionString recursively builds a GraphQL selection set string
// like "{ name login }" from an ObjectSelection.
func buildSelectionString(sel *ObjectSelection) string {
	var parts []string
	for _, field := range sel.Fields {
		if field.Selection != nil {
			parts = append(parts, field.Name+" "+buildSelectionString(field.Selection))
		} else {
			parts = append(parts, field.Name)
		}
	}
	for _, frag := range sel.InlineFragments {
		var fieldParts []string
		for _, field := range frag.Fields {
			if field.Selection != nil {
				fieldParts = append(fieldParts, field.Name+" "+buildSelectionString(field.Selection))
			} else {
				fieldParts = append(fieldParts, field.Name)
			}
		}
		parts = append(parts, fmt.Sprintf("... on %s { %s }", frag.TypeName.Name, strings.Join(fieldParts, " ")))
	}
	return "{ " + strings.Join(parts, " ") + " }"
}

func (o *ObjectSelection) convertGraphQLResultToModule(result any, fields []*FieldSelection, schema *introspection.Schema, parentField *introspection.Field, typeEnv Env) (Value, error) {
	// Check if the result is a list/slice
	if resultSlice, ok := result.([]any); ok {
		var elements []Value
		for _, item := range resultSlice {
			itemValue, err := o.convertGraphQLResultToModule(item, fields, schema, parentField, typeEnv)
			if err != nil {
				return nil, err
			}
			elements = append(elements, itemValue)
		}
		return ListValue{Elements: elements}, nil
	}

	resultModuleValue := NewModuleValue(o.Inferred)

	// Convert GraphQL result to Dang values
	if resultMap, ok := result.(map[string]any); ok {
		for _, field := range fields {
			if fieldValue, exists := resultMap[field.Name]; exists {
				// Check if fieldValue is a list that needs selection applied to each element
				// Get the field information for the nested selection
				nestedField := o.getFieldFromParent(field.Name, parentField, schema)

				// Handle nested selections
				if field.Selection != nil {
					if len(field.Selection.InlineFragments) > 0 {
						// Nested inline fragments: dispatch to inline fragment result converter
						nestedResult, err := field.Selection.convertInlineFragmentResult(fieldValue, schema, typeEnv)
						if err != nil {
							return nil, fmt.Errorf("ObjectSelection.convertGraphQLResultToModule: nested field %q inline fragments: %w", field.Name, err)
						}
						resultModuleValue.Set(field.Name, nestedResult)
					} else if fieldSlice, isSlice := fieldValue.([]any); isSlice && field.Selection.IsList {
						// Sub-selecting arrays
						var elements []Value
						for _, item := range fieldSlice {
							itemResult, err := field.Selection.convertGraphQLResultToModule(item, field.Selection.Fields, schema, nestedField, typeEnv)
							if err != nil {
								return nil, fmt.Errorf("ObjectSelection.convertGraphQLResultToModule: nested field %q item: %w", field.Name, err)
							}
							elements = append(elements, itemResult)
						}
						resultModuleValue.Set(field.Name, ListValue{Elements: elements})
					} else {
						// Sub-selecting objects
						nestedResult, err := field.Selection.convertGraphQLResultToModule(fieldValue, field.Selection.Fields, schema, nestedField, typeEnv)
						if err != nil {
							return nil, fmt.Errorf("ObjectSelection.convertGraphQLResultToModule: nested field %q: %w", field.Name, err)
						}
						resultModuleValue.Set(field.Name, nestedResult)
					}
				} else if fieldType := schema.Types.Get(o.unwrapType(nestedField.TypeRef).Name); fieldType != nil && fieldType.Kind == introspection.TypeKindEnum {
					// Convert enums
					strVal, ok := fieldValue.(string)
					if !ok {
						return nil, fmt.Errorf("ObjectSelection.convertGraphQLResultToModule: converting enum field %q: expected string value, got %T", field.Name, fieldValue)
					}
					// Get the enum type from the type environment
					enumType, found := typeEnv.NamedType(fieldType.Name)
					if !found {
						return nil, fmt.Errorf("type not defined for enum: %q", fieldType.Name)
					}
					resultModuleValue.Set(field.Name, EnumValue{
						Val:      strVal,
						EnumType: enumType,
					})
				} else {
					// Convert GraphQL value to Dang value
					dangVal, err := ToValue(fieldValue)
					if err != nil {
						return nil, fmt.Errorf("ObjectSelection.convertGraphQLResultToModule: converting field %q: %w", field.Name, err)
					}
					resultModuleValue.Set(field.Name, dangVal)
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

func (o *ObjectSelection) Walk(fn func(Node) bool) {
	if !fn(o) {
		return
	}
	o.Receiver.Walk(fn)
}

// Conditional represents an if-then-else expression
type Conditional struct {
	InferredTypeHolder
	Condition Node
	Then      *Block
	Else      any
	Loc       *SourceLocation
}

var _ Node = (*Conditional)(nil)
var _ Evaluator = (*Conditional)(nil)

func (c *Conditional) DeclaredSymbols() []string {
	return nil // Conditionals don't declare anything
}

func (c *Conditional) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, c.Condition.ReferencedSymbols()...)
	symbols = append(symbols, c.Then.ReferencedSymbols()...)
	if c.Else != nil {
		elseBlock := c.Else.(*Block)
		symbols = append(symbols, elseBlock.ReferencedSymbols()...)
	}
	return symbols
}

func (c *Conditional) Body() hm.Expression { return c }

func (c *Conditional) GetSourceLocation() *SourceLocation { return c.Loc }

func (c *Conditional) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(c, func() (hm.Type, error) {
		condType, err := c.Condition.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		if _, err := hm.Assignable(condType, hm.NonNullType{Type: BooleanType}); err != nil {
			return nil, NewInferError(fmt.Errorf("condition must be Boolean, got %s", condType), c.Condition)
		}

		// Analyze null assertions in the condition for flow-sensitive type checking
		assertions := AnalyzeNullAssertions(c.Condition)
		thenRefinements, elseRefinements, err := CreateTypeRefinements(assertions, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("creating type refinements: %w", err)
		}

		// Apply type refinements to the then branch
		thenEnv := ApplyTypeRefinements(env, thenRefinements)
		thenType, err := c.Then.Infer(ctx, thenEnv, fresh)
		if err != nil {
			return nil, err
		}

		if c.Else != nil {
			elseBlock := c.Else.(*Block)

			// Apply type refinements to the else branch
			elseEnv := ApplyTypeRefinements(env, elseRefinements)
			elseType, err := elseBlock.Infer(ctx, elseEnv, fresh)
			if err != nil {
				return nil, err
			}

			thenSubs, err := hm.Assignable(elseType, thenType)
			if err != nil {
				// Point to the specific else block for better error targeting
				var errorNode Node = elseBlock
				if len(elseBlock.Forms) > 0 {
					errorNode = elseBlock.Forms[len(elseBlock.Forms)-1] // Use the last form (the return value)
				}
				return nil, NewInferError(err, errorNode)
			}

			// Propagate substitutions backwards to the 'then'
			thenType = thenType.Apply(thenSubs).(hm.Type)
			c.Then.SetInferredType(thenType)

			// If either branch is nullable after substitution, the result
			// must be nullable. NullableTypeVariable (from null literals)
			// strips NonNull during binding, so a null branch naturally
			// resolves to a nullable type here.
			resolvedElse := elseType.Apply(thenSubs).(hm.Type)
			_, thenNonNull := thenType.(hm.NonNullType)
			_, elseNonNull := resolvedElse.(hm.NonNullType)
			if thenNonNull && !elseNonNull {
				thenType = thenType.(hm.NonNullType).Type
			}
		}

		return thenType, nil
	})
}

func (c *Conditional) Eval(ctx context.Context, env EvalEnv) (Value, error) {
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
			elseBlock := c.Else.(*Block)
			return EvalNode(ctx, env, elseBlock)
		} else {
			return NullValue{}, nil
		}
	})
}

func (c *Conditional) Walk(fn func(Node) bool) {
	if !fn(c) {
		return
	}
	c.Condition.Walk(fn)
	c.Then.Walk(fn)
	if c.Else != nil {
		if elseBlock, ok := c.Else.(*Block); ok {
			elseBlock.Walk(fn)
		}
	}
}

// ForLoop represents a for..in loop expression or condition-based loop
type ForLoop struct {
	InferredTypeHolder
	Variable      string   // Loop variable name (for single-variable iteration)
	KeyVariable   string   // Key/Index variable name (for two-variable iteration)
	ValueVariable string   // Value variable name (for two-variable iteration)
	Type          TypeNode // Optional type annotation
	Iterable      Node     // Expression that produces iterable (nil for condition-only loops)
	Condition     Node     // Condition for condition-only loops (nil for iterator loops)
	LoopBody      *Block   // Loop body
	Loc           *SourceLocation
}

var _ Node = (*ForLoop)(nil)
var _ Evaluator = (*ForLoop)(nil)

func (f *ForLoop) DeclaredSymbols() []string {
	return nil // For loops don't declare anything in global scope
}

func (f *ForLoop) ReferencedSymbols() []string {
	var symbols []string
	if f.Iterable != nil {
		symbols = append(symbols, f.Iterable.ReferencedSymbols()...)
	}
	if f.Condition != nil {
		symbols = append(symbols, f.Condition.ReferencedSymbols()...)
	}
	symbols = append(symbols, f.LoopBody.ReferencedSymbols()...)
	return symbols
}

func (f *ForLoop) Body() hm.Expression { return f }

func (f *ForLoop) GetSourceLocation() *SourceLocation { return f.Loc }

func (f *ForLoop) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(f, func() (hm.Type, error) {
		// Check if this is a condition-only loop (while-style)
		if f.Condition != nil {
			// Infer the condition type
			condType, err := f.Condition.Infer(ctx, env, fresh)
			if err != nil {
				return nil, err
			}

			// Unify with boolean
			if _, err := hm.Assignable(condType, hm.NonNullType{Type: BooleanType}); err != nil {
				return nil, NewInferError(fmt.Errorf("condition must be boolean, got %s", condType), f.Condition)
			}

			// Infer body type
			bodyType, err := f.LoopBody.Infer(ctx, env, fresh)
			if err != nil {
				return nil, err
			}

			// Condition loops return the last value from the body, or null if never executed
			if nonNull, ok := bodyType.(hm.NonNullType); ok {
				return nonNull.Type, nil
			}
			return bodyType, nil
		}

		// Check if this is an infinite loop
		if f.Iterable == nil {
			// Infer body type
			bodyType, err := f.LoopBody.Infer(ctx, env, fresh)
			if err != nil {
				return nil, err
			}

			// Infinite loops return the last value from the body, or null
			if nonNull, ok := bodyType.(hm.NonNullType); ok {
				return nonNull.Type, nil
			}
			return bodyType, nil
		}

		// Infer the type of the iterable
		iterableType, err := f.Iterable.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// Handle iteration - check if we have two variables or one
		if f.KeyVariable == "" {
			// Single variable iteration
			var elementType hm.Type

			// Unwrap non-nullable iterable type
			if nonNullType, ok := iterableType.(hm.NonNullType); ok {
				iterableType = nonNullType.Type
			}

			// Check if it's a list type
			if listType, ok := iterableType.(ListType); ok {
				elementType = listType.Type
			} else if _, ok := iterableType.(GraphQLListType); ok {
				return nil, NewInferError(fmt.Errorf("cannot iterate over GraphQL list of objects directly; use .{field1, field2, ...} to select fields first"), f.Iterable)
			} else {
				return nil, NewInferError(fmt.Errorf("expected list type for single-variable iteration, got %s", iterableType), f.Iterable)
			}

			// Check if explicit type annotation matches inferred element type
			if f.Type != nil {
				declaredType, err := f.Type.Infer(ctx, env, fresh)
				if err != nil {
					return nil, err
				}
				if _, err := hm.Assignable(elementType, declaredType); err != nil {
					return nil, NewInferError(fmt.Errorf("type annotation %s doesn't match element type %s", declaredType, elementType), f)
				}
			}

			// Single variable iteration - just add the element variable
			loopEnv := env.Clone()
			loopEnv = loopEnv.Add(f.Variable, hm.NewScheme(nil, elementType))

			bodyType, err := f.LoopBody.Infer(ctx, loopEnv, fresh)
			if err != nil {
				return nil, err
			}

			// For loops return the last value from the body, or null if never executed
			// Make the result nullable since the loop might not execute
			if nonNull, ok := bodyType.(hm.NonNullType); ok {
				return nonNull.Type, nil
			}
			return bodyType, nil

		} else {
			// Two variable iteration
			iterableType, err := f.Iterable.Infer(ctx, env, fresh)
			if err != nil {
				return nil, err
			}

			loopEnv := env.Clone()

			// Check if it's a list type (key=index, value=element)
			if listType, ok := iterableType.(ListType); ok {
				elementType := listType.Type
				loopEnv = loopEnv.Add(f.KeyVariable, hm.NewScheme(nil, hm.NonNullType{Type: IntType})) // index
				loopEnv = loopEnv.Add(f.ValueVariable, hm.NewScheme(nil, elementType))                 // element
			} else if nonNullListType, ok := iterableType.(hm.NonNullType); ok {
				if listType, ok := nonNullListType.Type.(ListType); ok {
					elementType := listType.Type
					loopEnv = loopEnv.Add(f.KeyVariable, hm.NewScheme(nil, hm.NonNullType{Type: IntType})) // index
					loopEnv = loopEnv.Add(f.ValueVariable, hm.NewScheme(nil, elementType))                 // element
				} else {
					// Not a list, assume object iteration (key=string, value=string for now)
					loopEnv = loopEnv.Add(f.KeyVariable, hm.NewScheme(nil, hm.NonNullType{Type: StringType}))   // key
					loopEnv = loopEnv.Add(f.ValueVariable, hm.NewScheme(nil, hm.NonNullType{Type: StringType})) // value
				}
			} else {
				// Not a list, assume object iteration (key=string, value=string for now)
				loopEnv = loopEnv.Add(f.KeyVariable, hm.NewScheme(nil, hm.NonNullType{Type: StringType}))   // key
				loopEnv = loopEnv.Add(f.ValueVariable, hm.NewScheme(nil, hm.NonNullType{Type: StringType})) // value
			}

			bodyType, err := f.LoopBody.Infer(ctx, loopEnv, fresh)
			if err != nil {
				return nil, err
			}

			// For loops return the last value from the body, or null if never executed
			if nonNull, ok := bodyType.(hm.NonNullType); ok {
				return nonNull.Type, nil
			}
			return bodyType, nil
		}
	})
}

func (f *ForLoop) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, f, func() (Value, error) {
		var lastVal Value = NullValue{}

		// Handle condition-only loop (while-style)
		if f.Condition != nil {
			for {
				condVal, err := EvalNode(ctx, env, f.Condition)
				if err != nil {
					return nil, fmt.Errorf("evaluating condition: %w", err)
				}

				boolVal, ok := condVal.(BoolValue)
				if !ok {
					return nil, fmt.Errorf("condition must evaluate to boolean, got %T", condVal)
				}

				if !boolVal.Val {
					break
				}

				val, err := EvalNode(ctx, env, f.LoopBody)
				if err != nil {
					// Check if it's a break or continue
					var breakEx *BreakException
					var continueEx *ContinueException
					if errors.As(err, &breakEx) {
						break
					}
					if errors.As(err, &continueEx) {
						continue
					}
					return nil, fmt.Errorf("evaluating body: %w", err)
				}
				// Only update lastVal if there was no error
				lastVal = val
			}
			return lastVal, nil
		}

		// Handle infinite loop
		if f.Iterable == nil {
			for {
				val, err := EvalNode(ctx, env, f.LoopBody)
				if err != nil {
					// Check if it's a break or continue
					var breakEx *BreakException
					var continueEx *ContinueException
					if errors.As(err, &breakEx) {
						break
					}
					if errors.As(err, &continueEx) {
						continue
					}
					return nil, fmt.Errorf("evaluating body: %w", err)
				}
				// Only update lastVal if there was no error
				lastVal = val
			}
			return lastVal, nil
		}

		// Evaluate the iterable
		iterableVal, err := EvalNode(ctx, env, f.Iterable)
		if err != nil {
			return nil, fmt.Errorf("evaluating iterable: %w", err)
		}

		if f.KeyVariable == "" {
			// Single variable iteration
			if listVal, ok := iterableVal.(ListValue); ok {
				// Handle list iteration
				for _, element := range listVal.Elements {
					// Create new scope for loop iteration
					loopEnv := env.Clone()
					loopEnv.Set(f.Variable, element)

					// Evaluate the body
					val, err := EvalNode(ctx, loopEnv, f.LoopBody)
					if err != nil {
						// Check if it's a break or continue
						var breakEx *BreakException
						var continueEx *ContinueException
						if errors.As(err, &breakEx) {
							break
						}
						if errors.As(err, &continueEx) {
							continue
						}
						return nil, fmt.Errorf("evaluating loop body: %w", err)
					}
					// Only update lastVal if there was no error
					lastVal = val
				}
			} else {
				return nil, fmt.Errorf("single-variable iteration only supports lists, got %T", iterableVal)
			}
		} else {
			// Two variable iteration
			if listVal, ok := iterableVal.(ListValue); ok {
				// Handle list iteration with index (key=index, value=element)
				for i, element := range listVal.Elements {
					// Create new scope for loop iteration
					loopEnv := env.Clone()
					loopEnv.Set(f.KeyVariable, IntValue{Val: int(i)}) // key = index
					loopEnv.Set(f.ValueVariable, element)             // value = element

					// Evaluate the body
					val, err := EvalNode(ctx, loopEnv, f.LoopBody)
					if err != nil {
						// Check if it's a break or continue
						var breakEx *BreakException
						var continueEx *ContinueException
						if errors.As(err, &breakEx) {
							break
						}
						if errors.As(err, &continueEx) {
							continue
						}
						return nil, fmt.Errorf("evaluating loop body: %w", err)
					}
					// Only update lastVal if there was no error
					lastVal = val
				}
			} else {
				// Handle object iteration - for now, just return an error since we need more work on object types
				return nil, fmt.Errorf("object iteration not yet implemented for type %T", iterableVal)
			}
		}

		return lastVal, nil
	})
}

func (f *ForLoop) Walk(fn func(Node) bool) {
	if !fn(f) {
		return
	}
	if f.Iterable != nil {
		f.Iterable.Walk(fn)
	}
	if f.Condition != nil {
		f.Condition.Walk(fn)
	}
	f.LoopBody.Walk(fn)
}

// Break represents a break statement in a loop
type Break struct {
	InferredTypeHolder
	Loc *SourceLocation
}

var _ Node = (*Break)(nil)
var _ Evaluator = (*Break)(nil)

func (b *Break) DeclaredSymbols() []string {
	return nil // Break doesn't declare anything
}

func (b *Break) ReferencedSymbols() []string {
	return nil // Break doesn't reference anything
}

func (b *Break) Body() hm.Expression { return b }

func (b *Break) GetSourceLocation() *SourceLocation { return b.Loc }

func (b *Break) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(b, func() (hm.Type, error) {
		// Break returns a fresh type variable that can unify with anything
		t := fresh.Fresh()
		b.SetInferredType(t)
		return t, nil
	})
}

func (b *Break) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return nil, &BreakException{}
}

func (b *Break) Walk(fn func(Node) bool) {
	fn(b)
}

// Continue represents a continue statement in a loop
type Continue struct {
	InferredTypeHolder
	Loc *SourceLocation
}

var _ Node = (*Continue)(nil)
var _ Evaluator = (*Continue)(nil)

func (c *Continue) DeclaredSymbols() []string {
	return nil // Continue doesn't declare anything
}

func (c *Continue) ReferencedSymbols() []string {
	return nil // Continue doesn't reference anything
}

func (c *Continue) Body() hm.Expression { return c }

func (c *Continue) GetSourceLocation() *SourceLocation { return c.Loc }

func (c *Continue) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(c, func() (hm.Type, error) {
		// Continue returns a fresh type variable that can unify with anything
		t := fresh.Fresh()
		c.SetInferredType(t)
		return t, nil
	})
}

func (c *Continue) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return nil, &ContinueException{}
}

func (c *Continue) Walk(fn func(Node) bool) {
	fn(c)
}

// BreakException is used to signal a break statement
type BreakException struct{}

func (e *BreakException) Error() string {
	return "break outside of loop"
}

// ContinueException is used to signal a continue statement
type ContinueException struct{}

func (e *ContinueException) Error() string {
	return "continue outside of loop"
}

// Let represents a let binding expression
type Let struct {
	InferredTypeHolder
	Name  string
	Value Node
	Expr  Node
	Loc   *SourceLocation
}

var _ Node = (*Let)(nil)
var _ Evaluator = (*Let)(nil)

func (l *Let) DeclaredSymbols() []string {
	return nil // Let expressions don't declare symbols in the global scope
}

func (l *Let) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, l.Value.ReferencedSymbols()...)
	symbols = append(symbols, l.Expr.ReferencedSymbols()...)
	return symbols
}

func (l *Let) Body() hm.Expression { return l }

func (l *Let) GetSourceLocation() *SourceLocation { return l.Loc }

func (l *Let) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(l, func() (hm.Type, error) {
		valueType, err := l.Value.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		newEnv := env.Clone()
		newEnv.Add(l.Name, hm.NewScheme(nil, valueType))

		return l.Expr.Infer(ctx, newEnv, fresh)
	})
}

func (l *Let) Eval(ctx context.Context, env EvalEnv) (Value, error) {
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

func (l *Let) Walk(fn func(Node) bool) {
	if !fn(l) {
		return
	}
	l.Value.Walk(fn)
	l.Expr.Walk(fn)
}

// TypeHint represents a type hint expression using :: syntax
type TypeHint struct {
	InferredTypeHolder
	Expr Node
	Type TypeNode
	Loc  *SourceLocation
}

var _ Node = (*TypeHint)(nil)
var _ Evaluator = (*TypeHint)(nil)

func (t *TypeHint) DeclaredSymbols() []string {
	return nil // Type hints don't declare symbols
}

func (t *TypeHint) ReferencedSymbols() []string {
	return t.Expr.ReferencedSymbols()
}

func (t *TypeHint) Body() hm.Expression { return t }

func (t *TypeHint) GetSourceLocation() *SourceLocation { return t.Loc }

func (t *TypeHint) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(t, func() (hm.Type, error) {
		// Infer the type of the expression
		exprType, err := t.Expr.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// Infer the type of the type hint
		hintType, err := t.Type.Infer(ctx, env, fresh)
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
		if subs, err := hm.Assignable(exprCore, hintCore); err == nil {
			// Unification succeeded - apply substitutions to the hint and return it
			// This allows the hint to override the expression's type (including nullability)
			result := hintType.Apply(subs).(hm.Type)
			return result, nil
		}

		// Core unification failed, try the original approach with subtyping
		subs, err := hm.Assignable(exprType, hintType)
		if err != nil {
			return nil, NewInferError(fmt.Errorf("type hint mismatch: expression has type %s, but hint expects %s", exprType, hintType), t.Expr)
		}

		// Apply substitutions to the hint type and return it
		result := hintType.Apply(subs).(hm.Type)
		return result, nil
	})
}

func (t *TypeHint) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, t, func() (Value, error) {
		// Type hints don't change runtime behavior - just evaluate the expression
		return EvalNode(ctx, env, t.Expr)
	})
}

func (t *TypeHint) Walk(fn func(Node) bool) {
	if !fn(t) {
		return
	}
	t.Expr.Walk(fn)
}

// BlockArg represents a block argument expression with bidirectional type inference
// Block args are used in function calls like: numbers.map: { x -> x * 2 }
// They differ from lambdas in that they receive both parameter types AND
// expected return type from the function signature they're passed to.
type BlockArg struct {
	InferredTypeHolder
	Args     []*SlotDecl
	BodyNode Node
	Loc      *SourceLocation

	// ExpectedParamTypes are the parameter types expected by the function signature.
	// Set by FunCall.Infer() before calling BlockArg.Infer()
	ExpectedParamTypes []hm.Type

	// ExpectedReturnType is the return type expected by the function signature.
	// Set by FunCall.Infer() before calling BlockArg.Infer()
	ExpectedReturnType hm.Type

	// Inferred is the final inferred function type
	Inferred *hm.FunctionType

	// InferredScope is the environment with parameters in scope
	InferredScope Env
}

var _ Node = &BlockArg{}
var _ Evaluator = &BlockArg{}

func (b *BlockArg) DeclaredSymbols() []string {
	return nil // BlockArgs don't declare symbols in the global scope
}

func (b *BlockArg) ReferencedSymbols() []string {
	// BlockArgs reference symbols from their body, but not their parameters
	bodySymbols := b.BodyNode.ReferencedSymbols()

	// Filter out parameter names since they're bound locally
	paramNames := make(map[string]bool)
	for _, arg := range b.Args {
		paramNames[arg.Name.Name] = true
	}

	var referenced []string
	for _, sym := range bodySymbols {
		if !paramNames[sym] {
			referenced = append(referenced, sym)
		}
	}

	return referenced
}

func (b *BlockArg) Body() hm.Expression { return b }

func (b *BlockArg) GetSourceLocation() *SourceLocation { return b.Loc }

func (b *BlockArg) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(b, func() (hm.Type, error) {
		// Check if block has too many parameters
		if len(b.Args) > len(b.ExpectedParamTypes) {
			return nil, NewInferError(
				fmt.Errorf("block has %d parameters but expected at most %d",
					len(b.Args), len(b.ExpectedParamTypes)),
				b,
			)
		}

		// Clone environment for closure semantics
		newEnv := env.Clone()
		b.InferredScope = newEnv.(Env)

		// Add user-provided parameters to environment with expected types
		argSchemes := []Keyed[*hm.Scheme]{}
		for i, arg := range b.Args {
			var paramType hm.Type
			if i < len(b.ExpectedParamTypes) && b.ExpectedParamTypes[i] != nil {
				// Use the expected type from the function signature
				paramType = b.ExpectedParamTypes[i]
			} else {
				// No expected type - create a fresh type variable
				paramType = fresh.Fresh()
			}

			// Add parameter to environment
			newEnv.Add(arg.Name.Name, hm.NewScheme(nil, paramType))
			argSchemes = append(argSchemes, Keyed[*hm.Scheme]{
				Key:   arg.Name.Name,
				Value: hm.NewScheme(nil, paramType),
			})
		}

		// Add any remaining expected parameters to the function type signature
		// but NOT to the environment (they will be ignored by the block)
		for i := len(b.Args); i < len(b.ExpectedParamTypes); i++ {
			paramType := b.ExpectedParamTypes[i]
			// Generate a parameter name for the type signature
			paramName := fmt.Sprintf("_unused%d", i)
			argSchemes = append(argSchemes, Keyed[*hm.Scheme]{
				Key:   paramName,
				Value: hm.NewScheme(nil, paramType),
			})
		}

		// Infer the body with parameters in scope
		bodyType, err := b.BodyNode.Infer(ctx, newEnv, fresh)
		if err != nil {
			return nil, fmt.Errorf("BlockArg body: %w", err)
		}

		// If we have an expected return type, unify with the body type
		if b.ExpectedReturnType != nil {
			if _, err := hm.Assignable(bodyType, b.ExpectedReturnType); err != nil {
				return nil, NewInferError(
					fmt.Errorf("block argument body has type %s but expected type %s",
						bodyType, b.ExpectedReturnType),
					b.BodyNode,
				)
			}
		}

		// Build the function type
		b.Inferred = hm.NewFnType(NewRecordType("", argSchemes...), bodyType)
		t := b.Inferred
		b.SetInferredType(t)
		return t, nil
	})
}

func (b *BlockArg) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, b, func() (Value, error) {
		if b.Inferred == nil {
			return nil, fmt.Errorf("BlockArg.Eval: function type not inferred")
		}

		// Extract all parameter names from the inferred function type
		// This includes both user-provided and unused parameters
		recordType, ok := b.Inferred.Arg().(*RecordType)
		if !ok {
			return nil, fmt.Errorf("BlockArg.Eval: expected RecordType for function arguments")
		}

		argNames := make([]string, len(recordType.Fields))
		for i, field := range recordType.Fields {
			argNames[i] = field.Key
		}

		return FunctionValue{
			Args:     argNames,
			Body:     b.BodyNode,
			Closure:  env,
			FnType:   b.Inferred,
			Defaults: make(map[string]Node), // Block args don't support defaults
			ArgDecls: b.Args,
		}, nil
	})
}

func (b *BlockArg) Walk(fn func(Node) bool) {
	if !fn(b) {
		return
	}
	// Walk parameters
	for _, arg := range b.Args {
		arg.Walk(fn)
	}
	// Walk body
	b.BodyNode.Walk(fn)
}

// instantiateListMethod instantiates a list method's type by substituting
// the type variable 'a' with the actual element type of the list.
func instantiateListMethod(def BuiltinDef, elemType hm.Type) hm.Type {
	// Build a record type for the method arguments, replacing TypeVar('a') with elemType
	args := NewRecordType("")
	for _, param := range def.ParamTypes {
		paramType := substituteTypeVar(param.Type, 'a', elemType)
		args.Add(param.Name, hm.NewScheme(nil, paramType))
	}

	// Similarly, handle the return type
	returnType := substituteTypeVar(def.ReturnType, 'a', elemType)

	fnType := hm.NewFnType(args, returnType)

	// Copy the block type if present, substituting type variables
	if def.BlockType != nil {
		blockType := substituteTypeVar(def.BlockType, 'a', elemType).(*hm.FunctionType)
		fnType.SetBlock(blockType)
	}

	return fnType
}

// substituteTypeVar recursively replaces a type variable with a concrete type
func substituteTypeVar(t hm.Type, tv hm.TypeVariable, replacement hm.Type) hm.Type {
	switch typ := t.(type) {
	case hm.TypeVariable:
		if typ == tv {
			return replacement
		}
		return typ
	case hm.NonNullType:
		return hm.NonNullType{Type: substituteTypeVar(typ.Type, tv, replacement)}
	case ListType:
		return ListType{Type: substituteTypeVar(typ.Type, tv, replacement)}
	case *hm.FunctionType:
		newFnType := hm.NewFnType(
			substituteTypeVar(typ.Arg(), tv, replacement),
			substituteTypeVar(typ.Ret(false), tv, replacement),
		)
		// Preserve the block type
		if typ.Block() != nil {
			newBlockType := substituteTypeVar(typ.Block(), tv, replacement).(*hm.FunctionType)
			newFnType.SetBlock(newBlockType)
		}
		return newFnType
	case *RecordType:
		newRec := NewRecordType(typ.Named)
		for _, field := range typ.Fields {
			fieldType, _ := field.Value.Type()
			newFieldType := substituteTypeVar(fieldType, tv, replacement)
			newRec.Add(field.Key, hm.NewScheme(nil, newFieldType))
		}
		newRec.Directives = typ.Directives
		return newRec
	default:
		return t
	}
}
