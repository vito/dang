package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"

	"github.com/iancoleman/strcase"
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

	result, err := invoke(ctx, dag, schema, modSrcDir, modName, []byte(parentJson), parentName, fnName, inputArgs)
	if err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		WriteError(ctx, fmt.Errorf("failed to marshal result: %w", err))
		os.Exit(2)
	}

	slog.Debug("returning", "result", string(resultBytes))

	if err := fnCall.ReturnValue(ctx, dagger.JSON(resultBytes)); err != nil {
		WriteError(ctx, err)
		os.Exit(2)
	}
}

func invoke(ctx context.Context, dag *dagger.Client, schema *introspection.Schema, modSrcDir string, modName string, parentJSON []byte, parentName string, fnName string, inputArgs map[string][]byte) (any, error) {
	tmpfile, err := os.CreateTemp("", "dash-sdk-*.dash")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpfile.Name())

	var script strings.Builder
	camelModName := strcase.ToCamel(modName)

	// Import the module being executed
	script.WriteString(fmt.Sprintf("import \"%s\"\n\n", filepath.Join(modSrcDir, modName+".dash")))

	if parentName == "" { // INIT case
		if fnName == "" {
			fnName = "new"
		}
		args, err := buildDashArgs(schema, camelModName, fnName, inputArgs)
		if err != nil {
			return nil, fmt.Errorf("failed to build constructor args: %w", err)
		}
		script.WriteString(fmt.Sprintf("pvt inst = %s.%s(%s)\n", camelModName, fnName, args))
		script.WriteString("print(json(inst))\n")
	} else { // METHOD case
		script.WriteString(fmt.Sprintf("pvt inst = %s\n", camelModName))

		var parent map[string]json.RawMessage
		if err := json.Unmarshal(parentJSON, &parent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal parent json: %w", err)
		}

		parentTypeDef := schema.Types.Get(strcase.ToCamel(parentName))
		if parentTypeDef == nil {
			return nil, fmt.Errorf("type %s not found in schema", parentName)
		}

		for key, valBytes := range parent {
			var field *introspection.Field
			for _, f := range parentTypeDef.Fields {
				if f.Name == strcase.ToCamel(key) {
					field = f
					break
				}
			}
			if field == nil {
				continue
			}

			var val any
			if err := json.Unmarshal(valBytes, &val); err != nil {
				return nil, fmt.Errorf("failed to unmarshal parent field %s: %w", key, err)
			}
			dashLiteral, err := goValueToDashLiteral(val, field.TypeRef, schema)
			if err != nil {
				return nil, fmt.Errorf("failed to convert parent field %s to dash literal: %w", key, err)
			}
			script.WriteString(fmt.Sprintf("inst.%s = %s\n", strcase.ToKebab(key), dashLiteral))
		}

		kebabFnName := strcase.ToKebab(fnName)
		args, err := buildDashArgs(schema, camelModName, fnName, inputArgs)
		if err != nil {
			return nil, fmt.Errorf("failed to build method args: %w", err)
		}
		script.WriteString(fmt.Sprintf("print(json(inst.%s(%s)))\n", kebabFnName, args))
	}

	fmt.Fprintln(os.Stderr, script.String())

	slog.Debug("generated script", "path", tmpfile.Name(), "content", script.String())

	if _, err := tmpfile.WriteString(script.String()); err != nil {
		tmpfile.Close()
		return nil, fmt.Errorf("failed to write to temp file: %w", err)
	}
	if err := tmpfile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", err)
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	execCtx := ioctx.StdoutToContext(ctx, w)
	runErr := dash.RunFile(execCtx, dag.GraphQLClient(), schema, tmpfile.Name(), debug)

	w.Close()
	os.Stdout = oldStdout

	var resultBuf bytes.Buffer
	if _, err := io.Copy(&resultBuf, r); err != nil {
		return nil, fmt.Errorf("failed to read stdout: %w", err)
	}

	if runErr != nil {
		return nil, fmt.Errorf("failed to run dash script: %w\noutput: %s", runErr, resultBuf.String())
	}

	var result any
	if err := json.Unmarshal(resultBuf.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result from dash script: %w\noutput: %s", err, resultBuf.String())
	}

	return result, nil
}

