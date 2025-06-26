package dash

import (
	"context"
	"fmt"

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

func (c FunCall) Body() hm.Expression { return c.Args }

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

			if _, err := hm.Unify(dt, it); err != nil {
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

			if _, err := hm.Unify(dt, it); err != nil {
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
		for i, argName := range fn.Args {
			if i < len(c.Args) {
				fnEnv.Set(argName, argValues[c.Args[i].Key])
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
		return nil, fmt.Errorf("FuncDecl.Infer: Form: %w", err)
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
	for i, arg := range f.Args {
		argNames[i] = arg.Named
	}

	// Create a function type for this declaration
	argTypes := make([]Keyed[*hm.Scheme], len(f.Args))
	for i, arg := range f.Args {
		// Use a placeholder type for now - in a full implementation we'd get this from type inference
		argTypes[i] = Keyed[*hm.Scheme]{Key: arg.Named, Value: hm.NewScheme(nil, hm.TypeVariable(byte('a'+i))), Positional: false}
	}

	// Create the function type
	fnType := hm.NewFnType(NewRecordType("", argTypes...), hm.TypeVariable('r'))

	return FunctionValue{
		Args:    argNames,
		Body:    f.Form,
		Closure: env,
		FnType:  fnType,
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
		// TODO: is this right?
		return NonNullType{ListType{f.Fresh()}}, nil
	}

	var t hm.Type
	for i, el := range l.Elements {
		et, err := el.Infer(env, f)
		if err != nil {
			return nil, err
		}
		if t == nil {
			t = et
		} else if _, err := hm.Unify(t, et); err != nil {
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
	Name string
	Loc  *SourceLocation
}

var _ Node = Symbol{}
var _ Evaluator = Symbol{}

func (s Symbol) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	scheme, found := env.SchemeOf(s.Name)
	if !found {
		return nil, fmt.Errorf("Symbol.Infer: %q not found in env", s.Name)
	}
	t, _ := scheme.Type()

	// Check if this is a zero-arity function and return its return type
	if ft, ok := t.(*hm.FunctionType); ok {
		if rt, ok := ft.Arg().(*RecordType); ok {
			// Check if it has zero fields (truly zero-arity) or only optional arguments
			if len(rt.Fields) == 0 {
				// This is a zero-arity function, return its return type
				return ft.Ret(false), nil
			}

			// Check if all fields are optional (no NonNullType fields)
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
				return ft.Ret(false), nil
			}
		}
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

	// Auto-call zero-arity functions when accessed as symbols
	if isZeroArityFunction(val) {
		return callZeroArityFunction(ctx, env, val)
	}

	return val, nil
}

// isZeroArityFunction checks if a value is a function with zero required arguments
func isZeroArityFunction(val Value) bool {
	switch fn := val.(type) {
	case FunctionValue:
		return len(fn.Args) == 0
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

	// Check if all arguments are optional (nullable)
	for _, arg := range field.Args {
		if arg.TypeRef.Kind == "NON_NULL" {
			// This argument is required (non-null)
			return false
		}
	}

	// All arguments are optional, so this function can be called with zero args
	return true
}

// callZeroArityFunction calls a zero-arity function with empty arguments
func callZeroArityFunction(ctx context.Context, env EvalEnv, val Value) (Value, error) {
	emptyArgs := make(map[string]Value)

	switch fn := val.(type) {
	case FunctionValue:
		// Regular function call with empty environment
		return EvalNode(ctx, fn.Closure, fn.Body)

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
		return t, err
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
	rec, ok := nn.Type.(*Module)
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

	switch rec := receiverVal.(type) {
	case RecordValue:
		if val, found := rec.Fields[d.Field]; found {
			return val, nil
		}
		err := fmt.Errorf("Select.Eval: field %q not found in record", d.Field)
		return nil, CreateEvalError(ctx, err, d)

	case ModuleValue:
		// For module selection, we would typically return a function that represents the API call
		// For now, return a placeholder
		if val, found := rec.Values[d.Field]; found {
			// If this is a function call (Args present), call the function
			return val, nil
		}
		// Return a placeholder function for Dagger API calls
		return ModuleValue{
			Mod:    rec.Mod,
			Values: map[string]Value{d.Field: StringValue{Val: fmt.Sprintf("dagger.%s.%s", rec.Mod.Named, d.Field)}},
		}, nil

	case GraphQLValue:
		// Handle GraphQL field selection
		return rec.SelectField(ctx, d.Field)

	default:
		err := fmt.Errorf("Select.Eval: cannot select field %q from %T (value: %q). Expected a record or module value, but got %T", d.Field, receiverVal, receiverVal.String(), receiverVal)
		return nil, CreateEvalError(ctx, err, d)
	}
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
	lt = NonNullType{lt}
	if !lt.Eq(rt) {
		return nil, fmt.Errorf("Default.Infer: mismatched types: %s != %s", lt, rt)
	}
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

	if _, err := hm.Unify(condType, boolType); err != nil {
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

		if _, err := hm.Unify(thenType, elseType); err != nil {
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
	Args []string
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
		argType := fresh.Fresh()
		argTypes[i] = Keyed[*hm.Scheme]{Key: arg, Value: hm.NewScheme(nil, argType), Positional: false}
		newEnv.Add(arg, hm.NewScheme(nil, argType))
	}

	bodyType, err := l.Expr.Infer(newEnv, fresh)
	if err != nil {
		return nil, err
	}

	return hm.NewFnType(NewRecordType("", argTypes...), bodyType), nil
}

func (l Lambda) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	// For now, create a simple function type signature
	// In a full implementation, we'd need to properly infer the function type
	argTypes := make([]Keyed[*hm.Scheme], len(l.Args))
	for i, arg := range l.Args {
		// Use a placeholder type for now
		argTypes[i] = Keyed[*hm.Scheme]{Key: arg, Value: hm.NewScheme(nil, hm.TypeVariable(byte('a'+i))), Positional: false}
	}

	// Create a function type with placeholder return type
	fnType := hm.NewFnType(NewRecordType("", argTypes...), hm.TypeVariable('r'))

	return FunctionValue{
		Args:    l.Args,
		Body:    l.Expr,
		Closure: env,
		FnType:  fnType,
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
			if _, err := hm.Unify(resultType, caseType); err != nil {
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
