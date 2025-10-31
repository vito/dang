package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"sort"

	"dagger.io/dagger"
	"dagger.io/dagger/dag"
	"dagger.io/dagger/telemetry"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vito/dang/pkg/hm"

	"github.com/vito/dang/introspection"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/ioctx"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
)

const debug = false

const introspectionJSON = "/introspection.json"

func main() {
	ctx := context.Background()
	ctx = ioctx.StdoutToContext(ctx, os.Stdout)
	ctx = ioctx.StderrToContext(ctx, os.Stderr)

	ctx = telemetry.InitEmbedded(ctx, resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String("dagger-dang-sdk"),
	))
	defer telemetry.Close()

	dag, err := dagger.Connect(ctx)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}

	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))

	schema, err := Introspect(ctx, dag)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}

	fnCall := dag.CurrentFunctionCall()
	parentName, err := fnCall.ParentName(ctx)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}
	fnName, err := fnCall.Name(ctx)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}
	parentJson, err := fnCall.Parent(ctx)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}
	fnArgs, err := fnCall.InputArgs(ctx)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}

	inputArgs := make(map[string][]byte)
	for _, fnArg := range fnArgs {
		argName, err := fnArg.Name(ctx)
		if err != nil {
			WriteError(ctx, err)
			os.Exit(2)
		}
		argValue, err := fnArg.Value(ctx)
		if err != nil {
			WriteError(ctx, err)
			os.Exit(2)
		}
		inputArgs[argName] = []byte(argValue)
	}

	slog.Debug("invoking", "parentName", parentName, "fnName", fnName)

	modSrcDir := os.Args[1]

	err = invoke(ctx, dag, schema, modSrcDir, []byte(parentJson), parentName, fnName, inputArgs)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}
}

