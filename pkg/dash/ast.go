package dash

import (
	"context"
	"fmt"
	"strings"

	"github.com/chewxy/hm"
	"github.com/vito/dash/introspection"
)

type Node interface {
	hm.Expression
	hm.Inferer
	GetSourceLocation() *SourceLocation
}

type Keyed[X any] struct {
	Key        string
	Value      X
	Positional bool // true if this argument was passed positionally
}

type Visibility int

const (
	PublicVisibility Visibility = iota
	PrivateVisibility
)

type FunCall struct {
	Fun  Node
	Args Record
	Loc  *SourceLocation
}

var _ Node = FunCall{}
var _ Evaluator = FunCall{}

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
				return nil, fmt.Errorf("FunCall.Infer: %q cannot unify (%s ~ %s): %w", k, dt, it, err)
			}
		}
		// TODO: check required args are specified?
		return ft.Ret(false), nil
	case *Module:
		// For modules, use the original logic for now
		// TODO: Add proper positional argument support for modules
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
				return nil, fmt.Errorf("FunCall.Infer: %q cannot unify (%s ~ %s): %w", k, dt, it, err)
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

	case ModuleValue:
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

type FunDecl struct {
	Named      string
	Args       []SlotDecl
	Form       Node
	Ret        TypeNode
	Visibility Visibility
	Loc        *SourceLocation
}

var _ hm.Expression = FunDecl{}
var _ Evaluator = FunDecl{}

func (f FunDecl) Body() hm.Expression { return f.Form }

func (f FunDecl) GetSourceLocation() *SourceLocation { return f.Loc }

var _ hm.Inferer = FunDecl{}

func (f FunDecl) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// TODO: Lambda semantics

	var err error

	// closure
	env = env.Clone()

	args := []Keyed[*hm.Scheme]{}
	for _, arg := range f.Args {
		var definedArgType hm.Type

		if arg.Type_ != nil {
			// TODO should this take fresh? seems like maybe not?
			definedArgType, err = arg.Type_.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("FuncDecl.Infer arg: %w", err)
			}
		}

		var inferredValType hm.Type
		if arg.Value != nil {
			inferredValType, err = arg.Value.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("FuncDecl.Infer arg: %w", err)
			}
		}

		if definedArgType != nil && inferredValType != nil {
			if !definedArgType.Eq(inferredValType) {
				return nil, fmt.Errorf("FuncDecl.Infer arg: %q mismatch: defined as %s, inferred as %s", arg.Named, definedArgType, inferredValType)
			}
		} else if definedArgType != nil {
			inferredValType = definedArgType
		} else if inferredValType != nil {
			definedArgType = inferredValType
		} else {
			return nil, fmt.Errorf("FuncDecl.Infer arg: %q has no type or value", arg.Named)
		}

		scheme := hm.NewScheme(nil, definedArgType)
		env.Add(arg.Named, scheme)
		args = append(args, Keyed[*hm.Scheme]{Key: arg.Named, Value: scheme, Positional: false})
	}

	var definedRet hm.Type

	if f.Ret != nil {
		definedRet, err = f.Ret.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("FuncDecl.Infer: Ret: %w", err)
		}
	}

	inferredRet, err := f.Form.Infer(env, fresh)
	if err != nil {
		return nil, fmt.Errorf("FuncDecl(%s).Infer: Form: %w", f.Named, err)
	}

	if definedRet != nil {
		// TODO: Unify?
		if !definedRet.Eq(inferredRet) {
			return nil, fmt.Errorf("FuncDecl.Infer: %q mismatch: defined as %s, inferred as %s", f.Named, definedRet, inferredRet)
		}
	}

	return hm.NewFnType(NewRecordType("", args...), inferredRet), nil
}

func (f FunDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Extract argument names from SlotDecl
	argNames := make([]string, len(f.Args))
	defaults := make(map[string]Node)
	
	for i, arg := range f.Args {
		argNames[i] = arg.Named
		if arg.Value != nil {
			defaults[arg.Named] = arg.Value
		}
	}

	// Create a function type for this declaration
	argTypes := make([]Keyed[*hm.Scheme], len(f.Args))
	for i, arg := range f.Args {
		// Use a placeholder type for now - in a full implementation we'd get this from type inference
		argTypes[i] = Keyed[*hm.Scheme]{
			Key:        arg.Named,
			Value:      hm.NewScheme(nil, hm.TypeVariable(byte('a'+i))),
			Positional: false,
		}
	}

	// Create the function type
	fnType := hm.NewFnType(NewRecordType("", argTypes...), hm.TypeVariable('r'))

	return FunctionValue{
		Args:     argNames,
		Body:     f.Form,
		Closure:  env,
		FnType:   fnType,
		Defaults: defaults,
	}, nil
}

type List struct {
	Elements []Node
	Loc      *SourceLocation
}

var _ Node = List{}
var _ Evaluator = List{}

