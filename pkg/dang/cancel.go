package dang

import (
	"context"
	"errors"
	"fmt"
)

// Evaluation can be stopped from outside: when its context is cancelled or
// its deadline passes. Hosts that evaluate untrusted or agent-written code
// in-process rely on this, since nothing else stops `loop { }`.
//
// It is checked at one checkpoint, enterCall, which every evaluation of a
// user-defined body passes through: function and method calls (so recursion),
// constructors, and every invocation of a block — `loop` iterations, and the
// callbacks of each/map/filter/reduce/rewriteMatches and the other builtins
// taking a block. Code between two calls is bounded, so that is enough to stop
// any runaway evaluation. The check is a non-blocking ctx.Err(), cheap even in
// deep recursion (see frameContext).
//
// The resulting error aborts the evaluation: no `rescue` catches it (see
// isCancelled), or `loop { x rescue null }` would spin forever. Nor is it
// memoized as the result of a lazy binding or an imported module's code, which
// a later evaluation in the same scope runs again instead.
//
// The fail-fast cancellation evalParallel uses among `{{ }}` tasks is not an
// evaluation's cancellation: it stops in-flight I/O only, and Dang code in the
// remaining tasks runs to completion, as it always has (see cancellation).

// CancelledError is returned when evaluation stops because its context was
// cancelled or its deadline passed. It unwraps to the context's error, so
// errors.Is(err, context.DeadlineExceeded) and friends hold.
//
// Cancellation noticed by I/O rather than by the interpreter (a GraphQL
// request cut short) surfaces as that request's error instead, which wraps
// the context's error too: hosts should test for cancellation with errors.Is
// (or their context's Err), not errors.As(*CancelledError).
type CancelledError struct {
	// Err is the context's error: context.Canceled or
	// context.DeadlineExceeded.
	Err error
	// Cause is context.Cause of the context, when it differs from Err.
	Cause error
}

func (e *CancelledError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("evaluation cancelled: %s", e.Cause)
	}
	return fmt.Sprintf("evaluation cancelled: %s", e.Err)
}

func (e *CancelledError) Unwrap() []error {
	if e.Cause != nil {
		return []error{e.Err, e.Cause}
	}
	return []error{e.Err}
}

// checkpoint fails if the evaluation running under ctx, whose current frame is
// f, was cancelled. Every call passes through it (see enterCall), so it must
// stay cheap.
func checkpoint(ctx context.Context, f *evalFrame) error {
	if ctx.Err() != nil {
		if c := cancellation(ctx, f); c != nil {
			return c
		}
	}
	return nil
}

// errSiblingFailed is the cause evalParallel cancels its tasks' context with
// once one of them fails.
var errSiblingFailed = errors.New("a sibling `{{ }}` field failed")

// parallelTask describes the `{{ }}` task an evaluation runs in.
//
// evalParallel cancels the tasks' own context (ctx) with errSiblingFailed
// once one fails. That cancellation is for in-flight I/O: Dang code ignores
// it unless watch, the context the `{{ }}` was evaluated under (the
// outermost, for nested ones), is done too, and runs to completion so the
// error reported stays deterministic (see cancellation).
type parallelTask struct {
	ctx   context.Context
	watch context.Context
}

// cancellation returns the cancellation stopping the evaluation running under
// ctx, whose current frame is f, or nil if it may go on. A `{{ }}` task's
// context being done only because evalParallel cancelled it when a sibling
// failed doesn't count, unless the context the `{{ }}` ran under is done
// too.
func cancellation(ctx context.Context, f *evalFrame) *CancelledError {
	if ctx.Err() == nil {
		return nil
	}
	// The task's context is a cancelCtx, so its cause is at hand, whereas
	// context.Cause(ctx) would walk every frame above it.
	if t := f.task; t != nil && t.ctx.Err() != nil && context.Cause(t.ctx) == errSiblingFailed {
		if t.watch.Err() == nil {
			return nil
		}
		return cancelledError(t.watch)
	}
	return cancelledError(ctx)
}

// evalCancelled returns the cancellation stopping the evaluation running
// under ctx, or nil if it may go on.
func evalCancelled(ctx context.Context) *CancelledError {
	return cancellation(ctx, currentFrame(ctx))
}

// cutShort reports whether err, returned by work run under ctx, says only
// that the work was cut short, not what it would have produced: the
// evaluation was cancelled, or (in a `{{ }}` task) I/O was cancelled because
// a sibling failed. Such an error must not be memoized as the work's result.
func cutShort(ctx context.Context, err error) bool {
	if isCancelled(err) || evalCancelled(ctx) != nil {
		return true
	}
	return ctx.Err() != nil &&
		(errors.Is(err, context.Canceled) || errors.Is(err, errSiblingFailed))
}

// watchedContext returns the context whose cancellation stops the evaluation
// running under ctx, whose current frame is f: ctx itself, unless that is a
// `{{ }}` task's (see parallelTask).
func watchedContext(ctx context.Context, f *evalFrame) context.Context {
	if f.task != nil {
		return f.task.watch
	}
	return ctx
}

// cancelledError describes why ctx, which is done, stopped the evaluation.
func cancelledError(ctx context.Context) *CancelledError {
	err := &CancelledError{Err: ctx.Err()}
	if cause := context.Cause(ctx); cause != nil && cause != err.Err {
		err.Cause = cause
	}
	return err
}

// isCancelled reports whether err is, or wraps, a CancelledError: the
// evaluation was cancelled, which nothing may rescue.
func isCancelled(err error) bool {
	var cancelled *CancelledError
	return errors.As(err, &cancelled)
}