func invoke(ctx context.Context, dag *dagger.Client, schema *introspection.Schema, modSrcDir string, parentJSON []byte, parentName string, fnName string, inputArgs map[string][]byte) (rerr error) {
	fnCall := dag.CurrentFunctionCall()
	defer func() {
		if rerr != nil {
			if err := fnCall.ReturnError(ctx, convertError(rerr)); err != nil {
				fmt.Println("failed to return error:", err, "\noriginal error:", rerr)
			}
		}
	}()

	ctx = ioctx.StdoutToContext(ctx, os.Stdout)
	ctx = ioctx.StderrToContext(ctx, os.Stderr)

	env, err := dang.RunDir(ctx, dag.GraphQLClient(), schema, modSrcDir, debug)
	if err != nil {
		return err
	}

	dagMod := dag.Module()
	if desc, found := env.Get("description"); found {
		dagMod = dagMod.WithDescription(desc.String())
	}

	// initializing module
	if parentName == "" {
		dagMod, err := initModule(dag, env)
		if err != nil {
			return fmt.Errorf("failed to init module: %w", err)
		}
		jsonBytes, err := json.Marshal(dagMod)
		if err != nil {
			return fmt.Errorf("failed to marshal module: %w", err)
		}
		return fnCall.ReturnValue(ctx, dagger.JSON(jsonBytes))
	}

	parentModBase, found := env.Get(parentName)
	if !found {
		return fmt.Errorf("unknown parent type: %s", parentName)
	}
	var parentState map[string]any
	dec := json.NewDecoder(bytes.NewReader(parentJSON))
	dec.UseNumber()
	if err := dec.Decode(&parentState); err != nil {
		return fmt.Errorf("failed to unmarshal parent JSON: %w", err)
	}

	parentConstructor := parentModBase.(*dang.ConstructorFunction)
	parentModType := parentConstructor.ClassType

	var fnType *hm.FunctionType

	if fnName == "" {
		fnType = parentConstructor.FnType
	} else {
		fnScheme, found := parentModType.SchemeOf(fnName)
		if !found {
			return fmt.Errorf("unknown function: %s", fnName)
		}
		t, mono := fnScheme.Type()
		if !mono {
			return fmt.Errorf("non-monotype function %s", fnName)
		}
		var ok bool
		fnType, ok = t.(*hm.FunctionType)
		if !ok {
			return fmt.Errorf("expected function type, got %T", fnScheme)
		}
	}

	var args dang.Record
	argMap := make(map[string]dang.Value, len(args))
	for _, arg := range fnType.Arg().(*dang.RecordType).Fields {
		argType, mono := arg.Value.Type()
		if !mono {
			return fmt.Errorf("non-monotype argument %s", arg.Key)
		}
		jsonValue, provided := inputArgs[arg.Key]
		if !provided {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(jsonValue))
		dec.UseNumber()
		var val any
		if err := dec.Decode(&val); err != nil {
			return fmt.Errorf("failed to unmarshal input argument %s: %w", arg.Key, err)
		}
		dangVal, err := anyToDang(ctx, env, val, argType)
		if err != nil {
			return fmt.Errorf("failed to convert input argument %s to dang value: %w", arg.Key, err)
		}
		argMap[arg.Key] = dangVal
		args = append(args, dang.Keyed[dang.Node]{
			Key:   arg.Key,
			Value: &dang.ValueNode{Val: dangVal},
		})
	}

	var result dang.Value
	if fnName == "" {
		result, err = parentConstructor.Call(ctx, env, argMap)
		if err != nil {
			return fmt.Errorf("failed to call parent constructor: %w", err)
		}
	} else {
		parentModEnv := dang.NewModuleValue(parentModType)
		parentModEnv.Set("self", parentModEnv)

		for name, value := range parentState {
			scheme, found := parentModType.SchemeOf(name)
			if !found {
				return fmt.Errorf("unknown field: %s", name)
			}
			fieldType, isMono := scheme.Type()
			if !isMono {
				return fmt.Errorf("non-monotype argument %s", name)
			}
			dangVal, err := anyToDang(ctx, env, value, fieldType)
			if err != nil {
				return fmt.Errorf("failed to convert parent state %s to dang value: %w", name, err)
			}
			parentModEnv.Set(name, dangVal)
		}

		bodyEnv := dang.CreateCompositeEnv(parentModEnv, env)
		_, err := dang.EvaluateFormsWithPhases(ctx, parentConstructor.ClassBodyForms, bodyEnv)
		if err != nil {
			return fmt.Errorf("evaluating class body for %s: %w", parentConstructor.ClassName, err)
		}

		call := &dang.FunCall{
			Fun: &dang.Select{
				Receiver: &dang.ValueNode{Val: parentModEnv},
				Field:    fnName,
			},
			Args: args,
		}
		result, err = call.Eval(ctx, env)
		if err != nil {
			return fmt.Errorf("failed to evaluate call: %w", err)
		}
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}
	return fnCall.ReturnValue(ctx, dagger.JSON(jsonBytes))
}

