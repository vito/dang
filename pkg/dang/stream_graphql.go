package dang

// GraphQL subscription root fields as streams. See stream.go for the stream
// runtime and docs/proposals/subscriptions.md for the design.

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"
	"github.com/vito/dang/v2/pkg/querybuilder"
)

// subscriptionFieldType is the type of calling a subscription root field
// declared with type ret: a Stream of its values. A field whose events are
// GraphQL objects (or lists of them) is a GraphQLStreamType, which, like a
// GraphQLListType, has to be given a selection (.{{ }}) before it can be
// consumed: a pushed event carries only what the operation selected.
func subscriptionFieldType(f *introspection.Field, ret hm.Type, schema *introspection.Schema) hm.Type {
	if !isScalarType(f.TypeRef, schema) {
		return NonNull(GraphQLStreamType{ret})
	}
	return NonNull(StreamOf(ret))
}

// isSubscriptionRootType reports whether t is a schema's Subscription root
// type (the namespace subscription fields are called through).
func isSubscriptionRootType(t hm.Type) bool {
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}
	mod, ok := t.(*Type)
	if !ok || mod.SourceSchema == nil || mod.SourceSchema.SubscriptionType == nil {
		return false
	}
	return mod.Kind == ObjectKind && mod.Named == mod.SourceSchema.SubscriptionType.Name
}

// firstFieldName names the first selected field, for error messages.
func (o *ObjectSelection) firstFieldName() string {
	if len(o.Fields) > 0 {
		return o.Fields[0].Name
	}
	return "field"
}

// subscriptionSource is a GraphQL subscription root field call: the
// `subscription { field(args) }` chain, before any selection set.
type subscriptionSource struct {
	fn   GraphQLFunction
	base *querybuilder.Selection
}

// newSubscriptionStream is the stream a subscription root field evaluates to.
// A leaf-typed field (scalars, enums, lists of them) can be consumed as is.
// An object-typed field cannot: its StreamValue only becomes consumable once
// a .{{ }} selection has decided what each event carries (selectEvents); the
// type checker rejects terminals on it, and open guards the same at runtime.
func newSubscriptionStream(fn GraphQLFunction, base *querybuilder.Selection) StreamValue {
	elemType := fn.FnType.Ret(false)
	if elem, _, _, ok := streamElemOf(elemType); ok {
		elemType = elem
	}
	label := "Subscription." + fn.Name
	if !isScalarType(fn.Field.TypeRef, fn.Schema) {
		return StreamValue{
			ElemType: elemType,
			Label:    label,
			sub:      &subscriptionSource{fn: fn, base: base},
			open: func(context.Context) (streamIter, error) {
				return nil, fmt.Errorf("%s pushes %s objects: select the fields each event should carry first, e.g. %s.{{id}}", label, getTypeName(fn.Field.TypeRef), label)
			},
		}
	}
	return StreamValue{
		ElemType: elemType,
		Label:    label,
		open: subscriptionOpener(base, fn.Client, func(raw any) (Value, error) {
			return graphQLResultToValue(raw, fn.Field.TypeRef, fn.Field.Directives.ExpectedType(), fn.Schema, fn.TypeScope, fn.Client)
		}),
	}
}

// selectEvents is a .{{ }} selection on a subscription stream: the selection
// becomes the operation's selection set, and each event converts like the
// result of the same selection on a query. With inline fragments the event is
// a member of the narrowed union the fragments describe.
func (src *subscriptionSource) selectEvents(ctx context.Context, scope ValueScope, o *ObjectSelection, elemType hm.Type) (StreamValue, error) {
	fn := src.fn
	label := "Subscription." + fn.Name + ".{{...}}"

	if len(o.InlineFragments) > 0 {
		query := src.base.SelectMultiple(o.inlineFragmentSelectionParts()...)
		return StreamValue{
			ElemType: elemType,
			Label:    label,
			open: subscriptionOpener(query, fn.Client, func(raw any) (Value, error) {
				return o.convertInlineFragmentResult(raw, fn.Schema, fn.TypeScope, fn.Client)
			}),
		}, nil
	}

	query, err := o.buildGraphQLQuery(ctx, scope, src.base, o.Fields)
	if err != nil {
		return StreamValue{}, err
	}
	return StreamValue{
		ElemType: elemType,
		Label:    label,
		open: subscriptionOpener(query, fn.Client, func(raw any) (Value, error) {
			if raw == nil {
				return NullValue{}, nil
			}
			return o.convertGraphQLResultToModule(raw, o.Fields, fn.Schema, fn.Field, fn.TypeScope, fn.Client)
		}),
	}, nil
}

// subscriptionOpener opens the subscription operation query carries and
// converts each pushed event with convert.
func subscriptionOpener(query *querybuilder.Selection, client graphql.Client, convert func(any) (Value, error)) func(ctx context.Context) (streamIter, error) {
	return func(ctx context.Context) (streamIter, error) {
		events, err := query.Client(client).Subscribe(ctx)
		if err != nil {
			return nil, fmt.Errorf("starting subscription: %w", err)
		}
		return &funcIter{
			nextFn: func(ctx context.Context) (Value, bool, error) {
				raw, err := events.Next()
				if err != nil {
					if errors.Is(err, io.EOF) {
						return nil, false, nil
					}
					return nil, false, err
				}
				val, err := convert(raw)
				if err != nil {
					return nil, false, err
				}
				return val, true, nil
			},
			closeFn: events.Close,
		}, nil
	}
}
