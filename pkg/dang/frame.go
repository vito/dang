package dang

import (
	"context"
	"io"

	"github.com/vito/dang/v2/pkg/ioctx"
)

// evalFrame is the interpreter's runtime state for the code being evaluated:
// everything evaluation reads from "the current call" lives here, under a
// single context key, rather than as a stack of context values that lookups
// must walk.
//
// Frames are immutable once published: evaluation fans out over goroutines
// sharing a context (see evalParallel), so changing any field means copying
// the current frame (deriveFrame), overriding what changes, and pushing the
// copy (withFrame). Evaluating a node never pushes a frame by itself; only a
// change of state does, so a function call costs one push and the context
// chain between two frames stays short. Every lookup therefore stops at the
// nearest frame, instead of walking a chain whose length grows with recursion
// depth, and nothing misses: state that isn't set is simply a zero field.
//
// All access goes through frameFrom/currentFrame, so that threading the frame
// explicitly instead of through context.Context can later be a mechanical
// change.
type evalFrame struct {
	// evalCtx is the source context for error reporting (see
	// WithEvalContext).
	evalCtx *EvalContext

	// depth is the number of Dang function calls currently nested, and
	// maxDepth the most allowed (see enterCall).
	depth    int
	maxDepth int

	// Targets of return, break and continue, and the call expression a
	// block argument breaks out of (see control_flow.go).
	returnFrame    *ControlFrame
	breakFrame     *ControlFrame
	continueFrame  *ControlFrame
	blockCallFrame *ControlFrame

	// blockArg is the block argument for the next call to consume (see
	// ContextWithBlock).
	blockArg Value

	// forceChain is the chain of lazy initializers being forced (see
	// forcingInChain).
	forceChain *forceChain

	// inFlight is the error being rescued, exposed through the dynamic extent
	// of a rescue arm (clause expr or fallback) so that a raise during
	// recovery records it as the new error's cause (see RescueExpr).
	inFlight *RaisedError

	// stdout and stderr are the writers ioctx resolves for the context the
	// frame was pushed on. The frame answers ioctx lookups with them, so
	// printing doesn't walk to the root of the context. A writer installed
	// with ioctx above a frame still wins: lookups find it before the frame,
	// and the next frame pushed picks it up (see deriveFrame).
	stdout io.Writer
	stderr io.Writer
}

// rootFrame is the state of an evaluation before anything has been set.
var rootFrame = evalFrame{maxDepth: DefaultMaxCallDepth}

type frameKey struct{}

// frameContext is the context layer carrying an evalFrame. It owns its frame
// by value, so pushing a frame is a single allocation.
type frameContext struct {
	context.Context
	frame evalFrame
}

func (c *frameContext) Value(key any) any {
	if _, ok := key.(frameKey); ok {
		return &c.frame
	}
	switch {
	case ioctx.IsStdoutKey(key):
		return c.frame.stdout
	case ioctx.IsStderrKey(key):
		return c.frame.stderr
	}
	return c.Context.Value(key)
}

// frameFrom returns the nearest frame in ctx, or nil if there is none.
func frameFrom(ctx context.Context) *evalFrame {
	if fc, ok := ctx.(*frameContext); ok {
		return &fc.frame
	}
	f, _ := ctx.Value(frameKey{}).(*evalFrame)
	return f
}

// currentFrame returns the nearest frame in ctx, or the root frame if there is
// none. The result must not be modified.
func currentFrame(ctx context.Context) *evalFrame {
	if f := frameFrom(ctx); f != nil {
		return f
	}
	return &rootFrame
}

// deriveFrame returns a copy of ctx's current frame, to override fields of and
// push with withFrame.
func deriveFrame(ctx context.Context) evalFrame {
	if fc, ok := ctx.(*frameContext); ok {
		// Nothing sits between ctx and its frame, so its writers are current.
		return fc.frame
	}
	f := *currentFrame(ctx)
	// Context layers above the frame (if any) may have installed writers, so
	// resolve them afresh. The lookups stop at the frame at the latest.
	f.stdout = ioctx.StdoutFromContext(ctx)
	f.stderr = ioctx.StderrFromContext(ctx)
	return f
}

// withFrame returns ctx with f pushed as its current frame.
func withFrame(ctx context.Context, f evalFrame) context.Context {
	return &frameContext{Context: ctx, frame: f}
}

// ensureFrame returns ctx with a frame, pushing a root frame if it has none,
// along with that frame. Evaluation entry points call it so a context without
// a frame is looked through once, not at every node.
func ensureFrame(ctx context.Context) (context.Context, *evalFrame) {
	if f := frameFrom(ctx); f != nil {
		return ctx, f
	}
	fc := &frameContext{Context: ctx, frame: deriveFrame(ctx)}
	return fc, &fc.frame
}
