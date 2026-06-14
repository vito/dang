package dang

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/ioctx"
)

// registerStdlib registers all standard library builtins
// This is called from init() in env.go after type definitions are set up
func registerStdlib() {
	registerRandomAndUUID()
	registerJSON()
	registerAssert()
	registerRegexp()

	// print function: print(value: a) -> Null
	Builtin("print").
		Doc("prints a value to stdout").
		Example(`print("hello, world")`).
		Params("value", TypeVar('a')).
		Returns(TypeVar('n')).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			val, _ := args.Get("value")
			writer := ioctx.StdoutFromContext(ctx)
			_, _ = fmt.Fprintln(writer, val)
			return NullValue{}, nil
		})

	// loop function: loop { ... } -> r
	//
	// Repeatedly calls the block forever; the only normal exit is a break
	// (or return/raise) unwinding out of the block, so the call's result is
	// the break value. The declared return type is a bare type variable that
	// only ever materializes through mergeCallBreakTypes.
	Builtin("loop").
		Doc("repeatedly calls the block until it exits via break").
		Example(`loop { break 42 }`).
		Block(hm.NewFnType(NewRecordType(""), TypeVar('a'))).
		Returns(TypeVar('r')).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			if args.Block == nil {
				return nil, fmt.Errorf("loop requires a block argument")
			}
			fn := *args.Block

			for {
				if _, err := callFunc(ctx, fn); err != nil {
					return nil, err
				}
			}
		})

	// fromYAML function: fromYAML(data: String!) -> a
	Builtin("fromYAML").
		Doc("parses YAML into an opaque value that is materialized by an expected type").
		Example(`fromYAML("[a, b, c]") :: [String!]!`).
		Params("data", NonNull(StringType)).
		Returns(TypeVar('a')).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			data := args.GetString("data")
			raw, err := decodeYAML(data)
			if err != nil {
				return nil, fmt.Errorf("fromYAML: invalid YAML: %w", err)
			}
			return DeferredValue{Raw: raw}, nil
		})

	// toString function: toString(value: b) -> String!
	Builtin("toString").
		Doc("converts a value to a string, returning strings as-is and serializing other values to JSON").
		Example(`toString(42)`).
		Params("value", TypeVar('b')).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			val, _ := args.Get("value")

			// If already a string, return as-is
			if strVal, ok := val.(StringValue); ok {
				return strVal, nil
			}

			// Otherwise, serialize to JSON
			jsonBytes, err := json.Marshal(val)
			if err != nil {
				return nil, fmt.Errorf("toString: %w", err)
			}
			return ToValue(string(jsonBytes))
		})

	// String.split method: split(separator: String!, limit: Int = 0) -> [String!]!
	Method(StringType, "split").
		Doc("splits a string by separator").
		Example(`"a,b,c".split(",")`).
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
		Example(`"hello".toUpper`).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			return ToValue(strings.ToUpper(str))
		})

	// String.toLower method: toLower() -> String!
	Method(StringType, "toLower").
		Doc("converts a string to lowercase").
		Example(`"HELLO".toLower`).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			return ToValue(strings.ToLower(str))
		})

	// String.toBase64 method: toBase64() -> String!
	Method(StringType, "toBase64").
		Doc("encodes the string's bytes as a standard (padded) base64 string").
		Example(`"hello".toBase64`).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			return ToValue(base64.StdEncoding.EncodeToString([]byte(str)))
		})

	// String.fromBase64 method: fromBase64() -> String!
	Method(StringType, "fromBase64").
		Doc("decodes a standard (padded) base64 string, returning the decoded bytes as a string").
		Example(`"aGVsbG8=".fromBase64`).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			decoded, err := base64.StdEncoding.DecodeString(str)
			if err != nil {
				return nil, fmt.Errorf("fromBase64: %w", err)
			}
			return ToValue(string(decoded))
		})

	// String.trimPrefix method: trimPrefix(prefix: String!) -> String!
	Method(StringType, "trimPrefix").
		Doc("removes the specified prefix from the string if present").
		Example(`"v1.2.3".trimPrefix("v")`).
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
		Example(`"report.txt".trimSuffix(".txt")`).
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
		Example(`"  hi  ".trimSpace`).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			return ToValue(strings.TrimSpace(str))
		})

	// String.trim method: trim(cutset: String!) -> String!
	Method(StringType, "trim").
		Doc("removes all leading and trailing characters in cutset from the string").
		Example(`"__hi__".trim("_")`).
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
		Example(`"__hi__".trimLeft("_")`).
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
		Example(`"__hi__".trimRight("_")`).
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
		Example(`"dang".hasPrefix("da")`).
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
		Example(`"dang".hasSuffix("ng")`).
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
		Example(`"dang".contains("an")`).
		Params("substring", NonNull(StringType)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			substring := args.GetString("substring")
			return ToValue(strings.Contains(str, substring))
		})

	// String.replace method: replace(old: String!, new: String!, count: Int = -1) -> String!
	Method(StringType, "replace").
		Doc("replaces occurrences of old with new in the string. The count parameter controls how many replacements to make: -1 (default) replaces all occurrences, 1 replaces only the first, etc.").
		Example(`"a-b-c".replace("-", "_")`).
		Params(
			"old", NonNull(StringType),
			"new", NonNull(StringType),
			"count", IntType, IntValue{Val: -1},
		).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			old := args.GetString("old")
			new := args.GetString("new")
			count := args.GetInt("count")

			result := strings.Replace(str, old, new, count)
			return ToValue(result)
		})

	// String.padRight method: padRight(width: Int!) -> String!
	Method(StringType, "padRight").
		Doc("pads the string with spaces on the right to reach the specified width").
		Example(`"hi".padRight(5) + "|"`).
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
		Example(`"hi".padLeft(5) + "|"`).
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
		Example(`"hi".center(6) + "|"`).
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
		Example(`[1, 2, 3].contains(2)`).
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

	// List.uniq method: uniq -> [a]!
	Method(ListTypeModule, "uniq").
		Doc("returns a new list with duplicate elements removed, preserving first occurrence order").
		Example(`[1, 1, 2, 3, 3].uniq`).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			result := make([]Value, 0, len(list.Elements))

			for _, item := range list.Elements {
				seen := false
				for _, existing := range result {
					if valuesEqual(existing, item) {
						seen = true
						break
					}
				}
				if !seen {
					result = append(result, item)
				}
			}

			return ListValue{
				Elements: result,
				ElemType: list.ElemType,
			}, nil
		})

	// List.reject method: reject(fn: \(a) -> Boolean!) -> [a]!
	Method(ListTypeModule, "reject").
		Doc("returns a new list excluding elements for which the predicate returns true").
		Example(`[1, 2, 3, 4].reject { x => x > 2 }`).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			NonNull(BooleanType),
		)).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)

			if args.Block == nil {
				return nil, fmt.Errorf("reject requires a block argument")
			}
			fn := *args.Block

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

	// List.each method: each(fn: \(a, Int!) -> b) -> [a]!
	Method(ListTypeModule, "each").
		Doc("iterates over each element in the list, calling the block for each element").
		Example(`[1, 2, 3].each { x => print(x) }`).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}, Keyed[*hm.Scheme]{
				Key:   "index",
				Value: hm.NewScheme(nil, NonNull(IntType)),
			}),
			TypeVar('b'),
		)).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)

			if args.Block == nil {
				return nil, fmt.Errorf("each requires a block argument")
			}
			fn := *args.Block

			for i, item := range list.Elements {
				_, err := callFunc(ctx, fn, item, IntValue{i})
				if err != nil {
					return nil, fmt.Errorf("each block: %w", err)
				}
			}

			return list, nil
		})

	// List.any method: any(fn: \(a) -> Boolean!) -> Boolean!
	Method(ListTypeModule, "any").
		Doc("returns true if at least one element satisfies the predicate").
		Example(`[1, 2, 3].any { x => x > 2 }`).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			NonNull(BooleanType),
		)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)

			if args.Block == nil {
				return nil, fmt.Errorf("any requires a block argument")
			}
			fn := *args.Block

			for _, item := range list.Elements {
				res, err := callFunc(ctx, fn, item)
				if err != nil {
					return nil, fmt.Errorf("any predicate: %w", err)
				}

				boolVal, ok := res.(BoolValue)
				if !ok {
					return nil, fmt.Errorf("any predicate must return Boolean!, got %T", res)
				}

				if boolVal.Val {
					return BoolValue{Val: true}, nil
				}
			}

			return BoolValue{Val: false}, nil
		})

	// List.all method: all(fn: \(a) -> Boolean!) -> Boolean!
	Method(ListTypeModule, "all").
		Doc("returns true if all elements satisfy the predicate").
		Example(`[1, 2, 3].all { x => x > 0 }`).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			NonNull(BooleanType),
		)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)

			if args.Block == nil {
				return nil, fmt.Errorf("all requires a block argument")
			}
			fn := *args.Block

			for _, item := range list.Elements {
				res, err := callFunc(ctx, fn, item)
				if err != nil {
					return nil, fmt.Errorf("all predicate: %w", err)
				}

				boolVal, ok := res.(BoolValue)
				if !ok {
					return nil, fmt.Errorf("all predicate must return Boolean!, got %T", res)
				}

				if !boolVal.Val {
					return BoolValue{Val: false}, nil
				}
			}

			return BoolValue{Val: true}, nil
		})

	// List.map method: map(fn: \(a) -> b) -> [b]!
	Method(ListTypeModule, "map").
		Doc("returns a new list with each element transformed by the given function").
		Example(`[1, 2, 3].map { x => x * 2 }`).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}, Keyed[*hm.Scheme]{
				Key:   "index",
				Value: hm.NewScheme(nil, NonNull(IntType)),
			}),
			TypeVar('b'),
		)).
		Returns(NonNull(ListOf(TypeVar('b')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)

			if args.Block == nil {
				return nil, fmt.Errorf("map requires a block argument")
			}
			fn := *args.Block

			// Get the return type from the function's type
			fnType, ok := fn.Type().(*hm.FunctionType)
			if !ok {
				return nil, fmt.Errorf("map expects a function type, got %T", fn.Type())
			}
			resultElemType := fnType.ReturnType()

			var result []Value
			for i, item := range list.Elements {
				res, err := callFunc(ctx, fn, item, IntValue{i})
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
		Example(`[1, 2, 3, 4].filter { x => x > 2 }`).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			NonNull(BooleanType),
		)).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)

			if args.Block == nil {
				return nil, fmt.Errorf("filter requires a block argument")
			}
			fn := *args.Block

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

	// List.find method: find(fn: \(a) -> Boolean!) -> a?
	Method(ListTypeModule, "find").
		Doc("returns the first element for which the predicate returns true, or null if none match").
		Example(`[1, 2, 3].find { x => x > 1 }`).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			NonNull(BooleanType),
		)).
		Returns(Nullable(TypeVar('a'))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)

			if args.Block == nil {
				return nil, fmt.Errorf("find requires a block argument")
			}
			fn := *args.Block

			for _, item := range list.Elements {
				res, err := callFunc(ctx, fn, item)
				if err != nil {
					return nil, fmt.Errorf("find predicate: %w", err)
				}

				boolVal, ok := res.(BoolValue)
				if !ok {
					return nil, fmt.Errorf("find predicate must return Boolean!, got %T", res)
				}

				if boolVal.Val {
					return item, nil
				}
			}

			return NullValue{}, nil
		})

	// List.length method: length -> Int!
	Method(ListTypeModule, "length").
		Doc("returns the number of elements in the list").
		Example(`[1, 2, 3].length`).
		Returns(NonNull(IntType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			return IntValue{Val: len(list.Elements)}, nil
		})

	// List.isEmpty method: isEmpty -> Boolean!
	Method(ListTypeModule, "isEmpty").
		Doc("returns true if the list contains no elements").
		Example(`[1, 2, 3].isEmpty`).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			return BoolValue{Val: len(list.Elements) == 0}, nil
		})

	// List.reduce method: reduce(fn: \(acc: b, item: a) -> b, initial: b) -> b
	Method(ListTypeModule, "reduce").
		Doc("reduces the list to a single value using an accumulator function").
		Example(`[1, 2, 3, 4].reduce(0) { acc, x => acc + x }`).
		Params("initial", TypeVar('b')).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "acc",
				Value: hm.NewScheme(nil, TypeVar('b')),
			}, Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			TypeVar('b'),
		)).
		Returns(TypeVar('b')).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)

			if args.Block == nil {
				return nil, fmt.Errorf("reduce requires a block argument")
			}
			fn := *args.Block
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

	// List.dropLast method: dropLast(count: Int = 1) -> [a]!
	Method(ListTypeModule, "dropLast").
		Doc("returns a new list with the last count elements removed (default 1)").
		Example(`[1, 2, 3, 4].dropLast(2)`).
		Params("count", IntType, IntValue{Val: 1}).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			count := max(args.GetInt("count"), 0)
			end := max(len(list.Elements)-count, 0)
			return ListValue{
				Elements: list.Elements[:end],
				ElemType: list.ElemType,
			}, nil
		})

	// List.dropFirst method: dropFirst(count: Int = 1) -> [a]!
	Method(ListTypeModule, "dropFirst").
		Doc("returns a new list with the first count elements removed (default 1)").
		Example(`[1, 2, 3, 4].dropFirst(2)`).
		Params("count", IntType, IntValue{Val: 1}).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			count := max(args.GetInt("count"), 0)
			start := min(count, len(list.Elements))
			return ListValue{
				Elements: list.Elements[start:],
				ElemType: list.ElemType,
			}, nil
		})

	// List.takeFirst method: takeFirst(count: Int = 1) -> [a]!
	Method(ListTypeModule, "takeFirst").
		Doc("returns a new list containing only the first count elements (default 1)").
		Example(`[1, 2, 3, 4].takeFirst(2)`).
		Params("count", IntType, IntValue{Val: 1}).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			count := max(args.GetInt("count"), 0)
			end := min(count, len(list.Elements))
			return ListValue{
				Elements: list.Elements[:end],
				ElemType: list.ElemType,
			}, nil
		})

	// List.takeLast method: takeLast(count: Int = 1) -> [a]!
	Method(ListTypeModule, "takeLast").
		Doc("returns a new list containing only the last count elements (default 1)").
		Example(`[1, 2, 3, 4].takeLast(2)`).
		Params("count", IntType, IntValue{Val: 1}).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			count := max(args.GetInt("count"), 0)
			start := max(len(list.Elements)-count, 0)
			return ListValue{
				Elements: list.Elements[start:],
				ElemType: list.ElemType,
			}, nil
		})

	// List.takeWhile method: takeWhile(fn: \(a) -> Boolean!) -> [a]!
	Method(ListTypeModule, "takeWhile").
		Doc("returns a new list containing leading elements for which the predicate returns true, stopping at the first false").
		Example(`[1, 2, 3, 1].takeWhile { x => x < 3 }`).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			NonNull(BooleanType),
		)).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)

			if args.Block == nil {
				return nil, fmt.Errorf("takeWhile requires a block argument")
			}
			fn := *args.Block

			var result []Value
			for _, item := range list.Elements {
				res, err := callFunc(ctx, fn, item)
				if err != nil {
					return nil, fmt.Errorf("takeWhile predicate: %w", err)
				}

				boolVal, ok := res.(BoolValue)
				if !ok {
					return nil, fmt.Errorf("takeWhile predicate must return Boolean!, got %T", res)
				}

				if !boolVal.Val {
					break
				}
				result = append(result, item)
			}

			return ListValue{
				Elements: result,
				ElemType: list.ElemType,
			}, nil
		})

	// List.dropWhile method: dropWhile(fn: \(a) -> Boolean!) -> [a]!
	Method(ListTypeModule, "dropWhile").
		Doc("returns a new list with leading elements removed for which the predicate returns true, stopping at the first false").
		Example(`[1, 2, 3, 1].dropWhile { x => x < 3 }`).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			NonNull(BooleanType),
		)).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)

			if args.Block == nil {
				return nil, fmt.Errorf("dropWhile requires a block argument")
			}
			fn := *args.Block

			dropping := true
			var result []Value
			for _, item := range list.Elements {
				if dropping {
					res, err := callFunc(ctx, fn, item)
					if err != nil {
						return nil, fmt.Errorf("dropWhile predicate: %w", err)
					}

					boolVal, ok := res.(BoolValue)
					if !ok {
						return nil, fmt.Errorf("dropWhile predicate must return Boolean!, got %T", res)
					}

					if boolVal.Val {
						continue
					}
					dropping = false
				}
				result = append(result, item)
			}

			return ListValue{
				Elements: result,
				ElemType: list.ElemType,
			}, nil
		})

	// List.join method: join(separator: String!) -> String!
	Method(ListTypeModule, "join").
		Doc("joins the list elements into a string, converting each element to string and separating them with the given delimiter").
		Example(`["a", "b", "c"].join("-")`).
		Params("separator", NonNull(StringType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			separator := args.GetString("separator")

			var parts []string
			for _, item := range list.Elements {
				// Convert each item to string using the same logic as toString
				var strVal string
				if sv, ok := item.(StringValue); ok {
					strVal = sv.Val
				} else {
					jsonBytes, err := json.Marshal(item)
					if err != nil {
						return nil, fmt.Errorf("join: failed to convert element to string: %w", err)
					}
					strVal = string(jsonBytes)
				}
				parts = append(parts, strVal)
			}

			return ToValue(strings.Join(parts, separator))
		})

	// Map.get method: get(key: String!) -> a
	Method(MapTypeModule, "get").
		Doc("returns the value for the given key, or null if the key is absent").
		Example(`["a": 1, "b": 2].get("a")`).
		Params("key", NonNull(StringType)).
		Returns(TypeVar('a')).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MapValue)
			key := args.GetString("key")
			if val, ok := m.Get(key); ok {
				return val, nil
			}
			return NullValue{}, nil
		})

	// Map.has method: has(key: String!) -> Boolean!
	Method(MapTypeModule, "has").
		Doc("returns true if the map contains the given key").
		Example(`["a": 1, "b": 2].has("a")`).
		Params("key", NonNull(StringType)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MapValue)
			_, ok := m.Get(args.GetString("key"))
			return BoolValue{Val: ok}, nil
		})

	// Map.with method: with(key: String!, value: a) -> Map[a]!
	Method(MapTypeModule, "with").
		Doc("returns a new map with the given key set to the given value").
		Example(`["a": 1].with("b", 2)`).
		Params("key", NonNull(StringType), "value", TypeVar('a')).
		Returns(NonNull(MapOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MapValue)
			value, _ := args.Get("value")
			return m.With(args.GetString("key"), value), nil
		})

	// Map.without method: without(key: String!) -> Map[a]!
	Method(MapTypeModule, "without").
		Doc("returns a new map with the given key removed").
		Example(`["a": 1, "b": 2].without("a")`).
		Params("key", NonNull(StringType)).
		Returns(NonNull(MapOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MapValue)
			return m.Without(args.GetString("key")), nil
		})

	// Map.merge method: merge(other: Map[a]!) -> Map[a]!
	Method(MapTypeModule, "merge").
		Doc("returns a new map combining this map with another; values from the other map win on key conflicts").
		Example(`["a": 1, "b": 2].merge(["b": 20, "c": 3])`).
		Params("other", NonNull(MapOf(TypeVar('a')))).
		Returns(NonNull(MapOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MapValue)
			otherVal, _ := args.Get("other")
			other, ok := otherVal.(MapValue)
			if !ok {
				return nil, fmt.Errorf("merge expects a map, got %T", otherVal)
			}
			result := m
			for _, k := range other.Keys {
				result = result.With(k, other.Entries[k])
			}
			return result, nil
		})

	// Map.keys method: keys -> [String!]!
	Method(MapTypeModule, "keys").
		Doc("returns the map's keys in insertion order").
		Example(`["a": 1, "b": 2].keys`).
		Returns(NonNull(ListOf(NonNull(StringType)))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MapValue)
			elems := make([]Value, len(m.Keys))
			for i, k := range m.Keys {
				elems[i] = StringValue{Val: k}
			}
			return ListValue{Elements: elems, ElemType: NonNull(StringType)}, nil
		})

	// Map.values method: values -> [a]!
	Method(MapTypeModule, "values").
		Doc("returns the map's values in insertion order").
		Example(`["a": 1, "b": 2].values`).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MapValue)
			elems := make([]Value, len(m.Keys))
			for i, k := range m.Keys {
				elems[i] = m.Entries[k]
			}
			return ListValue{Elements: elems, ElemType: m.ValType}, nil
		})

	// Map.length method: length -> Int!
	Method(MapTypeModule, "length").
		Doc("returns the number of entries in the map").
		Example(`["a": 1, "b": 2].length`).
		Returns(NonNull(IntType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MapValue)
			return IntValue{Val: len(m.Keys)}, nil
		})

	// Map.isEmpty method: isEmpty -> Boolean!
	Method(MapTypeModule, "isEmpty").
		Doc("returns true if the map contains no entries").
		Example(`[:].isEmpty`).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MapValue)
			return BoolValue{Val: len(m.Keys) == 0}, nil
		})

	// Map.each method: each(fn: \(String!, a) -> b) -> Map[a]!
	Method(MapTypeModule, "each").
		Doc("iterates over each entry in insertion order, calling the block with the key and value").
		Example(`["a": 1, "b": 2].each { key, value => print(`+"`${key}=${value}`"+`) }`).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "key",
				Value: hm.NewScheme(nil, NonNull(StringType)),
			}, Keyed[*hm.Scheme]{
				Key:   "value",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			TypeVar('b'),
		)).
		Returns(NonNull(MapOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MapValue)
			if args.Block == nil {
				return nil, fmt.Errorf("each requires a block argument")
			}
			fn := *args.Block
			for _, k := range m.Keys {
				_, err := callFunc(ctx, fn, StringValue{Val: k}, m.Entries[k])
				if err != nil {
					return nil, fmt.Errorf("each block: %w", err)
				}
			}
			return m, nil
		})

	// Map.map method: map(fn: \(String!, a) -> b) -> Map[b]!
	Method(MapTypeModule, "map").
		Doc("returns a new map with each value transformed by the block; keys are preserved").
		Example(`["a": 1, "b": 2].map { key, value => value * 2 }`).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "key",
				Value: hm.NewScheme(nil, NonNull(StringType)),
			}, Keyed[*hm.Scheme]{
				Key:   "value",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			TypeVar('b'),
		)).
		Returns(NonNull(MapOf(TypeVar('b')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MapValue)
			if args.Block == nil {
				return nil, fmt.Errorf("map requires a block argument")
			}
			fn := *args.Block
			fnType, ok := fn.Type().(*hm.FunctionType)
			if !ok {
				return nil, fmt.Errorf("map expects a function type, got %T", fn.Type())
			}
			resultValType := fnType.ReturnType()

			entries := make(map[string]Value, len(m.Keys))
			for _, k := range m.Keys {
				res, err := callFunc(ctx, fn, StringValue{Val: k}, m.Entries[k])
				if err != nil {
					return nil, fmt.Errorf("map block: %w", err)
				}
				entries[k] = res
			}
			return MapValue{Keys: append([]string{}, m.Keys...), Entries: entries, ValType: resultValType}, nil
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

	return callable.Call(ctx, NewObject(NewType("_temp_", ObjectKind)), callArgs)
}
