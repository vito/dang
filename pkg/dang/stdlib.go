package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/ioctx"
)

// registerStdlib registers all standard library builtins
// This is called from init() in env.go after type definitions are set up
func registerStdlib() {
	// print function: print(value: a) -> Null
	Builtin("print").
		Doc("prints a value to stdout").
		Params("value", TypeVar('a')).
		Returns(TypeVar('n')).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			val, _ := args.Get("value")
			writer := ioctx.StdoutFromContext(ctx)
			fmt.Fprintln(writer, val)
			return NullValue{}, nil
		})

	// toJSON function: toJSON(value: b) -> String!
	Builtin("toJSON").
		Doc("serializes a value to JSON").
		Params("value", TypeVar('b')).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			val, _ := args.Get("value")
			jsonBytes, err := json.Marshal(val)
			if err != nil {
				return nil, fmt.Errorf("toJSON: %w", err)
			}
			return ToValue(string(jsonBytes))
		})

	// String.split method: split(separator: String!, limit: Int = 0) -> [String!]!
	Method(StringType, "split").
		Doc("splits a string by separator").
		Params(
			"separator", NonNull(StringType),
			"limit", IntType, IntValue{Val: 0},
		).
		Returns(NonNull(ListOf(NonNull(StringType)))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			sep := args.GetString("separator")
			limit := args.GetInt("limit")

			var parts []string
			if sep == "" {
				// Split every character
				for _, ch := range str {
					parts = append(parts, string(ch))
				}
				// Apply limit if specified
				if limit > 0 && len(parts) >= limit {
					remaining := strings.Join(parts[limit-1:], "")
					parts = append(parts[:limit-1], remaining)
				}
			} else {
				if limit > 0 {
					parts = strings.SplitN(str, sep, limit)
				} else {
					parts = strings.Split(str, sep)
				}
			}

			// Use ToValue helper for conversion
			return ToValue(parts)
		})

	// String.toUpper method: toUpper() -> String!
	Method(StringType, "toUpper").
		Doc("converts a string to uppercase").
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			return ToValue(strings.ToUpper(str))
		})

	// String.toLower method: toLower() -> String!
	Method(StringType, "toLower").
		Doc("converts a string to lowercase").
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			return ToValue(strings.ToLower(str))
		})

	// String.trimPrefix method: trimPrefix(prefix: String!) -> String!
	Method(StringType, "trimPrefix").
		Doc("removes the specified prefix from the string if present").
		Params("prefix", NonNull(StringType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			prefix := args.GetString("prefix")
			return ToValue(strings.TrimPrefix(str, prefix))
		})

	// String.trimSuffix method: trimSuffix(suffix: String!) -> String!
	Method(StringType, "trimSuffix").
		Doc("removes the specified suffix from the string if present").
		Params("suffix", NonNull(StringType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			suffix := args.GetString("suffix")
			return ToValue(strings.TrimSuffix(str, suffix))
		})

	// String.trimSpace method: trimSpace() -> String!
	Method(StringType, "trimSpace").
		Doc("removes all leading and trailing whitespace from the string").
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			return ToValue(strings.TrimSpace(str))
		})

	// String.trim method: trim(cutset: String!) -> String!
	Method(StringType, "trim").
		Doc("removes all leading and trailing characters in cutset from the string").
		Params("cutset", NonNull(StringType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			cutset := args.GetString("cutset")
			return ToValue(strings.Trim(str, cutset))
		})

	// String.trimLeft method: trimLeft(cutset: String!) -> String!
	Method(StringType, "trimLeft").
		Doc("removes all leading characters in cutset from the string").
		Params("cutset", NonNull(StringType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			cutset := args.GetString("cutset")
			return ToValue(strings.TrimLeft(str, cutset))
		})

	// String.trimRight method: trimRight(cutset: String!) -> String!
	Method(StringType, "trimRight").
		Doc("removes all trailing characters in cutset from the string").
		Params("cutset", NonNull(StringType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			cutset := args.GetString("cutset")
			return ToValue(strings.TrimRight(str, cutset))
		})

	// String.hasPrefix method: hasPrefix(prefix: String!) -> Boolean!
	Method(StringType, "hasPrefix").
		Doc("checks if the string starts with the specified prefix").
		Params("prefix", NonNull(StringType)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			prefix := args.GetString("prefix")
			return ToValue(strings.HasPrefix(str, prefix))
		})

	// String.hasSuffix method: hasSuffix(suffix: String!) -> Boolean!
	Method(StringType, "hasSuffix").
		Doc("checks if the string ends with the specified suffix").
		Params("suffix", NonNull(StringType)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			suffix := args.GetString("suffix")
			return ToValue(strings.HasSuffix(str, suffix))
		})

	// String.contains method: contains(substring: String!) -> Boolean!
	Method(StringType, "contains").
		Doc("checks if the string contains the specified substring").
		Params("substring", NonNull(StringType)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			substring := args.GetString("substring")
			return ToValue(strings.Contains(str, substring))
		})

	// String.padRight method: padRight(width: Int!) -> String!
	Method(StringType, "padRight").
		Doc("pads the string with spaces on the right to reach the specified width").
		Params("width", NonNull(IntType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			width := args.GetInt("width")

			// If the string is already at or longer than the target width, return as-is
			if len(str) >= width {
				return ToValue(str)
			}

			// Pad with spaces to reach the target width
			padded := str + strings.Repeat(" ", width-len(str))
			return ToValue(padded)
		})

	// String.padLeft method: padLeft(width: Int!) -> String!
	Method(StringType, "padLeft").
		Doc("pads the string with spaces on the left to reach the specified width").
		Params("width", NonNull(IntType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			width := args.GetInt("width")

			// If the string is already at or longer than the target width, return as-is
			if len(str) >= width {
				return ToValue(str)
			}

			// Pad with spaces to reach the target width
			padded := strings.Repeat(" ", width-len(str)) + str
			return ToValue(padded)
		})

	// String.center method: center(width: Int!) -> String!
	Method(StringType, "center").
		Doc("centers the string within the specified width by padding with spaces on both sides").
		Params("width", NonNull(IntType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			width := args.GetInt("width")

			// If the string is already at or longer than the target width, return as-is
			if len(str) >= width {
				return ToValue(str)
			}

			// Calculate padding needed
			totalPad := width - len(str)
			leftPad := totalPad / 2
			rightPad := totalPad - leftPad

			// Center the string
			centered := strings.Repeat(" ", leftPad) + str + strings.Repeat(" ", rightPad)
			return ToValue(centered)
		})

	// List.contains method: contains(element: a) -> Boolean!
	Method(ListTypeModule, "contains").
		Doc("checks if the list contains the specified element").
		Params("element", TypeVar('a')).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			element, _ := args.Get("element")

			for _, item := range list.Elements {
				if valuesEqual(item, element) {
					return BoolValue{Val: true}, nil
				}
			}
			return BoolValue{Val: false}, nil
		})

	// List.reject method: reject(fn: \(a) -> Boolean!) -> [a]!
	Method(ListTypeModule, "reject").
		Doc("returns a new list excluding elements for which the predicate returns true").
		Params("fn", hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			NonNull(BooleanType),
		)).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			fn, _ := args.Get("fn")

			var result []Value
			for _, item := range list.Elements {
				res, err := callFunc(ctx, fn, item)
				if err != nil {
					return nil, fmt.Errorf("reject predicate: %w", err)
				}

				boolVal, ok := res.(BoolValue)
				if !ok {
					return nil, fmt.Errorf("reject predicate must return Boolean!, got %T", res)
				}

				if !boolVal.Val {
					result = append(result, item)
				}
			}

			return ListValue{
				Elements: result,
				ElemType: list.ElemType,
			}, nil
		})

	// List.map method: map(fn: \(a) -> b) -> [b]!
	Method(ListTypeModule, "map").
		Doc("returns a new list with each element transformed by the given function").
		Params("fn", hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			TypeVar('b'),
		)).
		Returns(NonNull(ListOf(TypeVar('b')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			fn, _ := args.Get("fn")

			// Get the return type from the function's type
			callable, ok := fn.(Callable)
			if !ok {
				return nil, fmt.Errorf("map expects a function, got %T", fn)
			}
			fnType, ok := callable.Type().(*hm.FunctionType)
			if !ok {
				return nil, fmt.Errorf("map expects a function type, got %T", callable.Type())
			}
			resultElemType := fnType.ReturnType()

			var result []Value
			for _, item := range list.Elements {
				res, err := callFunc(ctx, fn, item)
				if err != nil {
					return nil, fmt.Errorf("map function: %w", err)
				}

				result = append(result, res)
			}

			return ListValue{
				Elements: result,
				ElemType: resultElemType,
			}, nil
		})

	// List.filter method: filter(fn: \(a) -> Boolean!) -> [a]!
	Method(ListTypeModule, "filter").
		Doc("returns a new list containing only elements for which the predicate returns true").
		Params("fn", hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			NonNull(BooleanType),
		)).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			fn, _ := args.Get("fn")

			var result []Value
			for _, item := range list.Elements {
				res, err := callFunc(ctx, fn, item)
				if err != nil {
					return nil, fmt.Errorf("filter predicate: %w", err)
				}

				boolVal, ok := res.(BoolValue)
				if !ok {
					return nil, fmt.Errorf("filter predicate must return Boolean!, got %T", res)
				}

				if boolVal.Val {
					result = append(result, item)
				}
			}

			return ListValue{
				Elements: result,
				ElemType: list.ElemType,
			}, nil
		})

	// List.length method: length -> Int!
	Method(ListTypeModule, "length").
		Doc("returns the number of elements in the list").
		Returns(NonNull(IntType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			return IntValue{Val: len(list.Elements)}, nil
		})

	// List.isEmpty method: isEmpty -> Boolean!
	Method(ListTypeModule, "isEmpty").
		Doc("returns true if the list contains no elements").
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			return BoolValue{Val: len(list.Elements) == 0}, nil
		})

	// List.reduce method: reduce(fn: \(acc: b, item: a) -> b, initial: b) -> b
	Method(ListTypeModule, "reduce").
		Doc("reduces the list to a single value using an accumulator function").
		Params(
			"initial", TypeVar('b'),
			"fn", hm.NewFnType(
				NewRecordType("", Keyed[*hm.Scheme]{
					Key:   "acc",
					Value: hm.NewScheme(nil, TypeVar('b')),
				}, Keyed[*hm.Scheme]{
					Key:   "item",
					Value: hm.NewScheme(nil, TypeVar('a')),
				}),
				TypeVar('b'),
			),
		).
		Returns(TypeVar('b')).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			fn, _ := args.Get("fn")
			accumulator, _ := args.Get("initial")

			for _, item := range list.Elements {
				result, err := callFunc(ctx, fn, accumulator, item)
				if err != nil {
					return nil, fmt.Errorf("reduce function: %w", err)
				}
				accumulator = result
			}

			return accumulator, nil
		})
}

// callFunc calls a function with the given values as arguments.
// The values are mapped to the function's parameters in order.
func callFunc(ctx context.Context, fn Value, args ...Value) (Value, error) {
	callable, ok := fn.(Callable)
	if !ok {
		return nil, fmt.Errorf("expected a function, got %T", fn)
	}

	paramNames := callable.ParameterNames()
	if len(paramNames) < len(args) {
		return nil, fmt.Errorf("function has %d parameters but %d arguments provided", len(paramNames), len(args))
	}

	callArgs := make(map[string]Value)
	for i, arg := range args {
		callArgs[paramNames[i]] = arg
	}

	return callable.Call(ctx, NewModuleValue(NewModule("_temp_", ObjectKind)), callArgs)
}
