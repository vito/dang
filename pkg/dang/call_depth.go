package dang

import (
	"context"
	"fmt"
)

// DefaultMaxCallDepth bounds how deeply Dang function calls may nest when the
// context does not set a limit with ContextWithMaxCallDepth.
//
// Without a bound, runaway recursion keeps growing the Go stack until the
// runtime aborts with "fatal error: stack overflow". That is not a panic: no
// recover can catch it, so it takes down the whole host process (e.g. an
// engine evaluating many modules at once), not just the offending program.
// The default leaves ample headroom below Go's default 1 GB stack limit while
// still allowing deep, legitimate recursion.
const DefaultMaxCallDepth = 10000

// StackOverflowError is returned when a call would nest deeper than the
// maximum call depth.
type StackOverflowError struct {
	MaxDepth int
}

func (e *StackOverflowError) Error() string {
	return fmt.Sprintf("stack overflow: exceeded maximum call depth of %d (infinite recursion?)", e.MaxDepth)
}

type callDepthContextKey struct{}

// callDepth is stored as a single context value so the limit travels with the
// current depth: one lookup finds both at the nearest enclosing call, rather
// than walking the whole recursion-deep context chain to find the limit.
type callDepth struct {
	depth int
	max   int
}

// ContextWithMaxCallDepth sets the maximum depth of nested Dang function calls
// for evaluations run with the returned context. A limit of zero or less
// restores DefaultMaxCallDepth.
func ContextWithMaxCallDepth(ctx context.Context, max int) context.Context {
	if max <= 0 {
		max = DefaultMaxCallDepth
	}
	d := currentCallDepth(ctx)
	d.max = max
	return context.WithValue(ctx, callDepthContextKey{}, d)
}

func currentCallDepth(ctx context.Context) callDepth {
	if d, ok := ctx.Value(callDepthContextKey{}).(callDepth); ok {
		return d
	}
	return callDepth{max: DefaultMaxCallDepth}
}

// enterCall records entry into a Dang function body, failing with a
// StackOverflowError instead of letting runaway recursion exhaust the Go
// stack. Every path that evaluates a user-defined body must go through it and
// evaluate the body with the returned context.
func enterCall(ctx context.Context) (context.Context, error) {
	d := currentCallDepth(ctx)
	if d.depth >= d.max {
		return ctx, &StackOverflowError{MaxDepth: d.max}
	}
	d.depth++
	return context.WithValue(ctx, callDepthContextKey{}, d), nil
}
