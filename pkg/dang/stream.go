package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// StreamValue is a cold recipe for a sequence of values: naming one (e.g. a
// GraphQL subscription root field) runs nothing. A terminal (.each, .first,
// .toList) opens it under a child context, pulls values on the caller's
// goroutine, and cancels and closes it when the terminal exits by any route:
// completion, break, return, or raise. Consuming a stream twice opens it
// twice.
type StreamValue struct {
	ElemType hm.Type
	// Label describes the source, for display.
	Label string

	open func(ctx context.Context) (streamIter, error)

	// sub is set while the stream is an unmodified GraphQL subscription, so
	// a .{{ }} selection becomes the per-event selection set sent to the
	// server rather than a transformation applied client-side.
	sub *subscriptionSource
}

// streamIter is an opened stream.
type streamIter interface {
	// next blocks for the next value; ok is false once the stream completed.
	next(ctx context.Context) (val Value, ok bool, err error)
	// close releases the source (e.g. aborts the subscription request). It
	// must be safe to call more than once, and after the stream completed.
	close() error
}

var _ Value = StreamValue{}

func (s StreamValue) Type() hm.Type {
	if s.sub != nil {
		return hm.NonNullType{Type: GraphQLStreamType{s.ElemType}}
	}
	return hm.NonNullType{Type: StreamType{s.ElemType}}
}

func (s StreamValue) String() string {
	return fmt.Sprintf("<stream %s>", s.Label)
}

func (s StreamValue) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("a stream (%s) is not a value that can be serialized; consume it first, e.g. with .toList", s.Label)
}

// consume opens the stream and feeds each value to fn until fn returns false,
// fn fails, or the stream completes or fails. The stream is cancelled and
// closed before consume returns, whatever the route out.
func (s StreamValue) consume(ctx context.Context, fn func(Value) (bool, error)) error {
	sctx, cancel := context.WithCancel(ctx)
	it, err := s.open(sctx)
	if err != nil {
		cancel()
		return err
	}
	defer func() {
		cancel()
		_ = it.close()
	}()
	for {
		val, ok, err := it.next(sctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		more, err := fn(val)
		if err != nil || !more {
			return err
		}
	}
}

// transform derives a stream that rewrites each value of s as it is pulled.
func (s StreamValue) transform(label string, elemType hm.Type, step func(ctx context.Context, v Value) (Value, error)) StreamValue {
	return StreamValue{
		ElemType: elemType,
		Label:    s.Label + label,
		open: func(ctx context.Context) (streamIter, error) {
			inner, err := s.open(ctx)
			if err != nil {
				return nil, err
			}
			return &funcIter{
				nextFn: func(ctx context.Context) (Value, bool, error) {
					v, ok, err := inner.next(ctx)
					if err != nil || !ok {
						return nil, ok, err
					}
					out, err := step(ctx, v)
					if err != nil {
						return nil, false, err
					}
					return out, true, nil
				},
				closeFn: inner.close,
			}, nil
		},
	}
}

// take derives a stream that completes after n values, closing the source
// as soon as the nth value has been delivered rather than waiting for it.
func (s StreamValue) take(n int) StreamValue {
	return StreamValue{
		ElemType: s.ElemType,
		Label:    fmt.Sprintf("%s.take(%d)", s.Label, n),
		open: func(ctx context.Context) (streamIter, error) {
			if n <= 0 {
				return &funcIter{nextFn: func(context.Context) (Value, bool, error) { return nil, false, nil }}, nil
			}
			inner, err := s.open(ctx)
			if err != nil {
				return nil, err
			}
			seen := 0
			return &funcIter{
				nextFn: func(ctx context.Context) (Value, bool, error) {
					if seen >= n {
						_ = inner.close()
						return nil, false, nil
					}
					v, ok, err := inner.next(ctx)
					if ok {
						seen++
					}
					return v, ok, err
				},
				closeFn: inner.close,
			}, nil
		},
	}
}

type funcIter struct {
	nextFn  func(ctx context.Context) (Value, bool, error)
	closeFn func() error
}

func (f *funcIter) next(ctx context.Context) (Value, bool, error) {
	return f.nextFn(ctx)
}

func (f *funcIter) close() error {
	if f.closeFn == nil {
		return nil
	}
	return f.closeFn()
}

// listStream is a stream over the elements of a list. It is the core-language
// source of streams (List.toStream), for tests and fakes.
func listStream(list ListValue) StreamValue {
	return StreamValue{
		ElemType: list.ElemType,
		Label:    "list",
		open: func(context.Context) (streamIter, error) {
			i := 0
			return &funcIter{
				nextFn: func(ctx context.Context) (Value, bool, error) {
					if err := ctx.Err(); err != nil {
						return nil, false, context.Cause(ctx)
					}
					if i >= len(list.Elements) {
						return nil, false, nil
					}
					v := list.Elements[i]
					i++
					return v, true, nil
				},
			}, nil
		},
	}
}

// builtinContainer describes a builtin generic receiver type for method
// dispatch: the method module, the element type substituted for 'a', and
// whether the receiver was nullable.
type builtinContainer struct {
	module   *Type
	elem     hm.Type
	nullable bool
	label    string
}

// builtinContainerOf recognizes list, map and stream receiver types, non-null
// or nullable. A GraphQLStreamType is deliberately not one: it has no methods
// until a selection turns it into a StreamType.
func builtinContainerOf(t hm.Type) (builtinContainer, bool) {
	nullable := true
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
		nullable = false
	}
	switch ct := t.(type) {
	case ListType:
		return builtinContainer{ListTypeModule, ct.Type, nullable, "list"}, true
	case MapType:
		return builtinContainer{MapTypeModule, ct.Type, nullable, "map"}, true
	case StreamType:
		return builtinContainer{StreamTypeModule, ct.Type, nullable, "stream"}, true
	}
	return builtinContainer{}, false
}

