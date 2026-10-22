package dang

// SPIKE: `[T]!.race`, the dynamic (list) form of the `select` expression
// proposed in docs/proposals/concurrency-and-time.md, and raceValues, the
// evaluator core that `select` would share.

import (
	"context"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

func registerRace() {
	// List.race method: race(fn: \(a) -> b) -> b
	Method(ListTypeModule, "race").
		Doc("evaluates the block for every element concurrently and returns the result of the first to finish, cancelling and awaiting the rest; raises if the first to finish raised, or if the list is empty").
		Example("[30, 1, 20].race { ms => sleep(`${ms}ms`); ms }").
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "item",
				Value: hm.NewScheme(nil, TypeVar('a')),
			}),
			TypeVar('b'),
		)).
		Returns(TypeVar('b')).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			list := self.(ListValue)
			if args.Block == nil {
				return nil, fmt.Errorf("race requires a block argument")
			}
			if len(list.Elements) == 0 {
				return nil, fmt.Errorf("race over an empty list: nothing can ever finish")
			}
			fn := *args.Block
			_, val, err := raceValues(ctx, len(list.Elements), func(ctx context.Context, i int) (Value, error) {
				return callFunc(ctx, fn, list.Elements[i])
			})
			return val, err
		})
}

// raceResult is one contender's outcome.
type raceResult struct {
	idx int
	val Value
	err error
}

// raceValues runs n contenders concurrently and returns the index and outcome
// (value or error) of the first to finish. Contenders that had ALSO already
// finished by the time the winner is observed are considered too, and the
// lowest index among them wins. That preference is best-effort only:
// goroutine scheduling decides what has "already finished", so the winner
// among near-simultaneous finishers is unspecified. Every loser is then
// cancelled and awaited: no contender outlives the race (structured
// concurrency, like evalParallel).
func raceValues(ctx context.Context, n int, fn func(context.Context, int) (Value, error)) (int, Value, error) {
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan raceResult, n)
	for i := range n {
		go func(i int) {
			v, err := fn(raceCtx, i)
			results <- raceResult{idx: i, val: v, err: err}
		}(i)
	}

	finished := []raceResult{<-results}
drain:
	for {
		select {
		case r := <-results:
			finished = append(finished, r)
		default:
			break drain
		}
	}

	// Cancel the losers and wait for them to unwind before returning.
	cancel()
	for range n - len(finished) {
		<-results
	}

	winner := finished[0]
	for _, r := range finished[1:] {
		if r.idx < winner.idx {
			winner = r
		}
	}
	return winner.idx, winner.val, winner.err
}