func (l List) Infer(env hm.Env, f hm.Fresher) (hm.Type, error) {
	if len(l.Elements) == 0 {
		// For now, just return the original approach and document this as a known issue
		// The real fix requires changes to how the HM library handles recursive types
		tv := f.Fresh()
		return NonNullType{ListType{tv}}, nil
	}

	var t hm.Type
	for i, el := range l.Elements {
		et, err := el.Infer(env, f)
		if err != nil {
			return nil, err
		}
		if t == nil {
			t = et
		} else if _, err := UnifyWithCompatibility(t, et); err != nil {
			// TODO: is this right?
			return nil, fmt.Errorf("unify index %d: %w", i, err)
		}
	}
	return NonNullType{ListType{t}}, nil
}

func (l List) Body() hm.Expression { return l }

func (l List) GetSourceLocation() *SourceLocation { return l.Loc }

func (l List) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	if len(l.Elements) == 0 {
		return ListValue{Elements: []Value{}, ElemType: hm.TypeVariable('a')}, nil
	}

	values := make([]Value, len(l.Elements))
	var elemType hm.Type

	for i, elem := range l.Elements {
		val, err := EvalNode(ctx, env, elem)
		if err != nil {
			return nil, fmt.Errorf("evaluating list element %d: %w", i, err)
		}
		values[i] = val
		if i == 0 {
			elemType = val.Type()
		}
	}

	return ListValue{Elements: values, ElemType: elemType}, nil
}

// TODO record literals?

type Symbol struct {
	Name     string
	AutoCall bool
	Loc      *SourceLocation
}

var _ Node = Symbol{}
var _ Evaluator = Symbol{}

func autoCallFnType(t hm.Type) hm.Type {
	// Check if this is a zero-arity function and return its return type
	if ft, ok := t.(*hm.FunctionType); ok {
		if rt, ok := ft.Arg().(*RecordType); ok {
			// Check if all fields are optional (no NonNullType fields)
			// Note: This function only has type information, not default value information
			// The actual auto-call decision is made in isAutoCallableFn with FunctionValue
			hasRequiredArgs := false
			for _, field := range rt.Fields {
				if fieldType, _ := field.Value.Type(); fieldType != nil {
					if _, isNonNull := fieldType.(NonNullType); isNonNull {
						hasRequiredArgs = true
						break
					}
				}
			}

			if !hasRequiredArgs {
				// All arguments are optional, return the return type
				return ft.Ret(false)
			}
		}
	}
	return t
}

func (s Symbol) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	scheme, found := env.SchemeOf(s.Name)
	if !found {
		return nil, fmt.Errorf("Symbol.Infer: %q not found in env", s.Name)
	}
	t, _ := scheme.Type()
	if s.AutoCall {
		return autoCallFnType(t), nil
	}
	return t, nil
}

func (s Symbol) Body() hm.Expression { return s }

func (s Symbol) GetSourceLocation() *SourceLocation { return s.Loc }

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

// isAutoCallableFn checks if a value is a function with zero required arguments
func isAutoCallableFn(val Value) bool {
	switch fn := val.(type) {
	case FunctionValue:
		// For FunctionValue, check if all arguments either have default values or are optional
		for _, argName := range fn.Args {
			// If this argument has a default value, it's optional
			if _, hasDefault := fn.Defaults[argName]; hasDefault {
				continue
			}
			
			// If no default, check if the type is nullable (optional)
			if rt, ok := fn.FnType.Arg().(*RecordType); ok {
				scheme, found := rt.SchemeOf(argName)
				if found {
					if fieldType, _ := scheme.Type(); fieldType != nil {
						if _, isNonNull := fieldType.(NonNullType); isNonNull {
							// This is a required argument with no default value
							return false
						}
					}
				}
			}
		}
		// All arguments are either optional or have defaults, so this function can be auto-called
		return true
	case GraphQLFunction:
		// Check if the function has zero REQUIRED arguments (all args are optional)
		return hasZeroRequiredArgs(fn.Field)
	case BuiltinFunction:
		// For builtin functions, check if all arguments are optional (would need metadata)
		// For now, only consider truly zero-argument builtins as auto-callable
		if rt, ok := fn.FnType.Arg().(*RecordType); ok {
			return len(rt.Fields) == 0
		}
		return false
	default:
		return false
	}
}

// hasZeroRequiredArgs checks if a GraphQL field has zero required arguments
func hasZeroRequiredArgs(field *introspection.Field) bool {
	if field == nil {
		return false
	}

	// Check if all arguments are optional (nullable or have defaults)
	for _, arg := range field.Args {
		if arg.TypeRef.Kind == "NON_NULL" && arg.DefaultValue == nil {
			// This argument is required (non-null with no default)
			return false
		}
	}

	// All arguments are optional, so this function can be called with zero args
	return true
}

