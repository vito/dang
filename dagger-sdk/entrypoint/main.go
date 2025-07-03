package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/chewxy/hm"

	"github.com/vito/dash/introspection"
	"github.com/vito/dash/pkg/dash"
	"github.com/vito/dash/pkg/ioctx"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
)

const debug = false

const introspectionJSON = "/introspection.json"

func main() {
	ctx := context.Background()

	ctx = telemetry.InitEmbedded(ctx, resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String("dagger-dash-sdk"),
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
	modName := os.Args[2]

	err = invoke(ctx, dag, schema, modSrcDir, modName, []byte(parentJson), parentName, fnName, inputArgs)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}
}

func invoke(ctx context.Context, dag *dagger.Client, schema *introspection.Schema, modSrcDir string, modName string, parentJSON []byte, parentName string, fnName string, inputArgs map[string][]byte) error {
	execCtx := ioctx.StdoutToContext(ctx, os.Stdout)
	execCtx = ioctx.StderrToContext(ctx, os.Stderr)
	env, err := dash.RunDir(execCtx, dag.GraphQLClient(), schema, modSrcDir, debug)
	if err != nil {
		return fmt.Errorf("failed to run dir: %w", err)
	}

	// camelModName := strcase.ToCamel(modName)

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
		return dag.CurrentFunctionCall().ReturnValue(ctx, dagger.JSON(jsonBytes))
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
	parentModEnv := parentModBase.(dash.EvalEnv).Clone()
	parentModType := parentModEnv.(*dash.ModuleValue).Mod
	for name, value := range parentState {
		scheme, found := parentModType.SchemeOf(name)
		if !found {
			return fmt.Errorf("unknown field: %s", name)
		}
		fieldType, isMono := scheme.Type()
		if !isMono {
			return fmt.Errorf("non-monotype argument %s", name)
		}
		dashVal, err := anyToDash(ctx, env, value, fieldType)
		if err != nil {
			return fmt.Errorf("failed to convert input argument %s to dash value: %w", name, err)
		}
		parentModEnv.Set(name, dashVal)
	}
	if fnName == "" {
		fnName = "new"
	}
	fnValue, found := parentModEnv.Get(fnName)
	if !found {
		return fmt.Errorf("unknown function: %s", fnName)
	}

	call := dash.Select{
		Receiver: dash.ValueNode{Val: parentModEnv.(*dash.ModuleValue)},
		Field:    fnName,
	}
	var args dash.Record
	fn := fnValue.(dash.FunctionValue)
	for _, arg := range fn.FnType.Arg().(*dash.RecordType).Fields {
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
		dashVal, err := anyToDash(ctx, env, val, argType)
		if err != nil {
			return fmt.Errorf("failed to convert input argument %s to dash value: %w", arg.Key, err)
		}
		args = append(args, dash.Keyed[dash.Node]{
			Key:   arg.Key,
			Value: dash.ValueNode{Val: dashVal},
		})
	}
	call.Args = &args
	result, err := call.Eval(ctx, env)
	if err != nil {
		return fmt.Errorf("failed to evaluate call: %w", err)
	}
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}
	return dag.CurrentFunctionCall().ReturnValue(ctx, dagger.JSON(jsonBytes))
}

func anyToDash(ctx context.Context, env dash.EvalEnv, val any, fieldType hm.Type) (dash.Value, error) {
	if nonNull, ok := fieldType.(dash.NonNullType); ok {
		return anyToDash(ctx, env, val, nonNull.Type)
	}
	switch v := val.(type) {
	case string:
		if modType, ok := fieldType.(*dash.Module); ok && modType != dash.StringType {
			sel := dash.Select{
				Field: fmt.Sprintf("load%sFromID", modType.Named),
				Args: &dash.Record{
					dash.Keyed[dash.Node]{
						Key:   "id",
						Value: dash.String{Value: v},
					},
				},
			}
			return sel.Eval(ctx, env)
		}
		return dash.StringValue{Val: v}, nil
	case int:
		return dash.IntValue{Val: v}, nil
	case json.Number:
		// if strings.Contains(v.String(), ".") {
		// 	return dash.FloatValue{Val: v.Float64()}, nil
		// }
		i, err := v.Int64()
		if err != nil {
			return nil, fmt.Errorf("failed to convert json.Number to int64: %w", err)
		}
		return dash.IntValue{Val: int(i)}, nil
	case bool:
		return dash.BoolValue{Val: v}, nil
	case []any:
		listT, isList := fieldType.(dash.ListType)
		if !isList {
			return nil, fmt.Errorf("expected list type, got %T", fieldType)
		}
		vals := dash.ListValue{
			ElemType: listT,
		}
		for _, item := range v {
			val, err := anyToDash(ctx, env, item, listT.Type)
			if err != nil {
				return nil, fmt.Errorf("failed to convert list item: %w", err)
			}
			vals.Elements = append(vals.Elements, val)
		}
		return vals, nil

	default:
		return nil, fmt.Errorf("unsupported type %T", v)
	}
}