func anyToDang(ctx context.Context, env dang.EvalEnv, val any, fieldType hm.Type) (dang.Value, error) {
	if nonNull, ok := fieldType.(hm.NonNullType); ok {
		return anyToDang(ctx, env, val, nonNull.Type)
	}
	switch v := val.(type) {
	case string:
		if modType, ok := fieldType.(*dang.Module); ok && modType != dang.StringType {
			// Check if this is an enum type
			if modType.Kind == dang.EnumKind {
				// It's an enum - return the enum value
				if enumVal, found := env.Get(modType.Named); found {
					if enumMod, ok := enumVal.(*dang.ModuleValue); ok {
						if val, found := enumMod.Get(v); found {
							return val, nil
						}
						return nil, fmt.Errorf("unknown enum value %s.%s", modType.Named, v)
					}
				}
				return nil, fmt.Errorf("enum type %s not found in environment", modType.Named)
			}

			// Check if this is a scalar type
			if modType.Kind == dang.ScalarKind {
				// It's a scalar - return a ScalarValue
				return dang.ScalarValue{Val: v, ScalarType: modType}, nil
			}

			// Otherwise, assume it's an object ID
			sel := dang.FunCall{
				Fun: &dang.Select{
					Field: fmt.Sprintf("load%sFromID", modType.Named),
				},
				Args: dang.Record{
					dang.Keyed[dang.Node]{
						Key:   "id",
						Value: &dang.String{Value: v},
					},
				},
			}
			return sel.Eval(ctx, env)
		}
		return dang.StringValue{Val: v}, nil
	case int:
		return dang.IntValue{Val: v}, nil
	case json.Number:
		// if strings.Contains(v.String(), ".") {
		// 	return dang.FloatValue{Val: v.Float64()}, nil
		// }
		i, err := v.Int64()
		if err != nil {
			return nil, fmt.Errorf("failed to convert json.Number to int64: %w", err)
		}
		return dang.IntValue{Val: int(i)}, nil
	case bool:
		return dang.BoolValue{Val: v}, nil
	case []any:
		listT, isList := fieldType.(dang.ListType)
		if !isList {
			return nil, fmt.Errorf("expected list type, got %T", fieldType)
		}
		vals := dang.ListValue{
			ElemType: listT,
		}
		for _, item := range v {
			val, err := anyToDang(ctx, env, item, listT.Type)
			if err != nil {
				return nil, fmt.Errorf("failed to convert list item: %w", err)
			}
			vals.Elements = append(vals.Elements, val)
		}
		return vals, nil
	case map[string]any:
		mod, isMod := fieldType.(dang.Env)
		if !isMod {
			return nil, fmt.Errorf("expected module type, got %T", fieldType)
		}
		modVal := dang.NewModuleValue(mod)
		for name, val := range v {
			expectedT, found := mod.SchemeOf(name)
			if !found {
				return nil, fmt.Errorf("module %q does not have a scheme for %q", mod.Name(), name)
			}
			t, isMono := expectedT.Type()
			if !isMono {
				return nil, fmt.Errorf("expected monomorphic type, got %T", t)
			}
			dangVal, err := anyToDang(ctx, env, val, t)
			if err != nil {
				return nil, fmt.Errorf("failed to convert map item %q: %w", name, err)
			}
			mod.Add(name, hm.NewScheme(nil, dangVal.Type()))
			modVal.Set(name, dangVal)
		}
		return modVal, nil
	case nil:
		return dang.NullValue{}, nil
	default:
		return nil, fmt.Errorf("unsupported type %T", val)
	}
}