// autoCallFn calls a zero-arity function with empty arguments
func autoCallFn(ctx context.Context, env EvalEnv, val Value) (Value, error) {
	emptyArgs := make(map[string]Value)

	switch fn := val.(type) {
	case FunctionValue:
		// Simulate a proper function call with empty arguments to trigger default value handling
		fnEnv := fn.Closure.Clone()
		for _, argName := range fn.Args {
			if defaultExpr, hasDefault := fn.Defaults[argName]; hasDefault {
				// Evaluate the default value in the function's closure
				defaultVal, err := EvalNode(ctx, fn.Closure, defaultExpr)
				if err != nil {
					return nil, fmt.Errorf("evaluating default value for argument %q: %w", argName, err)
				}
				fnEnv.Set(argName, defaultVal)
			}
		}
		return EvalNode(ctx, fnEnv, fn.Body)

	case GraphQLFunction:
		// GraphQL function call with empty arguments
		return fn.Call(ctx, env, emptyArgs)

	case BuiltinFunction:
		// Builtin function call with empty arguments
		return fn.Call(ctx, env, emptyArgs)

	default:
		return nil, fmt.Errorf("callZeroArityFunction: %T is not a callable function", val)
	}
}

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

func (d Select) Body() hm.Expression { return d }

func (d Select) GetSourceLocation() *SourceLocation { return d.Loc }

func (d Select) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// If this is a function call (Args present), delegate to FunCall
	// implementation
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
				// If this is a function call (Args present), call the function
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

type Default struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Default{}
var _ Evaluator = Default{}

func (d Default) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := d.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := d.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// For the default operator, the left side can be nullable and the right side
	// provides the fallback value. We need to unify the non-null version of the
	// left type with the right type.

	// Unify types with subtyping support for nullable/NonNull compatibility
	if _, err := UnifyWithCompatibility(lt, rt); err != nil {
		return nil, fmt.Errorf("Default.Infer: mismatched types: %s and %s cannot be unified: %w", lt, rt, err)
	}

	// Return the right type (the fallback value type)
	return rt, nil
}

func (d Default) Body() hm.Expression { return d }

func (d Default) GetSourceLocation() *SourceLocation { return d.Loc }

func (d Default) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, d.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}

	// Check if left value is null
	if _, isNull := leftVal.(NullValue); isNull {
		// Use the right side as default
		return EvalNode(ctx, env, d.Right)
	}

	return leftVal, nil
}

type Equality struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Equality{}
var _ Evaluator = Equality{}

func (e Equality) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Type check both sides for validity, but allow cross-type comparison at runtime
	_, err := e.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	_, err = e.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// Equality always returns a boolean
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (e Equality) Body() hm.Expression { return e }

func (e Equality) GetSourceLocation() *SourceLocation { return e.Loc }

func (e Equality) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, e.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}

	rightVal, err := EvalNode(ctx, env, e.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}

	// Compare the values
	equal := valuesEqual(leftVal, rightVal)
	return BoolValue{Val: equal}, nil
}

// valuesEqual compares two values for equality
func valuesEqual(left, right Value) bool {
	// Handle null values
	_, leftIsNull := left.(NullValue)
	_, rightIsNull := right.(NullValue)
	if leftIsNull && rightIsNull {
		return true
	}
	if leftIsNull || rightIsNull {
		return false
	}

	// Compare by type
	switch l := left.(type) {
	case StringValue:
		if r, ok := right.(StringValue); ok {
			return l.Val == r.Val
		}
	case IntValue:
		if r, ok := right.(IntValue); ok {
			return l.Val == r.Val
		}
	case BoolValue:
		if r, ok := right.(BoolValue); ok {
			return l.Val == r.Val
		}
	case ListValue:
		if r, ok := right.(ListValue); ok {
			if len(l.Elements) != len(r.Elements) {
				return false
			}
			for i := range l.Elements {
				if !valuesEqual(l.Elements[i], r.Elements[i]) {
					return false
				}
			}
			return true
		}
	}

	// Different types or unsupported comparison
	return false
}

type Addition struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Addition{}
var _ Evaluator = Addition{}

func (a Addition) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := a.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := a.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	if _, err := UnifyWithCompatibility(lt, rt); err != nil {
		return nil, fmt.Errorf("Addition.Infer: mismatched types: %s and %s cannot be unified: %w", lt, rt, err)
	}
	return lt, nil
}

func (a Addition) Body() hm.Expression { return a }

func (a Addition) GetSourceLocation() *SourceLocation { return a.Loc }

func (a Addition) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, a.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, a.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}
	switch l := leftVal.(type) {
	case IntValue:
		if r, ok := rightVal.(IntValue); ok {
			return IntValue{Val: l.Val + r.Val}, nil
		}
	case StringValue:
		if r, ok := rightVal.(StringValue); ok {
			return StringValue{Val: l.Val + r.Val}, nil
		}
	case ListValue:
		if r, ok := rightVal.(ListValue); ok {
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
	}
	return nil, fmt.Errorf("addition not supported for types %T and %T", leftVal, rightVal)
}

type Subtraction struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Subtraction{}
var _ Evaluator = Subtraction{}

func (s Subtraction) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := s.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := s.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	if _, err := UnifyWithCompatibility(lt, rt); err != nil {
		return nil, fmt.Errorf("Subtraction.Infer: mismatched types: %s and %s cannot be unified: %w", lt, rt, err)
	}
	return lt, nil
}

func (s Subtraction) Body() hm.Expression { return s }

