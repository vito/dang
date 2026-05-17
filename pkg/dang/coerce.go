package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/vito/dang/pkg/hm"
)

// JSONValue is an opaque runtime value returned by fromJSON. It only becomes a
// regular Dang value when an expected type is available and coerceValue is
// called at an expected-type boundary.
type JSONValue struct {
	Raw any
}

var _ Value = JSONValue{}

func (j JSONValue) Type() hm.Type {
	return hm.TypeVariable('j')
}

func (j JSONValue) String() string {
	b, err := json.Marshal(j.Raw)
	if err != nil {
		return fmt.Sprintf("json:%T", j.Raw)
	}
	return string(b)
}

func (j JSONValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(j.Raw)
}

type coercionParam struct {
	name       string
	typ        hm.Type
	hasDefault bool
}

func coerceValue(ctx context.Context, env EvalEnv, val Value, target hm.Type, path string) (Value, error) {
	if target == nil || isTypeVariable(target) {
		return val, nil
	}
	if jsonVal, ok := val.(JSONValue); ok {
		return coerceJSON(ctx, env, jsonVal.Raw, target, path)
	}
	return coerceDangValue(ctx, env, val, target, path)
}

func coerceDangValue(ctx context.Context, env EvalEnv, val Value, target hm.Type, path string) (Value, error) {
	if target == nil || isTypeVariable(target) {
		return val, nil
	}

	if nn, ok := target.(hm.NonNullType); ok {
		// Normal Dang values have historically not been runtime-checked for
		// nullability at every boundary. Keep that behavior and only perform
		// narrow value conversions (e.g. String -> enum/custom scalar).
		return coerceDangValue(ctx, env, val, nn.Type, path)
	}

	if _, isNull := val.(NullValue); isNull {
		return val, nil
	}

	if union, ok := target.(*hm.UnionType); ok {
		for _, option := range union.Options {
			coerced, err := coerceValue(ctx, env, val, option, path)
			if err == nil {
				return coerced, nil
			}
		}
		return val, nil
	}

	if listType, ok := target.(ListType); ok {
		listVal, ok := val.(ListValue)
		if !ok {
			return val, nil
		}

		coerced := make([]Value, len(listVal.Elements))
		for i, elem := range listVal.Elements {
			cv, err := coerceValue(ctx, env, elem, listType.Type, appendPathIndex(path, i))
			if err != nil {
				return nil, err
			}
			coerced[i] = cv
		}
		return ListValue{Elements: coerced, ElemType: listType.Type}, nil
	}

	if mod, ok := target.(*Module); ok {
		switch mod.Kind {
		case EnumKind:
			if enumVal, ok := val.(EnumValue); ok {
				if _, err := hm.Assignable(enumVal.Type(), target); err == nil {
					return val, nil
				}
			}
			if strVal, ok := val.(StringValue); ok {
				return coerceStringToEnum(strVal.Val, mod, "coerce", path)
			}
		case ScalarKind:
			if scalarVal, ok := val.(ScalarValue); ok {
				if _, err := hm.Assignable(scalarVal.Type(), target); err == nil {
					return val, nil
				}
			}
			if strVal, ok := val.(StringValue); ok && isCustomScalar(mod) {
				return ScalarValue{Val: strVal.Val, ScalarType: mod}, nil
			}
		}
	}

	if _, err := hm.Assignable(val.Type(), target); err == nil {
		return val, nil
	}

	return val, nil
}