func initModule(dag *dagger.Client, env dang.EvalEnv) (*dagger.Module, error) {
	dagMod := dag.Module()

	// Handle module-level description if present
	if descBinding, found := env.Get("description"); found {
		dagMod = dagMod.WithDescription(descBinding.String())
	}

	binds := env.Bindings(dang.PublicVisibility)
	for _, binding := range binds {
		log.Println("Binding:", binding.Key)
		switch val := binding.Value.(type) {
		case *dang.ConstructorFunction:
			// Classes/objects - register as TypeDefs with their methods
			objDef, err := createObjectTypeDef(dag, binding.Key, val, env)
			if err != nil {
				return nil, fmt.Errorf("failed to create object %s: %w", binding.Key, err)
			}
			directives := ProcessedDirectives{}
			for _, slot := range val.Parameters {
				for _, dir := range slot.Directives {
					if directives[slot.Name.Name] == nil {
						directives[slot.Name.Name] = map[string]map[string]any{}
					}
					for _, arg := range dir.Args {
						if directives[slot.Name.Name][dir.Name] == nil {
							directives[slot.Name.Name][dir.Name] = map[string]any{}
						}
						val, err := evalConstantValue(arg.Value)
						if err != nil {
							return nil, fmt.Errorf("failed to evaluate directive argument %s.%s.%s: %w", slot.Name.Name, dir.Name, arg.Key, err)
						}
						directives[slot.Name.Name][dir.Name][arg.Key] = val
					}
				}
			}
			fnDef, err := createFunction(dag, binding.Key, val.FnType, directives, env)
			if err != nil {
				return nil, fmt.Errorf("failed to create constructor for %s: %w", binding.Key, err)
			}
			objDef = objDef.WithConstructor(fnDef)

			dagMod = dagMod.WithObject(objDef)

		case *dang.ModuleValue:
			// Check if this is an enum by checking its kind
			if mod, ok := val.Mod.(*dang.Module); ok && mod.Kind == dang.EnumKind {
				enumDef, err := createEnumTypeDef(dag, binding.Key, val)
				if err != nil {
					return nil, fmt.Errorf("failed to create enum %s: %w", binding.Key, err)
				}
				dagMod = dagMod.WithEnum(enumDef)
			} else if mod, ok := val.Mod.(*dang.Module); ok && mod.Kind == dang.ScalarKind {
				// Scalars are registered with the module, but we don't need to create TypeDefs for them
				// They're already handled as basic string types in dangTypeToTypeDef
				slog.Info("skipping scalar module value (handled as string type)", "name", binding.Key)
			} else {
				slog.Info("skipping non-enum module value", "name", binding.Key)
			}

		default:
			// Other values (functions, constants, etc.) - for now skip
			// In the Dagger SDK, everything needs to be structured as objects
			slog.Info("skipping non-class public binding", "name", binding.Key, "type", fmt.Sprintf("%T", val))
		}
	}

	return dagMod, nil
}

// arg => directive => directive args
type ProcessedDirectives = map[string]map[string]map[string]any

func createFunction(dag *dagger.Client, name string, fn *hm.FunctionType, directives ProcessedDirectives, env dang.EvalEnv) (*dagger.Function, error) {
	// Convert Dang function type to Dagger TypeDef
	retTypeDef, err := dangTypeToTypeDef(dag, fn.Ret(false), env)
	if err != nil {
		return nil, fmt.Errorf("failed to convert return type for %s: %w", fn, err)
	}

	funDef := dag.Function(name, retTypeDef)

	for _, arg := range fn.Arg().(*dang.RecordType).Fields {
		argType, mono := arg.Value.Type()
		if !mono {
			return nil, fmt.Errorf("non-monotype argument %s", arg.Key)
		}
		typeDef, err := dangTypeToTypeDef(dag, argType, env)
		if err != nil {
			return nil, fmt.Errorf("failed to convert argument type for %s: %w", arg.Key, err)
		}

		argOpts := dagger.FunctionWithArgOpts{}
		if _, isNonNull := argType.(hm.NonNullType); !isNonNull {
			typeDef = typeDef.WithOptional(true)
		}

		// Check for directives on this argument using processed directives
		if argDirectives, hasDirectives := directives[arg.Key]; hasDirectives {
			if defaultPath, hasDefaultPath := argDirectives["defaultPath"]; hasDefaultPath {
				if path, ok := defaultPath["path"].(string); ok {
					argOpts.DefaultPath = path
				}
			}
			if ignorePatterns, hasIgnorePatterns := argDirectives["ignorePatterns"]; hasIgnorePatterns {
				ignore, hasIgnore := ignorePatterns["patterns"]
				if ignore, ok := ignore.([]any); ok {
					var ignorePatterns []string
					for _, pattern := range ignore {
						if str, ok := pattern.(string); ok {
							ignorePatterns = append(ignorePatterns, str)
						} else {
							return nil, fmt.Errorf("invalid ignore argument %s: %T (expected string)", arg.Key, pattern)
						}
					}
					if len(ignorePatterns) > 0 {
						argOpts.Ignore = ignorePatterns
					}
				} else if hasIgnore {
					return nil, fmt.Errorf("invalid ignore directive for argument %s: %T (expected []any)", arg.Key, ignore)
				}
			}
		}

		// TODO: eval default?
		// if def, hasDefault := fn.Defaults[arg.Key]; hasDefault {
		// 	js, err := json.Marshal(def)
		// 	if err != nil {
		// 		return nil, fmt.Errorf("failed to marshal default value for %s: %w", arg.Key, err)
		// 	}
		// 	argOpts.DefaultValue = js
		// }
		funDef = funDef.WithArg(arg.Key, typeDef, argOpts)
	}

	return funDef, nil
}

