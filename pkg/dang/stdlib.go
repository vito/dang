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
			fmt.Fprintln(writer, val.String())
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
			return StringValue{Val: string(jsonBytes)}, nil
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

			values := make([]Value, len(parts))
			for i, part := range parts {
				values[i] = StringValue{Val: part}
			}

			return ListValue{
				Elements: values,
				ElemType: hm.NonNullType{Type: StringType},
			}, nil
		})

	// String.toUpper method: toUpper() -> String!
	Method(StringType, "toUpper").
		Doc("converts a string to uppercase").
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			return StringValue{Val: strings.ToUpper(str)}, nil
		})
}