func (s Subtraction) GetSourceLocation() *SourceLocation { return s.Loc }

func (s Subtraction) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, s.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, s.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}
	switch l := leftVal.(type) {
	case IntValue:
		if r, ok := rightVal.(IntValue); ok {
			return IntValue{Val: l.Val - r.Val}, nil
		}
	}
	return nil, fmt.Errorf("subtraction not supported for types %T and %T", leftVal, rightVal)
}

type Multiplication struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Multiplication{}
var _ Evaluator = Multiplication{}

func (m Multiplication) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := m.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := m.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	if _, err := UnifyWithCompatibility(lt, rt); err != nil {
		return nil, fmt.Errorf("Multiplication.Infer: mismatched types: %s and %s cannot be unified: %w", lt, rt, err)
	}
	return lt, nil
}

func (m Multiplication) Body() hm.Expression { return m }

func (m Multiplication) GetSourceLocation() *SourceLocation { return m.Loc }

func (m Multiplication) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, m.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, m.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}
	switch l := leftVal.(type) {
	case IntValue:
		if r, ok := rightVal.(IntValue); ok {
			return IntValue{Val: l.Val * r.Val}, nil
		}
	}
	return nil, fmt.Errorf("multiplication not supported for types %T and %T", leftVal, rightVal)
}

type Division struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Division{}
var _ Evaluator = Division{}

func (d Division) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := d.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := d.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	if _, err := UnifyWithCompatibility(lt, rt); err != nil {
		return nil, fmt.Errorf("Division.Infer: mismatched types: %s and %s cannot be unified: %w", lt, rt, err)
	}
	return lt, nil
}

func (d Division) Body() hm.Expression { return d }

func (d Division) GetSourceLocation() *SourceLocation { return d.Loc }

func (d Division) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, d.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, d.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}
	switch l := leftVal.(type) {
	case IntValue:
		if r, ok := rightVal.(IntValue); ok {
			if r.Val == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return IntValue{Val: l.Val / r.Val}, nil
		}
	}
	return nil, fmt.Errorf("division not supported for types %T and %T", leftVal, rightVal)
}

type Modulo struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Modulo{}
var _ Evaluator = Modulo{}

func (m Modulo) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := m.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := m.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	if _, err := UnifyWithCompatibility(lt, rt); err != nil {
		return nil, fmt.Errorf("Modulo.Infer: mismatched types: %s and %s cannot be unified: %w", lt, rt, err)
	}
	return lt, nil
}

func (m Modulo) Body() hm.Expression { return m }

func (m Modulo) GetSourceLocation() *SourceLocation { return m.Loc }

func (m Modulo) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, m.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, m.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}
	switch l := leftVal.(type) {
	case IntValue:
		if r, ok := rightVal.(IntValue); ok {
			if r.Val == 0 {
				return nil, fmt.Errorf("modulo by zero")
			}
			return IntValue{Val: l.Val % r.Val}, nil
		}
	}
	return nil, fmt.Errorf("modulo not supported for types %T and %T", leftVal, rightVal)
}

type Inequality struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Inequality{}
var _ Evaluator = Inequality{}

func (i Inequality) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Type check both sides for validity, but allow cross-type comparison at runtime
	_, err := i.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	_, err = i.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// Inequality always returns a boolean
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (i Inequality) Body() hm.Expression { return i }

func (i Inequality) GetSourceLocation() *SourceLocation { return i.Loc }

func (i Inequality) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, i.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, i.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}

	// Compare the values and return the opposite of equality
	equal := valuesEqual(leftVal, rightVal)
	return BoolValue{Val: !equal}, nil
}

type LessThan struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = LessThan{}
var _ Evaluator = LessThan{}

func (l LessThan) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Type check both sides for validity
	_, err := l.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	_, err = l.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// LessThan always returns a boolean
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (l LessThan) Body() hm.Expression { return l }

func (l LessThan) GetSourceLocation() *SourceLocation { return l.Loc }