// evalConstantValue converts AST nodes to Go values for directive arguments
func evalConstantValue(node dang.Node) (any, error) {
	switch n := node.(type) {
	case *dang.String:
		return n.Value, nil
	case *dang.Int:
		return n.Value, nil
	case *dang.Boolean:
		return n.Value, nil
	case *dang.List:
		var elements []any
		for _, elem := range n.Elements {
			if evalElem, err := evalConstantValue(elem); err == nil {
				elements = append(elements, evalElem)
			} else {
				return nil, fmt.Errorf("failed to evaluate list element: %w", err)
			}
		}
		return elements, nil
	default:
		// For more complex nodes, we could try full evaluation
		// but for now, directive arguments should be simple literals
		return nil, fmt.Errorf("unsupported directive argument type: %T", node)
	}
}

func createObjectTypeDef(dag *dagger.Client, name string, module *dang.ConstructorFunction, env dang.EvalEnv) (*dagger.TypeDef, error) {
	objDef := dag.TypeDef().WithObject(name)

	// Process public methods in the class
	for name, scheme := range module.ClassType.Bindings(dang.PublicVisibility) {
		slotType, isMono := scheme.Type()
		if !isMono {
			return nil, fmt.Errorf("non-monotype method %s", name)
		}
		switch x := slotType.(type) {
		case *hm.FunctionType:
			fn := x
			// TODO: figure out the directives locally
			fnDef, err := createFunction(dag, name, fn, nil, env)
			if err != nil {
				return nil, fmt.Errorf("failed to create method %s for %s: %w", name, name, err)
			}
			if desc, ok := module.ClassType.GetDocString(name); ok {
				fnDef = fnDef.WithDescription(desc)
			}
			objDef = objDef.WithFunction(fnDef)
		default:
			fieldDef, err := dangTypeToTypeDef(dag, slotType, env)
			if err != nil {
				return nil, fmt.Errorf("failed to create field %s: %w", name, err)
			}
			opts := dagger.TypeDefWithFieldOpts{}
			if desc, ok := module.ClassType.GetDocString(name); ok {
				opts.Description = desc
			}
			objDef = objDef.WithField(name, fieldDef, opts)
		}
	}

	return objDef, nil
}

// createEnumTypeDef creates a Dagger enum TypeDef from a Dang enum ModuleValue
func createEnumTypeDef(dag *dagger.Client, name string, enumMod *dang.ModuleValue) (*dagger.TypeDef, error) {
	enumDef := dag.TypeDef().WithEnum(name)

	// Add each enum value as a member
	for memberName := range enumMod.Values {
		enumDef = enumDef.WithEnumMember(memberName)
	}

	return enumDef, nil
}

