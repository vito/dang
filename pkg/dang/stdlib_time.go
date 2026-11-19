package dang

// SPIKE: timing primitives (`sleep` and `timeout`). Durations are plain Go
// duration strings ("300ms", "1.5s") as a stopgap; a proper `Duration` scalar
// is the intended replacement.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vito/dang/v2/pkg/hm"
)

func registerTime() {
	// sleep(duration: String!) -> Null
	Builtin("sleep").
		Doc("pauses for the given duration (Go syntax: \"300ms\", \"1.5s\", \"5m\"); cancelled along with the surrounding evaluation, e.g. when it loses a race or a timeout expires").
		Example(`sleep("1ms")`).
		Params("duration", NonNull(StringType)).
		Returns(TypeVar('n')).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			d, err := parseDuration("sleep", args.GetString("duration"))
			if err != nil {
				return nil, err
			}
			if err := sleepCtx(ctx, d); err != nil {
				return nil, err
			}
			return NullValue{}, nil
		})

	// timeout(duration: String!) { a } -> a?
	Builtin("timeout").
		Doc("evaluates the block with a deadline, returning its value, or null if the deadline expires first; the block's in-flight work (sleeps, GraphQL requests, loops) is cancelled").
		Example(`timeout("1s") { 42 }`).
		Params("duration", NonNull(StringType)).
		Block(hm.NewFnType(NewRecordType(""), TypeVar('a'))).
		Returns(Nullable(TypeVar('a'))).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			if args.Block == nil {
				return nil, fmt.Errorf("timeout requires a block argument")
			}
			d, err := parseDuration("timeout", args.GetString("duration"))
			if err != nil {
				return nil, err
			}
			tctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			val, err := callFunc(tctx, *args.Block)
			if err != nil {
				// Only our own deadline turns into null: a parent cancellation,
				// a genuine error, or break/return passes through untouched.
				if ctx.Err() == nil &&
					errors.Is(tctx.Err(), context.DeadlineExceeded) &&
					!isControlFlowException(err) {
					return NullValue{}, nil
				}
				return nil, err
			}
			return val, nil
		})
}

func parseDuration(fn, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q (want Go syntax like \"300ms\", \"1.5s\", \"5m\")", fn, raw)
	}
	if d < 0 {
		return 0, fmt.Errorf("%s: negative duration %q", fn, raw)
	}
	return d, nil
}

// sleepCtx waits for d, returning early with the context's cause if ctx is
// cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}