func (l LessThan) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, l.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, l.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}

	switch lv := leftVal.(type) {
	case IntValue:
		if rv, ok := rightVal.(IntValue); ok {
			return BoolValue{Val: lv.Val < rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("less than comparison not supported for types %T and %T", leftVal, rightVal)
}

type GreaterThan struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = GreaterThan{}
var _ Evaluator = GreaterThan{}

func (g GreaterThan) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Type check both sides for validity
	_, err := g.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	_, err = g.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// GreaterThan always returns a boolean
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (g GreaterThan) Body() hm.Expression { return g }

func (g GreaterThan) GetSourceLocation() *SourceLocation { return g.Loc }

func (g GreaterThan) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, g.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, g.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}

	switch lv := leftVal.(type) {
	case IntValue:
		if rv, ok := rightVal.(IntValue); ok {
			return BoolValue{Val: lv.Val > rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("greater than comparison not supported for types %T and %T", leftVal, rightVal)
}

type LessThanEqual struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = LessThanEqual{}
var _ Evaluator = LessThanEqual{}

func (l LessThanEqual) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Type check both sides for validity
	_, err := l.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	_, err = l.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// LessThanEqual always returns a boolean
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (l LessThanEqual) Body() hm.Expression { return l }

func (l LessThanEqual) GetSourceLocation() *SourceLocation { return l.Loc }

func (l LessThanEqual) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, l.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, l.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}

	switch lv := leftVal.(type) {
	case IntValue:
		if rv, ok := rightVal.(IntValue); ok {
			return BoolValue{Val: lv.Val <= rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("less than or equal comparison not supported for types %T and %T", leftVal, rightVal)
}

type GreaterThanEqual struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = GreaterThanEqual{}
var _ Evaluator = GreaterThanEqual{}

func (g GreaterThanEqual) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Type check both sides for validity
	_, err := g.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	_, err = g.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// GreaterThanEqual always returns a boolean
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (g GreaterThanEqual) Body() hm.Expression { return g }

func (g GreaterThanEqual) GetSourceLocation() *SourceLocation { return g.Loc }

func (g GreaterThanEqual) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, g.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, g.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}

	switch lv := leftVal.(type) {
	case IntValue:
		if rv, ok := rightVal.(IntValue); ok {
			return BoolValue{Val: lv.Val >= rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("greater than or equal comparison not supported for types %T and %T", leftVal, rightVal)
}

type Null struct {
	Loc *SourceLocation
}

var _ Node = Null{}
var _ Evaluator = Null{}

func (n Null) Body() hm.Expression { return n }

func (n Null) GetSourceLocation() *SourceLocation { return n.Loc }

func (Null) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return fresh.Fresh(), nil
}

func (n Null) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return NullValue{}, nil
}

var (
	// Null does not have a type. Its type is always inferred as a free variable.
	// NullType    = NewClass("Null")

	BooleanType = NewModule("Boolean")
	StringType  = NewModule("String")
	IntType     = NewModule("Int")
)

type String struct {
	Value string
	Loc   *SourceLocation
}

var _ Node = String{}
var _ Evaluator = String{}

func (s String) Body() hm.Expression { return s }

func (s String) GetSourceLocation() *SourceLocation { return s.Loc }

func (s String) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return NonNullTypeNode{NamedTypeNode{"String"}}.Infer(env, fresh)
}

func (s String) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return StringValue{Val: s.Value}, nil
}

type Quoted struct {
	Quoter string
	Raw    string
}

type Boolean struct {
	Value bool
	Loc   *SourceLocation
}

var _ Node = Boolean{}
var _ Evaluator = Boolean{}

func (b Boolean) Body() hm.Expression { return b }

func (b Boolean) GetSourceLocation() *SourceLocation { return b.Loc }

func (b Boolean) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (b Boolean) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return BoolValue{Val: b.Value}, nil
}

type Int struct {
	Value int64
	Loc   *SourceLocation
}

var _ Node = Int{}
var _ Evaluator = Int{}

func (i Int) Body() hm.Expression { return i }

func (i Int) GetSourceLocation() *SourceLocation { return i.Loc }

func (i Int) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return NonNullTypeNode{NamedTypeNode{"Int"}}.Infer(env, fresh)
}

func (i Int) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return IntValue{Val: int(i.Value)}, nil
}

// Additional language constructs

type Conditional struct {
	Condition Node
	Then      Block
	Else      any
	Loc       *SourceLocation
}

var _ Node = Conditional{}
var _ Evaluator = Conditional{}

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
			return nil, fmt.Errorf("Conditional.Infer: then and else branches must have same type: %s != %s", thenType, elseType)
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

type Let struct {
	Name  string
	Value Node
	Expr  Node
	Loc   *SourceLocation
}

var _ Node = Let{}
var _ Evaluator = Let{}

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

type Lambda struct {
	Args []SlotDecl
	Expr Node
	Loc  *SourceLocation
}

var _ Node = Lambda{}
var _ Evaluator = Lambda{}

func (l Lambda) Body() hm.Expression { return l }

func (l Lambda) GetSourceLocation() *SourceLocation { return l.Loc }

func (l Lambda) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	newEnv := env.Clone()
	argTypes := make([]Keyed[*hm.Scheme], len(l.Args))

	for i, arg := range l.Args {
		var definedArgType hm.Type
		var err error

		if arg.Type_ != nil {
			definedArgType, err = arg.Type_.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("Lambda.Infer arg: %w", err)
			}
		}

		var inferredValType hm.Type
		if arg.Value != nil {
			inferredValType, err = arg.Value.Infer(env, fresh)
			if err != nil {
				return nil, fmt.Errorf("Lambda.Infer arg: %w", err)
			}
		}

		var finalArgType hm.Type
		if definedArgType != nil && inferredValType != nil {
			if !definedArgType.Eq(inferredValType) {
				return nil, fmt.Errorf("Lambda.Infer arg: %q mismatch: defined as %s, inferred as %s", arg.Named, definedArgType, inferredValType)
			}
			finalArgType = definedArgType
		} else if definedArgType != nil {
			finalArgType = definedArgType
		} else if inferredValType != nil {
			finalArgType = inferredValType
		} else {
			finalArgType = fresh.Fresh()
		}

		scheme := hm.NewScheme(nil, finalArgType)
		newEnv.Add(arg.Named, scheme)
		argTypes[i] = Keyed[*hm.Scheme]{Key: arg.Named, Value: scheme, Positional: false}
	}

	bodyType, err := l.Expr.Infer(newEnv, fresh)
	if err != nil {
		return nil, err
	}

	return hm.NewFnType(NewRecordType("", argTypes...), bodyType), nil
}

