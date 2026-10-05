package querybuilder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/Khan/genqlient/graphql"
	"github.com/vito/dang/v2/pkg/gqlsse"
)

// EventStream is an open subscription operation. Each Next yields the value
// at the selection's binding point (the root field with its sub-selection),
// unpacked exactly as Execute would unpack a query's response.
type EventStream struct {
	q      *QueryBuilder
	stream gqlsse.Stream
}

// Subscribe sends the selection as a subscription operation and returns its
// event stream. The selection must have been built from Subscription() and
// select exactly one root field; the client must implement gqlsse.Subscriber.
// Nothing is buffered: each Next reads the next pushed event. Cancelling ctx
// or calling Close ends the subscription.
func (q *QueryBuilder) Subscribe(ctx context.Context) (*EventStream, error) {
	if q.client == nil {
		return nil, fmt.Errorf("no client configured for selection")
	}
	if !q.isSubscription {
		return nil, fmt.Errorf("Subscribe requires a subscription selection")
	}
	sub, ok := q.client.(gqlsse.Subscriber)
	if !ok {
		return nil, fmt.Errorf("GraphQL client %T cannot carry subscriptions (it does not implement gqlsse.Subscriber)", q.client)
	}

	query, err := q.Build(ctx)
	if err != nil {
		return nil, err
	}
	const opName = "Subscription"
	payload := "subscription " + opName + " " + query
	slog.DebugContext(ctx, "starting GraphQL subscription", "query", payload)

	stream, err := sub.Subscribe(ctx, &graphql.Request{
		Query:  payload,
		OpName: opName,
	})
	if err != nil {
		return nil, err
	}
	return &EventStream{q: q, stream: stream}, nil
}

// Next blocks for the next pushed event and returns its unpacked value. It
// returns io.EOF when the server completes the stream, and the event's
// errors (a gqlerror.List) when the server pushes a result carrying errors.
func (s *EventStream) Next() (any, error) {
	result, err := s.stream.Next()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, err
	}
	if len(result.Errors) > 0 {
		return nil, result.Errors
	}
	var data any
	if len(result.Data) > 0 {
		if err := decodeJSON(result.Data, &data); err != nil {
			return nil, err
		}
	}
	var value any
	if err := s.q.Bind(&value).unpack(data); err != nil {
		return nil, err
	}
	return value, nil
}

// Close ends the subscription.
func (s *EventStream) Close() error {
	return s.stream.Close()
}
