package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

type lazySlotState int

const (
	lazySlotPending lazySlotState = iota
	lazySlotEvaluating
	lazySlotDone
)

// lazySlotValue is a module-level runtime initializer installed before its
// value is evaluated. It is forced on first read, caches the result, and
// detects real initialization cycles.
type lazySlotValue struct {
	Name       string
	Slot       *SlotDecl
	Env        EvalEnv
	Visibility Visibility
	State      lazySlotState
	Value      Value
}

var _ Value = (*lazySlotValue)(nil)

func (l *lazySlotValue) Type() hm.Type {
	if l.Value != nil {
		return l.Value.Type()
	}
	if l.Slot != nil {
		return l.Slot.GetInferredType()
	}
	return nil
}

func (l *lazySlotValue) String() string {
	if l.Value != nil {
		return l.Value.String()
	}
	return fmt.Sprintf("<lazy %s>", l.Name)
}

func forceLazyValue(ctx context.Context, val Value) (Value, error) {
	for {
		lazy, ok := val.(*lazySlotValue)
		if !ok {
			return val, nil
		}
		forced, err := forceLazySlot(ctx, lazy)
		if err != nil {
			return nil, err
		}
		val = forced
	}
}

func forceLazySlot(ctx context.Context, lazy *lazySlotValue) (Value, error) {
	switch lazy.State {
	case lazySlotDone:
		return lazy.Value, nil
	case lazySlotEvaluating:
		return nil, fmt.Errorf("initialization cycle while evaluating variable %q", lazy.Name)
	case lazySlotPending:
		// continue below
	default:
		return nil, fmt.Errorf("invalid lazy slot state %d for variable %q", lazy.State, lazy.Name)
	}

	if lazy.Slot == nil {
		return nil, fmt.Errorf("lazy variable %q has no slot declaration", lazy.Name)
	}
	if lazy.Slot.Value == nil {
		return nil, fmt.Errorf("lazy variable %q has no initializer", lazy.Name)
	}

	lazy.State = lazySlotEvaluating
	val, err := EvalNode(ctx, lazy.Env, lazy.Slot.Value)
	if err != nil {
		lazy.State = lazySlotPending
		return nil, err
	}
	if val == nil {
		lazy.State = lazySlotPending
		return nil, fmt.Errorf("initializer for variable %q returned nil", lazy.Name)
	}

	lazy.Value = val
	lazy.State = lazySlotDone
	lazy.Env.SetWithVisibility(lazy.Name, val, lazy.Visibility)
	return val, nil
}