func dangTypeToTypeDef(dag *dagger.Client, dangType hm.Type, env dang.EvalEnv) (ret *dagger.TypeDef, rerr error) {
	def := dag.TypeDef()

	if nonNull, isNonNull := dangType.(hm.NonNullType); isNonNull {
		// Handle non-null wrapper
		sub, err := dangTypeToTypeDef(dag, nonNull.Type, env)
		if err != nil {
			return nil, fmt.Errorf("failed to convert non-null type: %w", err)
		}
		return sub.WithOptional(false), nil
	} else {
		def = def.WithOptional(true)
	}

	switch t := dangType.(type) {
	case dang.ListType:
		elemTypeDef, err := dangTypeToTypeDef(dag, t.Type, env)
		if err != nil {
			return nil, fmt.Errorf("failed to convert list element type: %w", err)
		}
		return def.WithListOf(elemTypeDef), nil

	case *dang.Module:
		// Check for basic types and object/class types
		switch t.Named {
		case "String":
			return def.WithKind(dagger.TypeDefKindStringKind), nil
		case "Int":
			return def.WithKind(dagger.TypeDefKindIntegerKind), nil
		case "Boolean":
			return def.WithKind(dagger.TypeDefKindBooleanKind), nil
		case "Void":
			return def.WithKind(dagger.TypeDefKindVoidKind), nil
		case "":
			// ad-hoc object type like {{foo: 1}}
			return nil, fmt.Errorf("cannot directly expose ad-hoc object type: %s", t)
		default:
			// Check if this is an enum by looking up the value in the environment
			if val, found := env.Get(t.Named); found {
				if modVal, ok := val.(*dang.ModuleValue); ok {
					if mod, ok := modVal.Mod.(*dang.Module); ok && mod.Kind == dang.EnumKind {
						// It's an enum type - just reference it by name
						// The enum TypeDef is already registered in the module
						return def.WithEnum(t.Named), nil
					}
					if mod, ok := modVal.Mod.(*dang.Module); ok && mod.Kind == dang.ScalarKind {
						// Scalars are exposed as strings in the Dagger SDK
						// TODO: revise if/when Dagger supports custom scalars?
						return def.WithKind(dagger.TypeDefKindStringKind), nil
					}
				}
			}
			// assume object (TODO?)
			return def.WithObject(t.Named), nil
		}

	default:
		// For type variables and other complex types, default to string for now
		// TODO: Handle type variables more gracefully
		slog.Info("unknown type, defaulting to string", "type", fmt.Sprintf("%T", dangType), "value", fmt.Sprintf("%s", dangType))
		return nil, fmt.Errorf("unknown type: %T: %s", dangType, dangType)
	}
}

func Introspect(ctx context.Context, dag *dagger.Client) (*introspection.Schema, error) {
	introspectionJSONBytes, err := os.ReadFile(introspectionJSON)
	if err != nil {
		return nil, fmt.Errorf("introspection query: %w", err)
	}

	var response struct {
		Schema *introspection.Schema `json:"__schema"`
	}
	if err := json.Unmarshal(introspectionJSONBytes, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal introspection JSON: %w", err)
	}
	return response.Schema, nil
}

func WriteError(ctx context.Context, err error) {
	if err != nil {
		fmt.Println(err)
	}
}

// func initPlatform(ctx context.Context, dag *dagger.Client, scope *bass.Scope) error {
// 	// Set the default OCI platform as *platform*.
// 	platStr, err := dag.DefaultPlatform(ctx)
// 	if err != nil {
// 		return fmt.Errorf("failed to get default platform: %w", err)
// 	}
// 	scope.Set("*platform*", bass.String(platStr))

// 	// Set the non-OS portion of the OCI platform as *arch* so that we include v7
// 	// in arm/v7.
// 	_, arch, _ := strings.Cut(string(platStr), "/")
// 	scope.Set("*arch*", bass.String(arch))

// 	return nil
// }

func convertError(rerr error) *dagger.Error {
	var gqlErr *gqlerror.Error
	if errors.As(rerr, &gqlErr) {
		dagErr := dag.Error(gqlErr.Message)
		if gqlErr.Extensions != nil {
			keys := make([]string, 0, len(gqlErr.Extensions))
			for k := range gqlErr.Extensions {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				val, err := json.Marshal(gqlErr.Extensions[k])
				if err != nil {
					fmt.Println("failed to marshal error value:", err)
				}
				dagErr = dagErr.WithValue(k, dagger.JSON(val))
			}
		}
		return dagErr
	}
	return dag.Error(rerr.Error())
}