func coerceJSON(ctx context.Context, env EvalEnv, raw any, target hm.Type, path string) (Value, error) {
	if target == nil || isTypeVariable(target) {
		return JSONValue{Raw: raw}, nil
	}

	if nn, ok := target.(hm.NonNullType); ok {
		if raw == nil {
			return nil, coerceError("fromJSON", path, "cannot use null as %s", target.Name())
		}
		coerced, err := coerceJSON(ctx, env, raw, nn.Type, path)
		if err != nil {
			return nil, err
		}
		if _, isNull := coerced.(NullValue); isNull {
			return nil, coerceError("fromJSON", path, "cannot use null as %s", target.Name())
		}
		return coerced, nil
	}

	if raw == nil {
		return NullValue{}, nil
	}

	if union, ok := target.(*hm.UnionType); ok {
		for _, option := range union.Options {
			coerced, err := coerceJSON(ctx, env, raw, option, path)
			if err == nil {
				return coerced, nil
			}
		}
		return nil, coerceError("fromJSON", path, "cannot coerce %s to %s", rawJSONType(raw), target.Name())
	}

	switch target {
	case StringType:
		str, ok := raw.(string)
		if !ok {
			return nil, coerceError("fromJSON", path, "expected string, got %s", rawJSONType(raw))
		}
		return StringValue{Val: str}, nil
	case IntType:
		iv, ok := jsonNumberToInt(raw)
		if !ok {
			return nil, coerceError("fromJSON", path, "expected integral number, got %s", rawJSONType(raw))
		}
		return IntValue{Val: iv}, nil
	case FloatType:
		fv, ok := jsonNumberToFloat(raw)
		if !ok {
			return nil, coerceError("fromJSON", path, "expected number, got %s", rawJSONType(raw))
		}
		return FloatValue{Val: fv}, nil
	case BooleanType:
		bv, ok := raw.(bool)
		if !ok {
			return nil, coerceError("fromJSON", path, "expected boolean, got %s", rawJSONType(raw))
		}
		return BoolValue{Val: bv}, nil
	}

	if listType, ok := target.(ListType); ok {
		elems, ok := raw.([]any)
		if !ok {
			return nil, coerceError("fromJSON", path, "expected array for %s, got %s", target.Name(), rawJSONType(raw))
		}
		coerced := make([]Value, len(elems))
		for i, elem := range elems {
			cv, err := coerceJSON(ctx, env, elem, listType.Type, appendPathIndex(path, i))
			if err != nil {
				return nil, err
			}
			coerced[i] = cv
		}
		return ListValue{Elements: coerced, ElemType: listType.Type}, nil
	}

	if mod, ok := target.(*Module); ok {
		switch mod.Kind {
		case EnumKind:
			str, ok := raw.(string)
			if !ok {
				return nil, coerceError("fromJSON", path, "expected string for enum %s, got %s", mod.Name(), rawJSONType(raw))
			}
			return coerceStringToEnum(str, mod, "fromJSON", path)
		case ScalarKind:
			if !isCustomScalar(mod) {
				break
			}
			str, ok := raw.(string)
			if !ok {
				return nil, coerceError("fromJSON", path, "expected string for scalar %s, got %s", mod.Name(), rawJSONType(raw))
			}
			return ScalarValue{Val: str, ScalarType: mod}, nil
		case ObjectKind, InputKind:
			obj, ok := raw.(map[string]any)
			if !ok {
				return nil, coerceError("fromJSON", path, "expected object for %s, got %s", mod.Name(), rawJSONType(raw))
			}
			if mod.Named != "" {
				return coerceJSONToConstructor(ctx, env, obj, mod, path)
			}
			return coerceJSONToAnonymousObject(ctx, env, obj, mod, path)
		}
	}

	return nil, coerceError("fromJSON", path, "cannot coerce %s to %s", rawJSONType(raw), target.Name())
}

func coerceJSONToConstructor(ctx context.Context, env EvalEnv, obj map[string]any, mod *Module, path string) (Value, error) {
	ctorVal, found := env.Get(mod.Named)
	if !found {
		return nil, coerceError("fromJSON", path, "constructor %q not found", mod.Named)
	}
	callable, ok := ctorVal.(Callable)
	if !ok {
		return nil, coerceError("fromJSON", path, "%q is not callable", mod.Named)
	}

	params := callableParams(ctorVal)
	args := make(map[string]Value)
	for _, param := range params {
		fieldPath := appendPathField(path, param.name)
		rawField, provided := obj[param.name]
		if provided {
			coerced, err := coerceJSON(ctx, env, rawField, param.typ, fieldPath)
			if err != nil {
				return nil, err
			}
			args[param.name] = coerced
			continue
		}

		if param.hasDefault {
			continue
		}
		if isNullableType(param.typ) {
			args[param.name] = NullValue{}
			continue
		}
		return nil, coerceError("fromJSON", fieldPath, "missing required field")
	}

	constructed, err := callable.Call(ctx, env, args)
	if err != nil {
		return nil, coerceError("fromJSON", path, "constructing %s: %v", mod.Name(), err)
	}
	return constructed, nil
}

func coerceJSONToAnonymousObject(ctx context.Context, env EvalEnv, obj map[string]any, mod *Module, path string) (Value, error) {
	instance := NewModuleValue(mod)
	for name, scheme := range mod.Bindings(PrivateVisibility) {
		fieldType, mono := scheme.Type()
		if !mono {
			return nil, coerceError("fromJSON", appendPathField(path, name), "field type is not monomorphic")
		}
		fieldPath := appendPathField(path, name)
		if rawField, provided := obj[name]; provided {
			coerced, err := coerceJSON(ctx, env, rawField, fieldType, fieldPath)
			if err != nil {
				return nil, err
			}
			instance.SetWithVisibility(name, coerced, PublicVisibility)
			continue
		}

		if isNullableType(fieldType) {
			instance.SetWithVisibility(name, NullValue{}, PublicVisibility)
			continue
		}
		return nil, coerceError("fromJSON", fieldPath, "missing required field")
	}
	return instance, nil
}

