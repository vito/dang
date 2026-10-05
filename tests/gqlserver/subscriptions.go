package gqlserver

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

// Tick is the model behind the Subscription.ticks events. fail marks the tick
// whose label resolver errors, so a pushed event can carry GraphQL errors.
type Tick struct {
	N    int `json:"n"`
	fail bool
}

// activeSubscriptions counts subscription resolvers whose streams are still
// open, so tests can observe that a client-side cancel reaches the server.
var activeSubscriptions atomic.Int64

// eventLog is the fixed log Subscription.events replays.
var eventLog []Event

func init() {
	eventLog = []Event{
		&MessageEvent{ID: "msg-1", Seq: 1, Text: "hello", Author: users[0]},
		&StateEvent{Seq: 2, Status: StatusActive},
		&MessageEvent{ID: "msg-3", Seq: 3, Text: "how are you?", Author: users[1]},
		&StateEvent{Seq: 4, Status: StatusArchived},
		&MessageEvent{ID: "msg-5", Seq: 5, Text: "bye", Author: users[0]},
	}
}

// eventsPerConnection is how many log entries one events() connection
// replays before completing, so callers have to reconnect with `after:`.
const eventsPerConnection = 2

func findEventNode(id string) Node {
	for _, ev := range eventLog {
		if msg, ok := ev.(*MessageEvent); ok && msg.ID == id {
			return msg
		}
	}
	return nil
}

func parseOptionalDuration(name string, raw *string) (time.Duration, error) {
	if raw == nil {
		return 0, nil
	}
	d, err := time.ParseDuration(*raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, *raw, err)
	}
	return d, nil
}

// stream runs produce on its own goroutine, delivering values on the returned
// channel and closing it when produce returns or ctx is cancelled. It keeps
// activeSubscriptions up to date for the lifetime of the goroutine.
func stream[T any](ctx context.Context, produce func(send func(T) bool)) <-chan T {
	ch := make(chan T)
	activeSubscriptions.Add(1)
	go func() {
		defer activeSubscriptions.Add(-1)
		defer close(ch)
		produce(func(v T) bool {
			select {
			case ch <- v:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	return ch
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
