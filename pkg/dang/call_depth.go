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

// ContextWithMaxCallDepth sets the maximum depth of nested Dang function calls
// for evaluations run with the returned context. A limit of zero or less
// restores DefaultMaxCallDepth.
func ContextWithMaxCallDepth(ctx context.Context, max int) context.Context {
	if max <= 0 {
		max = DefaultMaxCallDepth
	}
	f := deriveFrame(ctx)
	f.maxDepth = max
	return withFrame(ctx, f)
}

// enterCall records entry into a Dang function body, failing with a
// StackOverflowError instead of letting runaway recursion exhaust the Go
// stack. It returns the frame for the body: the caller's, one call deeper.
// Every path that evaluates a user-defined body must go through it and
// evaluate the body under that frame (pushed with withFrame, along with any
// other state the call changes, such as its return target).
func enterCall(ctx context.Context) (evalFrame, error) {
	f := deriveFrame(ctx)
	if f.depth >= f.maxDepth {
		return f, &StackOverflowError{MaxDepth: f.maxDepth}
	}
	f.depth++
	return f, nil
}