func callableParams(callable Value) []coercionParam {
	switch c := callable.(type) {
	case *ConstructorFunction:
		params := make([]coercionParam, 0, len(c.Parameters))
		for _, param := range c.Parameters {
			typ := param.GetInferredType()
			if typ == nil {
				typ = functionParamType(c.FnType, param.Name.Name)
			}
			params = append(params, coercionParam{
				name:       param.Name.Name,
				typ:        typ,
				hasDefault: param.Value != nil,
			})
		}
		return params
	case FunctionValue:
		params := functionTypeParams(c.FnType)
		for i := range params {
			_, params[i].hasDefault = c.Defaults[params[i].name]
		}
		return params
	case BoundMethod:
		return callableParams(c.Method)
	case BuiltinFunction:
		return functionTypeParams(c.FnType)
	case BoundBuiltinMethod:
		return callableParams(c.Method)
	case GraphQLFunction:
		return functionTypeParams(c.FnType)
	case InputObjectConstructor:
		return functionTypeParams(c.FnType)
	}
	if typed, ok := callable.(interface{ Type() hm.Type }); ok {
		if ft, ok := typed.Type().(*hm.FunctionType); ok {
			return functionTypeParams(ft)
		}
	}
	return nil
}

func functionTypeParams(fnType *hm.FunctionType) []coercionParam {
	if fnType == nil {
		return nil
	}
	rec, ok := fnType.Arg().(*RecordType)
	if !ok {
		return nil
	}
	params := make([]coercionParam, 0, len(rec.Fields))
	for _, field := range rec.Fields {
		typ, mono := field.Value.Type()
		if !mono {
			continue
		}
		params = append(params, coercionParam{name: field.Key, typ: typ})
	}
	return params
}

func functionParamType(fnType *hm.FunctionType, name string) hm.Type {
	if fnType == nil {
		return nil
	}
	rec, ok := fnType.Arg().(*RecordType)
	if !ok {
		return nil
	}
	scheme, found := rec.SchemeOf(name)
	if !found {
		return nil
	}
	typ, mono := scheme.Type()
	if !mono {
		return nil
	}
	return typ
}

func coerceStringToEnum(str string, enumType *Module, origin, path string) (Value, error) {
	if !enumHasValue(enumType, str) {
		return nil, coerceError(origin, path, "invalid enum value %q for %s", str, enumType.Name())
	}
	return EnumValue{Val: str, EnumType: enumType}, nil
}

func enumHasValue(enumType *Module, value string) bool {
	scheme, found := enumType.SchemeOf(value)
	if !found {
		return false
	}
	typ, mono := scheme.Type()
	if !mono {
		return false
	}
	return typ.Eq(hm.NonNullType{Type: enumType})
}

func isCustomScalar(mod *Module) bool {
	if mod.Kind != ScalarKind {
		return false
	}
	switch mod {
	case StringType, IntType, FloatType, BooleanType, IDType, ListTypeModule:
		return false
	default:
		return true
	}
}

func isNullableType(typ hm.Type) bool {
	if typ == nil {
		return false
	}
	_, nonNull := typ.(hm.NonNullType)
	return !nonNull
}

func isTypeVariable(typ hm.Type) bool {
	switch typ.(type) {
	case hm.TypeVariable, hm.NullableTypeVariable:
		return true
	default:
		return false
	}
}

func jsonNumberToInt(raw any) (int, bool) {
	var f float64
	switch n := raw.(type) {
	case json.Number:
		if i64, err := n.Int64(); err == nil {
			if i64 < intMin() || i64 > intMax() {
				return 0, false
			}
			return int(i64), true
		}
		parsed, err := n.Float64()
		if err != nil {
			return 0, false
		}
		f = parsed
	case float64:
		f = n
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f || f < float64(intMin()) || f > float64(intMax()) {
		return 0, false
	}
	return int(f), true
}

func jsonNumberToFloat(raw any) (float64, bool) {
	switch n := raw.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func intMax() int64 {
	return int64(^uint(0) >> 1)
}

func intMin() int64 {
	return -intMax() - 1
}

func rawJSONType(raw any) string {
	switch raw.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number, float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", raw)
	}
}

func appendPathField(path, field string) string {
	if path == "" || path == "$" {
		return field
	}
	return path + "." + field
}

func appendPathIndex(path string, index int) string {
	if path == "" || path == "$" {
		return fmt.Sprintf("[%d]", index)
	}
	return fmt.Sprintf("%s[%d]", path, index)
}

func coerceError(origin, path, format string, args ...any) error {
	if origin == "" {
		origin = "coerce"
	}
	message := fmt.Sprintf(format, args...)
	if path == "" {
		path = "$"
	}
	return fmt.Errorf("%s: %s: %s", origin, path, message)
}