func (l Lambda) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Extract argument names and defaults from SlotDecl
	argNames := make([]string, len(l.Args))
	defaults := make(map[string]Node)
	
	for i, arg := range l.Args {
		argNames[i] = arg.Named
		if arg.Value != nil {
			defaults[arg.Named] = arg.Value
		}
	}

	// Create function type for this lambda
	argTypes := make([]Keyed[*hm.Scheme], len(l.Args))
	for i, arg := range l.Args {
		// Use a placeholder type for now - in a full implementation we'd get this from type inference
		argTypes[i] = Keyed[*hm.Scheme]{Key: arg.Named, Value: hm.NewScheme(nil, hm.TypeVariable(byte('a'+i))), Positional: false}
	}

	// Create the function type
	fnType := hm.NewFnType(NewRecordType("", argTypes...), hm.TypeVariable('r'))

	return FunctionValue{
		Args:     argNames,
		Body:     l.Expr,
		Closure:  env,
		FnType:   fnType,
		Defaults: defaults,
	}, nil
}

type Match struct {
	Expr  Node
	Cases []MatchCase
	Loc   *SourceLocation
}

var _ Node = Match{}

func (m Match) Body() hm.Expression { return m }

func (m Match) GetSourceLocation() *SourceLocation { return m.Loc }

func (m Match) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	exprType, err := m.Expr.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	if len(m.Cases) == 0 {
		return nil, fmt.Errorf("Match.Infer: no match cases")
	}

	var resultType hm.Type
	for i, case_ := range m.Cases {
		caseEnv := env.Clone()

		// TODO: Pattern matching type checking - for now just add pattern variables
		if varPattern, ok := case_.Pattern.(VariablePattern); ok {
			caseEnv.Add(varPattern.Name, hm.NewScheme(nil, exprType))
		}

		caseType, err := case_.Expr.Infer(caseEnv, fresh)
		if err != nil {
			return nil, err
		}

		if i == 0 {
			resultType = caseType
		} else {
			if _, err := UnifyWithCompatibility(resultType, caseType); err != nil {
				return nil, fmt.Errorf("Match.Infer: case %d type mismatch: %s != %s", i, resultType, caseType)
			}
		}
	}

	return resultType, nil
}

type MatchCase struct {
	Pattern Pattern
	Expr    Node
}

type Pattern interface{}

type WildcardPattern struct{}

type LiteralPattern struct {
	Value Node
}

type ConstructorPattern struct {
	Name string
	Args []Pattern
}

type VariablePattern struct {
	Name string
}

type Reassignment struct {
	Name  string
	Value Node
	Loc   *SourceLocation
}

var _ Node = Reassignment{}
var _ Evaluator = Reassignment{}

type CompoundAssignment struct {
	Name  string
	Op    string // "+" for +=, "-" for -=, etc.
	Value Node
	Loc   *SourceLocation
}

var _ Node = CompoundAssignment{}
var _ Evaluator = CompoundAssignment{}

func (r Reassignment) Body() hm.Expression { return r.Value }

func (r Reassignment) GetSourceLocation() *SourceLocation { return r.Loc }

func (r Reassignment) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Check that the variable exists in the environment
	scheme, found := env.SchemeOf(r.Name)
	if !found {
		return nil, fmt.Errorf("Reassignment.Infer: variable %q not found", r.Name)
	}

	// Get the existing type
	existingType, mono := scheme.Type()
	if !mono {
		return nil, fmt.Errorf("Reassignment.Infer: variable %q is not monomorphic", r.Name)
	}

	// Infer the type of the new value
	valueType, err := r.Value.Infer(env, fresh)
	if err != nil {
		return nil, fmt.Errorf("Reassignment.Infer: %w", err)
	}

	// Check that the types are compatible
	if _, err := UnifyWithCompatibility(existingType, valueType); err != nil {
		return nil, fmt.Errorf("Reassignment.Infer: cannot assign %s to variable %q of type %s: %w", valueType, r.Name, existingType, err)
	}

	// Reassignment returns the value type
	return valueType, nil
}

func (r Reassignment) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Check that the variable exists
	_, found := env.Get(r.Name)
	if !found {
		return nil, fmt.Errorf("Reassignment.Eval: variable %q not found", r.Name)
	}

	// Evaluate the new value
	newValue, err := EvalNode(ctx, env, r.Value)
	if err != nil {
		return nil, fmt.Errorf("Reassignment.Eval: evaluating value: %w", err)
	}

	// Update the variable in the environment
	env.Set(r.Name, newValue)

	// Return the new value
	return newValue, nil
}

