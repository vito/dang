package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/vito/dang/pkg/hm"
)

// materializeValue resolves deferred JSON values at expected-type boundaries.
//
// This is intentionally not a general runtime type assertion. Normal Dang
// values are trusted to match their inferred type and are returned unchanged,
// except for narrow explicit coercions (string-to-enum/custom-scalar and list
// elements that may themselves need materialization).
func materializeValue(ctx context.Context, env EvalEnv, val Value, target hm.Type, path string) (Value, error) {
	if target == nil {
		return val, nil
	}

	switch v := val.(type) {
	case JSONValue:
		return materializeJSON(ctx, env, v.Raw, target, path)
	case StringValue:
		return materializeStringValue(v, target, path)
	case ListValue:
		elemTarget, ok := listElementTarget(target)
		if !ok {
			return val, nil
		}
		elements := make([]Value, len(v.Elements))
		for i, elem := range v.Elements {
			materialized, err := materializeValue(ctx, env, elem, elemTarget, joinIndexPath(path, i))
			if err != nil {
				return nil, err
			}
			elements[i] = materialized
		}
		return ListValue{Elements: elements, ElemType: elemTarget}, nil
	default:
		return val, nil
	}
}

func materializeStringValue(val StringValue, target hm.Type, path string) (Value, error) {
	inner := unwrapNonNull(target)
	mod, ok := inner.(*Module)
	if !ok {
		return val, nil
	}

	switch mod.Kind {
	case EnumKind:
		if !enumHasValue(mod, val.Val) {
			return nil, materializeError(path, "invalid enum value %q for %s", val.Val, mod.Name())
		}
		return EnumValue{Val: val.Val, EnumType: mod}, nil
	case ScalarKind:
		if isPrimitiveScalar(mod) {
			return val, nil
		}
		return ScalarValue{Val: val.Val, ScalarType: mod}, nil
	default:
		return val, nil
	}
}

func materializeJSON(ctx context.Context, env EvalEnv, raw any, target hm.Type, path string) (Value, error) {
	if nn, ok := target.(hm.NonNullType); ok {
		if raw == nil {
			return nil, jsonMaterializeError(path, "null is not allowed for %s", target.Name())
		}
		return materializeJSON(ctx, env, raw, nn.Type, path)
	}

	if raw == nil {
		return NullValue{}, nil
	}

	switch target.(type) {
	case hm.TypeVariable, hm.NullableTypeVariable:
		return JSONValue{Raw: raw}, nil
	}

	if union, ok := target.(*hm.UnionType); ok {
		return materializeJSONUnion(ctx, env, raw, union, path)
	}

	if target == StringType {
		s, ok := raw.(string)
		if !ok {
			return nil, jsonMaterializeError(path, "expected string, got %s", jsonKind(raw))
		}
		return StringValue{Val: s}, nil
	}

	if target == IntType {
		num, ok := raw.(json.Number)
		if !ok {
			return nil, jsonMaterializeError(path, "expected int, got %s", jsonKind(raw))
		}
		val, err := jsonNumberToInt(num)
		if err != nil {
			return nil, jsonMaterializeError(path, "expected integral number for Int, got %q", num.String())
		}
		return IntValue{Val: val}, nil
	}

	if target == FloatType {
		num, ok := raw.(json.Number)
		if !ok {
			return nil, jsonMaterializeError(path, "expected number, got %s", jsonKind(raw))
		}
		val, err := num.Float64()
		if err != nil {
			return nil, jsonMaterializeError(path, "invalid number %q", num.String())
		}
		return FloatValue{Val: val}, nil
	}

	if target == BooleanType {
		b, ok := raw.(bool)
		if !ok {
			return nil, jsonMaterializeError(path, "expected boolean, got %s", jsonKind(raw))
		}
		return BoolValue{Val: b}, nil
	}

	if elemTarget, ok := listElementTarget(target); ok {
		arr, ok := raw.([]any)
		if !ok {
			return nil, jsonMaterializeError(path, "expected array, got %s", jsonKind(raw))
		}
		elements := make([]Value, len(arr))
		for i, elem := range arr {
			materialized, err := materializeJSON(ctx, env, elem, elemTarget, joinIndexPath(path, i))
			if err != nil {
				return nil, err
			}
			elements[i] = materialized
		}
		return ListValue{Elements: elements, ElemType: elemTarget}, nil
	}

	if mod, ok := target.(*Module); ok {
		switch mod.Kind {
		case EnumKind:
			s, ok := raw.(string)
			if !ok {
				return nil, jsonMaterializeError(path, "expected string for enum %s, got %s", mod.Name(), jsonKind(raw))
			}
			if !enumHasValue(mod, s) {
				return nil, jsonMaterializeError(path, "invalid enum value %q for %s", s, mod.Name())
			}
			return EnumValue{Val: s, EnumType: mod}, nil
		case ScalarKind:
			s, ok := raw.(string)
			if !ok {
				return nil, jsonMaterializeError(path, "expected string for scalar %s, got %s", mod.Name(), jsonKind(raw))
			}
			if mod == StringType {
				return StringValue{Val: s}, nil
			}
			return ScalarValue{Val: s, ScalarType: mod}, nil
		case ObjectKind:
			if mod.Named == "" {
				return materializeAnonymousObject(ctx, env, raw, mod, path)
			}
			return materializeNamedObject(ctx, env, raw, mod, path)
		case InputKind:
			return materializeNamedObject(ctx, env, raw, mod, path)
		}
	}

	return nil, jsonMaterializeError(path, "cannot materialize JSON as %s", target.Name())
}