func initModule(dag *dagger.Client, env dash.EvalEnv) (*dagger.Module, error) {
	dagMod := dag.Module()

	// Handle module-level description if present
	if descBinding, found := env.Get("description"); found {
		dagMod = dagMod.WithDescription(descBinding.String())
	}

	binds := env.Bindings(dash.PublicVisibility)
	for _, binding := range binds {
		log.Println("Binding:", binding.Key)
		switch val := binding.Value.(type) {
		case *dash.ModuleValue:
			// Classes/objects - register as TypeDefs with their methods
			objDef, err := createObjectTypeDef(dag, binding.Key, val)
			if err != nil {
				return nil, fmt.Errorf("failed to create object %s: %w", binding.Key, err)
			}
			dagMod = dagMod.WithObject(objDef)

		default:
			// Other values (functions, constants, etc.) - for now skip
			// In the Dagger SDK, everything needs to be structured as objects
			slog.Info("skipping non-class public binding", "name", binding.Key, "type", fmt.Sprintf("%T", val))
		}
	}

	return dagMod, nil
}

func createFunction(dag *dagger.Client, name string, fn dash.FunctionValue) (*dagger.Function, error) {
	// Convert Dash function type to Dagger TypeDef
	retTypeDef, err := dashTypeToTypeDef(dag, fn.FnType.Ret(false))
	if err != nil {
		return nil, fmt.Errorf("failed to convert return type for %s: %w", fn.FnType, err)
	}

	funDef := dag.Function(name, retTypeDef)

	for _, arg := range fn.FnType.Arg().(*dash.RecordType).Fields {
		argType, mono := arg.Value.Type()
		if !mono {
			return nil, fmt.Errorf("non-monotype argument %s", arg.Key)
		}
		typeDef, err := dashTypeToTypeDef(dag, argType)
		if err != nil {
			return nil, fmt.Errorf("failed to convert argument type for %s: %w", arg.Key, err)
		}
		argOpts := dagger.FunctionWithArgOpts{}
		if _, isNonNull := argType.(dash.NonNullType); !isNonNull {
			typeDef = typeDef.WithOptional(true)
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

func createObjectTypeDef(dag *dagger.Client, name string, module *dash.ModuleValue) (*dagger.TypeDef, error) {
	objDef := dag.TypeDef().WithObject(name)

	// Process public methods in the class
	for _, binding := range module.Bindings(dash.PublicVisibility) {
		scheme, found := module.Mod.SchemeOf(binding.Key)
		if !found {
			return nil, fmt.Errorf("failed to find type scheme for %s", binding.Key)
		}
		slotType, isMono := scheme.Type()
		if !isMono {
			return nil, fmt.Errorf("non-monotype method %s", binding.Key)
		}
		switch x := binding.Value.(type) {
		case dash.FunctionValue:
			fn := x
			fnDef, err := createFunction(dag, binding.Key, fn)
			if err != nil {
				return nil, fmt.Errorf("failed to create method %s for %s: %w", binding.Key, name, err)
			}

			if binding.Key == "new" {
				// Constructor function
				objDef = objDef.WithConstructor(fnDef)
			} else {
				// Regular method
				objDef = objDef.WithFunction(fnDef)
			}
		default:
			fieldDef, err := dashTypeToTypeDef(dag, slotType)
			if err != nil {
				return nil, fmt.Errorf("failed to create field %s: %w", binding.Key, err)
			}
			objDef = objDef.WithField(binding.Key, fieldDef)
		}
	}

	return objDef, nil
}

func dashTypeToTypeDef(dag *dagger.Client, dashType hm.Type) (*dagger.TypeDef, error) {
	def := dag.TypeDef()

	switch t := dashType.(type) {
	case dash.NonNullType:
		// Handle non-null wrapper
		return dashTypeToTypeDef(dag, t.Type)

	case dash.ListType:
		elemTypeDef, err := dashTypeToTypeDef(dag, t.Type)
		if err != nil {
			return nil, fmt.Errorf("failed to convert list element type: %w", err)
		}
		return def.WithListOf(elemTypeDef), nil

	case *dash.Module:
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
			// assume object (TODO?)
			return def.WithObject(t.Named), nil
		}

	default:
		// For type variables and other complex types, default to string for now
		// TODO: Handle type variables more gracefully
		slog.Info("unknown type, defaulting to string", "type", fmt.Sprintf("%T", dashType), "value", fmt.Sprintf("%s", dashType))
		return nil, fmt.Errorf("unknown type: %T: %s", dashType, dashType)
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