func (c CompoundAssignment) Body() hm.Expression { return c.Value }

func (c CompoundAssignment) GetSourceLocation() *SourceLocation { return c.Loc }

func (c CompoundAssignment) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Check that the variable exists in the environment
	scheme, found := env.SchemeOf(c.Name)
	if !found {
		return nil, fmt.Errorf("CompoundAssignment.Infer: variable %q not found", c.Name)
	}

	// Get the existing type
	existingType, mono := scheme.Type()
	if !mono {
		return nil, fmt.Errorf("CompoundAssignment.Infer: variable %q is not monomorphic", c.Name)
	}

	// For += operator, check addition compatibility by attempting unification
	// This leverages the existing Addition type inference logic
	if c.Op == "+" {
		// Create a temporary Addition node to check type compatibility
		tempAddition := Addition{
			Left:  Symbol{Name: c.Name}, // Reference to existing variable
			Right: c.Value,              // Right-hand side value
		}

		// Try to infer the addition result type
		_, err := tempAddition.Infer(env, fresh)
		if err != nil {
			return nil, fmt.Errorf("CompoundAssignment.Infer: %w", err)
		}
	}

	// Compound assignment returns the existing variable type
	return existingType, nil
}

func (c CompoundAssignment) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// Check that the variable exists and get its current value
	currentValue, found := env.Get(c.Name)
	if !found {
		return nil, fmt.Errorf("CompoundAssignment.Eval: variable %q not found", c.Name)
	}

	// Evaluate the right-hand side value
	rightValue, err := EvalNode(ctx, env, c.Value)
	if err != nil {
		return nil, fmt.Errorf("CompoundAssignment.Eval: evaluating value: %w", err)
	}

	// Perform the compound operation
	var newValue Value
	if c.Op == "+" {
		// Use the same logic as Addition.Eval
		switch l := currentValue.(type) {
		case IntValue:
			if r, ok := rightValue.(IntValue); ok {
				newValue = IntValue{Val: l.Val + r.Val}
			} else {
				return nil, fmt.Errorf("CompoundAssignment.Eval: cannot add %T to int variable %q", rightValue, c.Name)
			}
		case StringValue:
			if r, ok := rightValue.(StringValue); ok {
				newValue = StringValue{Val: l.Val + r.Val}
			} else {
				return nil, fmt.Errorf("CompoundAssignment.Eval: cannot add %T to string variable %q", rightValue, c.Name)
			}
		case ListValue:
			if r, ok := rightValue.(ListValue); ok {
				// Concatenate the lists
				combined := make([]Value, len(l.Elements)+len(r.Elements))
				copy(combined, l.Elements)
				copy(combined[len(l.Elements):], r.Elements)

				// Use the element type from the left operand, or right if left is empty
				elemType := l.ElemType
				if len(l.Elements) == 0 && len(r.Elements) > 0 {
					elemType = r.ElemType
				}

				newValue = ListValue{Elements: combined, ElemType: elemType}
			} else {
				return nil, fmt.Errorf("CompoundAssignment.Eval: cannot add %T to list variable %q", rightValue, c.Name)
			}
		default:
			return nil, fmt.Errorf("CompoundAssignment.Eval: addition not supported for type %T", currentValue)
		}
	} else {
		return nil, fmt.Errorf("CompoundAssignment.Eval: unsupported operator %q", c.Op)
	}

	// Update the variable in the environment
	env.Set(c.Name, newValue)

	// Return the new value
	return newValue, nil
}

type Reopen struct {
	Name  string
	Block Block
	Loc   *SourceLocation
}

var _ Node = Reopen{}
var _ Evaluator = Reopen{}

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
	nonNullType, ok := termType.(NonNullType)
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
	// Evaluate the base term
	termValue, found := env.Get(r.Name)
	if !found {
		return nil, fmt.Errorf("Reopen.Eval: symbol %s not found", r.Name)
	}

	// The term must evaluate to a module that can be reopened
	moduleValue, ok := termValue.(ModuleValue)
	if !ok {
		return nil, fmt.Errorf("Reopen.Eval: cannot reopen %T, expected module", termValue)
	}

	// Create a new scope that inherits from the base module's values (copy-on-write using parent semantics)
	reopenedEnv := moduleValue.Clone()

	// Create a composite environment that allows access to both the reopened module and the current lexical environment
	// This enables access to local variables like function parameters while maintaining copy-on-write semantics
	compositeEnv := createCompositeEnv(reopenedEnv, env)

	// Evaluate the block in the composite scope
	for _, node := range r.Block.Forms {
		_, err := EvalNode(ctx, compositeEnv, node)
		if err != nil {
			return nil, fmt.Errorf("Reopen.Eval: evaluating block: %w", err)
		}
	}

	// Update the binding in the current env
	// Extract the primary environment from the composite (if used) or use reopenedEnv directly
	var val ModuleValue
	if compositeEnv, ok := compositeEnv.(CompositeEnv); ok {
		val = compositeEnv.primary.(ModuleValue)
	} else {
		val = reopenedEnv.(ModuleValue)
	}
	env.Set(r.Name, val)

	return val, nil
}