func materializeJSONUnion(ctx context.Context, env EvalEnv, raw any, union *hm.UnionType, path string) (Value, error) {
	var lastErr error
	for _, option := range union.Options {
		val, err := materializeJSON(ctx, env, raw, option, path)
		if err == nil {
			return val, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, jsonMaterializeError(path, "cannot materialize JSON as %s", union.Name())
	}
	return JSONValue{Raw: raw}, nil
}

func materializeNamedObject(ctx context.Context, env EvalEnv, raw any, mod *Module, path string) (Value, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, jsonMaterializeError(path, "expected object for %s, got %s", mod.Name(), jsonKind(raw))
	}

	constructorVal, found := env.Get(mod.Named)
	if !found {
		return nil, jsonMaterializeError(path, "constructor for %s not found", mod.Name())
	}
	callable, ok := constructorVal.(Callable)
	if !ok {
		return nil, jsonMaterializeError(path, "%s is not callable", mod.Name())
	}

	args, err := materializeConstructorArgs(ctx, env, obj, callable, path)
	if err != nil {
		return nil, err
	}

	return callable.Call(ctx, env, args)
}

func materializeConstructorArgs(ctx context.Context, env EvalEnv, obj map[string]any, callable Callable, path string) (map[string]Value, error) {
	args := map[string]Value{}

	if constructor, ok := callable.(*ConstructorFunction); ok {
		paramTypes := parameterTypes(callable)
		for _, param := range constructor.Parameters {
			name := param.Name.Name
			paramType := paramTypes[name]
			if paramType == nil {
				paramType = param.GetInferredType()
			}
			fieldPath := joinFieldPath(path, name)
			if rawVal, found := obj[name]; found {
				materialized, err := materializeJSON(ctx, env, rawVal, paramType, fieldPath)
				if err != nil {
					return nil, err
				}
				args[name] = materialized
				continue
			}

			if param.Value != nil {
				// Omit defaulted parameters so constructor/default handling runs.
				continue
			}
			if isNullableType(paramType) {
				args[name] = NullValue{}
				continue
			}
			return nil, jsonMaterializeError(fieldPath, "missing required field")
		}
		return args, nil
	}

	paramTypes := parameterTypes(callable)
	for _, name := range callable.ParameterNames() {
		paramType := paramTypes[name]
		fieldPath := joinFieldPath(path, name)
		if rawVal, found := obj[name]; found {
			materialized, err := materializeJSON(ctx, env, rawVal, paramType, fieldPath)
			if err != nil {
				return nil, err
			}
			args[name] = materialized
			continue
		}
		if isNullableType(paramType) {
			args[name] = NullValue{}
			continue
		}
		return nil, jsonMaterializeError(fieldPath, "missing required field")
	}
	return args, nil
}