// builtinContainerModuleOf is builtinContainerOf for runtime values.
func builtinContainerModuleOf(v Value) (*Type, string) {
	switch v.(type) {
	case ListValue:
		return ListTypeModule, "list"
	case MapValue:
		return MapTypeModule, "map"
	case StreamValue:
		return StreamTypeModule, "stream"
	}
	return nil, ""
}

// streamElemOf returns the element type of a (possibly non-null) stream type,
// and whether the stream is a GraphQLStreamType (events are GraphQL objects,
// so a selection on it is the operation's selection set).
func streamElemOf(t hm.Type) (elem hm.Type, nonNull bool, isGraphQL bool, ok bool) {
	if nn, isNN := t.(hm.NonNullType); isNN {
		t = nn.Type
		nonNull = true
	}
	switch st := t.(type) {
	case StreamType:
		return st.Type, nonNull, false, true
	case GraphQLStreamType:
		return st.Type, nonNull, true, true
	}
	return nil, false, false, false
}

// evalStreamSelection evaluates recv.{{ ... }} on a stream. On an unmodified
// subscription the selection becomes the operation's selection set, so only
// the selected fields are pushed; on any other stream it applies to each
// value as it is pulled.
func (o *ObjectSelection) evalStreamSelection(ctx context.Context, scope ValueScope, s StreamValue) (Value, error) {
	elemType, _, _, _ := streamElemOf(o.GetInferredType())
	if s.sub != nil {
		return s.sub.selectEvents(ctx, scope, o, elemType)
	}
	if len(o.InlineFragments) > 0 {
		return s.transform(".{{...}}", elemType, func(ctx context.Context, v Value) (Value, error) {
			return o.evalInlineFragmentOnValue(v, ctx, scope.Derive(true))
		}), nil
	}
	return s.transform(".{{...}}", elemType, func(ctx context.Context, v Value) (Value, error) {
		return o.evalSelectionOnValue(v, ctx, scope.Derive(true))
	}), nil
}
