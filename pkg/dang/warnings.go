package dang

import (
	"context"
	"sync"
)

// InferWarning is a non-fatal diagnostic produced during type inference.
// Unlike an InferError it never aborts the run; it points at code that type
// checks but very likely does not do what the author intended.
type InferWarning struct {
	Message  string
	Location *SourceLocation
}

// InferWarnings collects warnings produced during an inference pass. When a
// sink is attached to the context (WithInferWarningSink), EmitInferWarning
// appends to it instead of printing, so drivers that re-run inference
// repeatedly — the LSP re-infers on every keystroke — can surface warnings
// as diagnostics rather than spamming stderr.
type InferWarnings struct {
	mu   sync.Mutex
	list []InferWarning
}

func (w *InferWarnings) add(warning InferWarning) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.list = append(w.list, warning)
}

// Take returns the warnings collected so far and resets the sink.
func (w *InferWarnings) Take() []InferWarning {
	w.mu.Lock()
	defer w.mu.Unlock()
	list := w.list
	w.list = nil
	return list
}

type inferWarningSinkKey struct{}

// WithInferWarningSink attaches a warning collector to the context and
// returns it. Inference warnings emitted under the returned context are
// collected instead of printed.
func WithInferWarningSink(ctx context.Context) (context.Context, *InferWarnings) {
	sink := &InferWarnings{}
	return context.WithValue(ctx, inferWarningSinkKey{}, sink), sink
}

// EmitInferWarning reports a non-fatal inference diagnostic. With a sink
// attached it collects; otherwise it prints to the context's stderr with a
// highlighted source snippet, the same rendering as eval-time warnings.
func EmitInferWarning(ctx context.Context, node SourceLocatable, message string) {
	var loc *SourceLocation
	if node != nil {
		loc = node.GetSourceLocation()
	}
	if sink, ok := ctx.Value(inferWarningSinkKey{}).(*InferWarnings); ok {
		sink.add(InferWarning{Message: message, Location: loc})
		return
	}
	WarnAtSource(ctx, loc, message)
}