func materializeAnonymousObject(ctx context.Context, env EvalEnv, raw any, mod *Module, path string) (Value, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, jsonMaterializeError(path, "expected object, got %s", jsonKind(raw))
	}

	value := NewModuleValue(mod)
	for _, field := range mod.AsRecord().Fields {
		name := field.Key
		fieldType, mono := field.Value.Type()
		if !mono {
			return nil, jsonMaterializeError(joinFieldPath(path, name), "field type is not monomorphic")
		}
		fieldPath := joinFieldPath(path, name)
		if rawVal, found := obj[name]; found {
			materialized, err := materializeJSON(ctx, env, rawVal, fieldType, fieldPath)
			if err != nil {
				return nil, err
			}
			value.Set(name, materialized)
			continue
		}
		if isNullableType(fieldType) {
			value.Set(name, NullValue{})
			continue
		}
		return nil, jsonMaterializeError(fieldPath, "missing required field")
	}
	return value, nil
}

func parameterTypes(val Value) map[string]hm.Type {
	result := map[string]hm.Type{}
	ft, ok := val.Type().(*hm.FunctionType)
	if !ok {
		return result
	}
	rec, ok := ft.Arg().(*RecordType)
	if !ok {
		return result
	}
	for _, field := range rec.Fields {
		if typ, mono := field.Value.Type(); mono {
			result[field.Key] = typ
		}
	}
	return result
}

func parameterType(val Value, name string) (hm.Type, bool) {
	ft, ok := val.Type().(*hm.FunctionType)
	if !ok {
		return nil, false
	}
	rec, ok := ft.Arg().(*RecordType)
	if !ok {
		return nil, false
	}
	scheme, found := rec.SchemeOf(name)
	if !found {
		return nil, false
	}
	typ, mono := scheme.Type()
	if !mono {
		return nil, false
	}
	return typ, true
}

func listElementTarget(target hm.Type) (hm.Type, bool) {
	inner := unwrapNonNull(target)
	switch list := inner.(type) {
	case ListType:
		return list.Type, true
	case GraphQLListType:
		return list.Type, true
	default:
		return nil, false
	}
}

func unwrapNonNull(target hm.Type) hm.Type {
	if nn, ok := target.(hm.NonNullType); ok {
		return nn.Type
	}
	return target
}

func isNullableType(target hm.Type) bool {
	if target == nil {
		return true
	}
	_, nonNull := target.(hm.NonNullType)
	return !nonNull
}

func enumHasValue(enum *Module, value string) bool {
	scheme, found := enum.SchemeOf(value)
	if !found {
		return false
	}
	typ, mono := scheme.Type()
	if !mono {
		return false
	}
	_, err := hm.Assignable(typ, hm.NonNullType{Type: enum})
	return err == nil
}

func isPrimitiveScalar(mod *Module) bool {
	switch mod {
	case StringType, IntType, FloatType, BooleanType:
		return true
	default:
		return false
	}
}

func jsonNumberToInt(num json.Number) (int, error) {
	rat, ok := new(big.Rat).SetString(num.String())
	if !ok || !rat.IsInt() {
		return 0, fmt.Errorf("not an integer")
	}
	bigInt := rat.Num()
	if !bigInt.IsInt64() {
		return 0, fmt.Errorf("integer out of range")
	}
	i64 := bigInt.Int64()
	if int64(int(i64)) != i64 {
		return 0, fmt.Errorf("integer out of range")
	}
	return int(i64), nil
}

func joinFieldPath(path, field string) string {
	if path == "" || path == "$" {
		return field
	}
	return path + "." + field
}

func joinIndexPath(path string, index int) string {
	if path == "" || path == "$" {
		return fmt.Sprintf("[%d]", index)
	}
	return fmt.Sprintf("%s[%d]", path, index)
}

func materializeError(path, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if path == "" || path == "$" {
		return fmt.Errorf("%s", msg)
	}
	return fmt.Errorf("%s: %s", path, msg)
}

func jsonMaterializeError(path, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if path == "" || path == "$" {
		return fmt.Errorf("fromJSON: %s", msg)
	}
	return fmt.Errorf("fromJSON: %s: %s", path, msg)
}

func jsonKind(raw any) string {
	switch raw.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", raw)
	}
}

func materializePathForNode(node Node) string {
	switch n := node.(type) {
	case *Symbol:
		return n.Name
	case *Select:
		prefix := materializePathForNode(n.Receiver)
		if prefix == "" {
			return n.Field.Name
		}
		return joinFieldPath(prefix, n.Field.Name)
	default:
		return ""
	}
}