func buildDashArgs(schema *introspection.Schema, typeName, fnName string, args map[string][]byte) (string, error) {
	objType := schema.Types.Get(typeName)
	if objType == nil {
		return "", fmt.Errorf("type %s not found in schema", typeName)
	}

	var fn *introspection.Field
	for _, f := range objType.Fields {
		if f.Name == fnName {
			fn = f
			break
		}
	}
	if fn == nil {
		return "", fmt.Errorf("function %s not found on type %s", fnName, typeName)
	}

	var parts []string
	for _, argDef := range fn.Args {
		argBytes, ok := args[argDef.Name]
		if !ok {
			continue // Or handle default values if they are available in schema
		}
		var val any
		if err := json.Unmarshal(argBytes, &val); err != nil {
			return "", fmt.Errorf("failed to unmarshal arg %s: %w", argDef.Name, err)
		}
		dashLiteral, err := goValueToDashLiteral(val, argDef.TypeRef, schema)
		if err != nil {
			return "", fmt.Errorf("failed to convert arg %s to dash literal: %w", argDef.Name, err)
		}
		parts = append(parts, fmt.Sprintf("%s: %s", strcase.ToKebab(argDef.Name), dashLiteral))
	}
	return strings.Join(parts, ", "), nil
}

func goValueToDashLiteral(val any, typeRef *introspection.TypeRef, schema *introspection.Schema) (string, error) {
	if val == nil {
		return "null", nil
	}

	if typeRef.Kind == introspection.TypeKindNonNull {
		typeRef = typeRef.OfType
	}

	if typeRef.Kind == introspection.TypeKindList {
		list, ok := val.([]any)
		if !ok {
			return "", fmt.Errorf("expected list for list type, got %T", val)
		}
		var parts []string
		for _, item := range list {
			dashItem, err := goValueToDashLiteral(item, typeRef.OfType, schema)
			if err != nil {
				return "", err
			}
			parts = append(parts, dashItem)
		}
		return fmt.Sprintf("[%s]", strings.Join(parts, ", ")), nil
	}

	switch typeRef.Kind {
	case introspection.TypeKindScalar:
		jsonBytes, err := json.Marshal(val)
		if err != nil {
			return "", err
		}
		return string(jsonBytes), nil
	case introspection.TypeKindObject:
		id, ok := val.(string)
		if !ok {
			return "", fmt.Errorf("expected object ID string, got %T", val)
		}
		loadFn := "load" + typeRef.Name + "FromID"
		return fmt.Sprintf("%s(\"%s\")", loadFn, id), nil
	case introspection.TypeKindInputObject:
		rec, ok := val.(map[string]any)
		if !ok {
			return "", fmt.Errorf("expected map for input object, got %T", val)
		}
		inputTypeDef := schema.Types.Get(typeRef.Name)
		if inputTypeDef == nil {
			return "", fmt.Errorf("input type %s not found", typeRef.Name)
		}
		var parts []string
		for key, fieldVal := range rec {
			var inputField *introspection.InputValue
			for _, f := range inputTypeDef.InputFields {
				if f.Name == strcase.ToCamel(key) {
					inputField = &f
					break
				}
			}
			if inputField == nil {
				continue
			}
			dashVal, err := goValueToDashLiteral(fieldVal, inputField.TypeRef, schema)
			if err != nil {
				return "", err
			}
			parts = append(parts, fmt.Sprintf("%s: %s", strcase.ToKebab(key), dashVal))
		}
		return fmt.Sprintf("{%s}", strings.Join(parts, ", ")), nil
	default:
		return "", fmt.Errorf("unsupported kind for dash literal: %s", typeRef.Kind)
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
		log.Println(err)
	}
}
