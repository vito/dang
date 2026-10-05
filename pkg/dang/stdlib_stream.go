package dang

// Stream[a] builtins: the terminals that consume a stream (each, first,
// toList), take, which bounds one, and the core-language source
// List.toStream. GraphQL subscription root fields are the other source
// (stream_graphql.go). See docs/proposals/subscriptions.md.

import (
	"context"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

func registerStream() {
	itemBlock := func(ret hm.Type) *hm.FunctionType {
		return hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			ret,
		)
	}

	// Stream.each: the loop. The block runs on the caller's goroutine, once
	// per value, in order; `break v` makes the call yield v and `continue`
	// skips to the next value, as with a list's .each. However the call
	// exits, the stream is cancelled and closed before it returns.
	Method(StreamTypeModule, "each").
		Doc("consumes the stream, calling the block with each value until the stream completes (yielding null) or the block exits it with break (yielding the break value), return, or raise; the stream is cancelled when the loop exits").
		Example(`[1, 2, 3].toStream.each { x => if (x == 2) { break x * 10 } }`).
		Block(itemBlock(TypeVar('b'))).
		Returns(Nullable(TypeVar('r'))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			stream := self.(StreamValue)
			if args.Block == nil {
				return nil, fmt.Errorf("each requires a block argument")
			}
			fn := *args.Block
			err := stream.consume(ctx, func(v Value) (bool, error) {
				if _, err := callFunc(ctx, fn, v); err != nil {
					return false, err
				}
				return true, nil
			})
			if err != nil {
				return nil, err
			}
			return NullValue{}, nil
		})

	Method(StreamTypeModule, "first").
		Doc("waits for the stream's first value and cancels the rest of the stream; null if the stream completes without a value. Streams are cold, so each call opens the stream afresh").
		Example(`[1, 2, 3].toStream.first`).
		Returns(Nullable(TypeVar('a'))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			var first Value = NullValue{}
			err := self.(StreamValue).consume(ctx, func(v Value) (bool, error) {
				first = v
				return false, nil
			})
			if err != nil {
				return nil, err
			}
			return first, nil
		})

	Method(StreamTypeModule, "toList").
		Doc("consumes the stream until it completes, collecting its values; never returns for a stream that never completes, so bound it first with take").
		Example(`[1, 2, 3].toStream.toList`).
		Returns(NonNull(ListOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			stream := self.(StreamValue)
			elems := []Value{}
			err := stream.consume(ctx, func(v Value) (bool, error) {
				elems = append(elems, v)
				return true, nil
			})
			if err != nil {
				return nil, err
			}
			return ListValue{Elements: elems, ElemType: stream.ElemType}, nil
		})

	Method(StreamTypeModule, "take").
		Doc("a stream of at most the first n values; it completes, cancelling its source, once n values have been delivered").
		Example(`[1, 2, 3].toStream.take(2).toList`).
		Params("n", NonNull(IntType)).
		Returns(NonNull(StreamOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return self.(StreamValue).take(args.GetInt("n")), nil
		})

	Method(ListTypeModule, "toStream").
		Doc("a stream of the list's elements, for driving stream consumers from plain values").
		Example(`[1, 2, 3].toStream.toList`).
		Returns(NonNull(StreamOf(TypeVar('a')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return listStream(self.(ListValue)), nil
		})
}