// CompositeEnv is a specialized environment for reopening that allows access to both
// the reopened module (for copy-on-write semantics) and the current lexical environment (for variable access)
type CompositeEnv struct {
	primary EvalEnv // Where new bindings go (the reopened module)
	lexical EvalEnv // Where to look for external variables (current environment)
}

func (c CompositeEnv) Get(name string) (Value, bool) {
	// First check the primary environment (reopened module)
	if val, found := c.primary.Get(name); found {
		return val, true
	}
	// Then check the lexical environment (current scope)
	return c.lexical.Get(name)
}

func (c CompositeEnv) Set(name string, value Value) EvalEnv {
	// All new bindings go to the primary environment (copy-on-write semantics)
	c.primary.Set(name, value)
	return c
}

func (c CompositeEnv) Clone() EvalEnv {
	// Clone the primary environment and keep the same lexical environment
	return CompositeEnv{
		primary: c.primary.Clone(),
		lexical: c.lexical,
	}
}

// createCompositeEnv creates a composite environment for reopening
func createCompositeEnv(reopenedEnv EvalEnv, currentEnv EvalEnv) EvalEnv {
	return CompositeEnv{
		primary: reopenedEnv,
		lexical: currentEnv,
	}
}

// CompositeModule combines two type environments for Reopen type inference
type CompositeModule struct {
	primary Env // The reopened module (where new bindings go)
	lexical Env // Current lexical scope (for variable lookups)
}

func (c *CompositeModule) SchemeOf(name string) (*hm.Scheme, bool) {
	// First check the primary environment (reopened module)
	if scheme, found := c.primary.SchemeOf(name); found {
		return scheme, true
	}
	// Then check the lexical environment (current scope)
	return c.lexical.SchemeOf(name)
}

func (c *CompositeModule) Clone() hm.Env {
	return &CompositeModule{
		primary: c.primary.Clone().(Env),
		lexical: c.lexical, // Keep same lexical environment
	}
}

func (c *CompositeModule) Add(name string, scheme *hm.Scheme) hm.Env {
	c.primary.Add(name, scheme)
	return c
}

func (c *CompositeModule) Remove(name string) hm.Env {
	c.primary.Remove(name)
	return c
}

func (c *CompositeModule) Apply(subs hm.Subs) hm.Substitutable {
	return &CompositeModule{
		primary: c.primary.Apply(subs).(Env),
		lexical: c.lexical.Apply(subs).(Env),
	}
}

func (c *CompositeModule) FreeTypeVar() hm.TypeVarSet {
	primaryVars := c.primary.FreeTypeVar()
	lexicalVars := c.lexical.FreeTypeVar()
	return primaryVars.Union(lexicalVars)
}

var _ Env = &CompositeModule{}

func (t *CompositeModule) Eq(other Type) bool                         { return other == t }
func (t *CompositeModule) Name() string                               { return t.primary.Name() }
func (t *CompositeModule) Normalize(k, v hm.TypeVarSet) (Type, error) { return t, nil }
func (t *CompositeModule) Types() hm.Types                            { return nil }
func (t *CompositeModule) String() string                             { return t.Name() }
func (t *CompositeModule) Format(s fmt.State, c rune)                 { fmt.Fprintf(s, "%s", t.Name()) }

// NamedType looks up class types, needed for NamedTypeNode.Infer compatibility
func (c *CompositeModule) NamedType(name string) (Env, bool) {
	// First check the primary environment (reopened module)
	if t, found := c.primary.NamedType(name); found {
		return t, true
	}
	// Then check the lexical environment (current scope)
	return c.lexical.NamedType(name)
}

// AddClass adds a class type, needed for ClassDecl compatibility
func (c *CompositeModule) AddClass(name string, class Env) {
	c.primary.AddClass(name, class)
}

type Assert struct {
	Message Node  // Optional message expression
	Block   Block // Block containing the assertion expression
	Loc     *SourceLocation
}

var _ Node = Assert{}
var _ Evaluator = Assert{}

func (a Assert) Body() hm.Expression { return a.Block }

func (a Assert) GetSourceLocation() *SourceLocation { return a.Loc }

func (a Assert) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Infer the block type - the assertion will be evaluated
	_, err := a.Block.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// Infer the message type if present
	if a.Message != nil {
		_, err := a.Message.Infer(env, fresh)
		if err != nil {
			return nil, err
		}
	}

	// Assert returns nothing (unit type / null)
	return hm.TypeVariable('a'), nil
}

func (a Assert) Eval(ctx context.Context, env EvalEnv) (Value, error) {
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

// isTruthy determines if a value should be considered true for assertion purposes
func isTruthy(val Value) bool {
	switch v := val.(type) {
	case BoolValue:
		return v.Val
	case NullValue:
		return false
	case IntValue:
		return v.Val != 0
	case StringValue:
		return v.Val != ""
	case ListValue:
		return len(v.Elements) > 0
	default:
		return true // Other values are considered truthy
	}
}
