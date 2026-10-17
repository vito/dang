package dang

import (
	"context"
	"errors"
	"sync/atomic"
)

type ControlFrameKind int

const (
	NoControlFrame ControlFrameKind = iota
	ReturnFrame
	BlockCallFrame
	BlockInvocationFrame
)

type ControlFrame struct {
	ID     uint64
	Kind   ControlFrameKind
	Active bool
}

var nextControlFrameID uint64

func NewControlFrame(kind ControlFrameKind) *ControlFrame {
	return &ControlFrame{
		ID:     atomic.AddUint64(&nextControlFrameID, 1),
		Kind:   kind,
		Active: true,
	}
}

func (f *ControlFrame) Deactivate() {
	if f != nil {
		f.Active = false
	}
}

func currentReturnFrame(ctx context.Context) *ControlFrame {
	return currentFrame(ctx).returnFrame
}

func currentBreakFrame(ctx context.Context) *ControlFrame {
	return currentFrame(ctx).breakFrame
}

func currentContinueFrame(ctx context.Context) *ControlFrame {
	return currentFrame(ctx).continueFrame
}

func currentBlockCallFrame(ctx context.Context) *ControlFrame {
	return currentFrame(ctx).blockCallFrame
}

// enterFunctionControlBoundary makes f the frame of a function body: break,
// continue and block calls never target anything outside the function.
func (f *evalFrame) enterFunctionControlBoundary() {
	f.breakFrame = nil
	f.continueFrame = nil
	f.blockCallFrame = nil
}

func controlFrameMatches(target, frame *ControlFrame) bool {
	return target != nil && frame != nil && target == frame
}

type InferControlTarget struct {
	Kind ControlFrameKind
}

const (
	inferReturnTargetContextKey   contextKey = "inferReturnTarget"
	inferBreakTargetContextKey    contextKey = "inferBreakTarget"
	inferContinueTargetContextKey contextKey = "inferContinueTarget"
)

func NewInferControlTarget(kind ControlFrameKind) *InferControlTarget {
	return &InferControlTarget{Kind: kind}
}

func currentInferReturnTarget(ctx context.Context) *InferControlTarget {
	target, _ := ctx.Value(inferReturnTargetContextKey).(*InferControlTarget)
	return target
}

func currentInferBreakTarget(ctx context.Context) *InferControlTarget {
	target, _ := ctx.Value(inferBreakTargetContextKey).(*InferControlTarget)
	return target
}

func currentInferContinueTarget(ctx context.Context) *InferControlTarget {
	target, _ := ctx.Value(inferContinueTargetContextKey).(*InferControlTarget)
	return target
}

func contextWithInferReturnTarget(ctx context.Context, target *InferControlTarget) context.Context {
	return context.WithValue(ctx, inferReturnTargetContextKey, target)
}

func contextWithInferBreakTarget(ctx context.Context, target *InferControlTarget) context.Context {
	return context.WithValue(ctx, inferBreakTargetContextKey, target)
}

func contextWithInferContinueTarget(ctx context.Context, target *InferControlTarget) context.Context {
	return context.WithValue(ctx, inferContinueTargetContextKey, target)
}

func contextWithInferFunctionControlBoundary(ctx context.Context) context.Context {
	ctx = contextWithInferReturnTarget(ctx, nil)
	ctx = contextWithInferBreakTarget(ctx, nil)
	ctx = contextWithInferContinueTarget(ctx, nil)
	return ctx
}

func isBreakException(err error) bool {
	var br *BreakException
	return errors.As(err, &br)
}

func isContinueException(err error) bool {
	var cont *ContinueException
	return errors.As(err, &cont)
}

func isControlFlowException(err error) bool {
	return isReturnException(err) || isBreakException(err) || isContinueException(err)
}
