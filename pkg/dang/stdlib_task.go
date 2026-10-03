package dang

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vito/dang/v2/pkg/hm"
)

// Structured concurrency: `async { }` starts a Task bound to the innermost
// task scope (a nursery). A scope is opened by `tasks { }`, by every task
// body (so a task's own children are joined before it finishes), and by the
// host at the program's entrypoint (RunFile/RunDir here; the Dagger SDK per
// module-function call). When a scope exits it cancels every task still
// running and waits for all of them to unwind: no task outlives its scope.

// TaskTypeModule is the receiver for builtin methods on Task values.
var TaskTypeModule = NewType("Task", ScalarKind)

var (
	errTaskScopeExited = fmt.Errorf("task cancelled: the scope that started it exited: %w", context.Canceled)
	errTaskRaceLost    = fmt.Errorf("task cancelled: it lost a race: %w", context.Canceled)
	errTaskCancelled   = fmt.Errorf("task cancelled: %w", context.Canceled)
	errTaskTimedOut    = fmt.Errorf("task cancelled: it timed out: %w", context.Canceled)
)

type taskState struct {
	done     chan struct{}
	val      Value
	err      error
	cancel   context.CancelCauseFunc
	observed atomic.Bool
}

func (st *taskState) isDone() bool {
	select {
	case <-st.done:
		return true
	default:
		return false
	}
}

// await waits for the task. Cancelling the waiter does not cancel the task:
// the task belongs to its scope, not to whoever awaits it.
func (st *taskState) await(ctx context.Context) (Value, error) {
	st.observed.Store(true)
	select {
	case <-st.done:
		return st.val, st.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// stop cancels the task and waits for it to unwind.
func (st *taskState) stop(cause error) {
	st.observed.Store(true)
	st.cancel(cause)
	<-st.done
}

// TaskValue is a handle to a running (or finished) task. Copying the handle
// copies the reference; equality is identity.
type TaskValue struct {
	st         *taskState
	ResultType hm.Type
}

var _ Value = TaskValue{}

func (t TaskValue) Type() hm.Type {
	return hm.NonNullType{Type: TaskType{t.ResultType}}
}

func (t TaskValue) String() string {
	if t.st.isDone() {
		if t.st.err != nil {
			return "task(failed)"
		}
		return "task(done)"
	}
	return "task(running)"
}

func (t TaskValue) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("cannot marshal a task: a task lives only as long as the scope that started it")
}

type taskScope struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	mu     sync.Mutex
	tasks  []*taskState
	closed bool
}

type taskScopeKey struct{}

func taskScopeFrom(ctx context.Context) *taskScope {
	s, _ := ctx.Value(taskScopeKey{}).(*taskScope)
	return s
}

// ContextWithTaskScope opens a task scope (nursery). The returned close
// function must be called with the body's error when the body exits: it
// cancels every task still running, waits for all of them, and returns the
// body's error, or else the first failure of a task nobody observed (so
// errors never vanish silently).
func ContextWithTaskScope(ctx context.Context) (context.Context, func(bodyErr error) error) {
	s := &taskScope{}
	s.ctx, s.cancel = context.WithCancelCause(ctx)
	return context.WithValue(ctx, taskScopeKey{}, s), s.close
}

func (s *taskScope) close(bodyErr error) error {
	s.mu.Lock()
	s.closed = true
	tasks := slices.Clone(s.tasks)
	s.mu.Unlock()

	s.cancel(errTaskScopeExited)
	var unobserved error
	for _, t := range tasks {
		<-t.done
		if unobserved == nil && !t.observed.Load() && t.err != nil && !errors.Is(t.err, context.Canceled) {
			unobserved = t.err
		}
	}
	if bodyErr != nil {
		return bodyErr
	}
	return unobserved
}

func (s *taskScope) spawn(callCtx context.Context, run func(ctx context.Context) (Value, error)) (*taskState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("async: the task scope has already exited")
	}

	// Keep the call site's context values (stdout, services, imports), but
	// take cancellation from the scope: the task belongs to the scope, not to
	// the expression that started it.
	tctx, tcancel := context.WithCancelCause(context.WithoutCancel(callCtx))
	stop := context.AfterFunc(s.ctx, func() { tcancel(context.Cause(s.ctx)) })

	st := &taskState{done: make(chan struct{}), cancel: tcancel}
	s.tasks = append(s.tasks, st)

	tctx, closeChildren := ContextWithTaskScope(tctx)
	go func() {
		defer close(st.done)
		defer stop()
		defer tcancel(nil)
		v, err := run(tctx)
		if err := closeChildren(err); err != nil {
			st.err = err
			return
		}
		st.val = v
	}()
	return st, nil
}

func taskResultType(t hm.Type) (hm.Type, bool) {
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}
	if tt, ok := t.(TaskType); ok {
		return tt.Type, true
	}
	return nil, false
}

func taskReceiverResult(lt hm.Type) (res hm.Type, nullable bool, ok bool) {
	if nn, isNN := lt.(hm.NonNullType); isNN {
		if tt, isTask := nn.Type.(TaskType); isTask {
			return tt.Type, false, true
		}
		return nil, false, false
	}
	if tt, isTask := lt.(TaskType); isTask {
		return tt.Type, true, true
	}
	return nil, false, false
}

func tasksOf(self Value, method string) ([]TaskValue, error) {
	list, ok := self.(ListValue)
	if !ok {
		return nil, fmt.Errorf("%s: expected a list of tasks, got %T", method, self)
	}
	tasks := make([]TaskValue, len(list.Elements))
	for i, el := range list.Elements {
		t, ok := el.(TaskValue)
		if !ok {
			return nil, fmt.Errorf("%s: expected a list of tasks, got %s at index %d", method, el.Type(), i)
		}
		tasks[i] = t
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("%s: no tasks, so nothing can ever finish", method)
	}
	return tasks, nil
}

// firstDone returns the index of the first task to finish. Tasks that are
// already finished win in list order, so racing settled tasks is
// deterministic; otherwise the scheduler decides.
func firstDone(ctx context.Context, tasks []TaskValue) (int, error) {
	for i, t := range tasks {
		if t.st.isDone() {
			return i, nil
		}
	}
	won := make(chan int, len(tasks))
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for i, t := range tasks {
		go func() {
			select {
			case <-t.st.done:
				won <- i
			case <-wctx.Done():
			}
		}()
	}
	select {
	case i := <-won:
		return i, nil
	case <-ctx.Done():
		return -1, context.Cause(ctx)
	}
}

func registerTasks() {
	Builtin("async").
		Doc("starts the block as a concurrent task bound to the enclosing task scope; the scope cancels it if it is still running when the scope exits").
		Example(`tasks { async { 21 * 2 }.await }`).
		Block(hm.NewFnType(NewRecordType(""), TypeVar('a'))).
		Returns(NonNull(TaskType{TypeVar('a')})).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			if args.Block == nil {
				return nil, fmt.Errorf("async requires a block argument")
			}
			s := taskScopeFrom(ctx)
			if s == nil {
				return nil, fmt.Errorf("async: no task scope; wrap the code in tasks { ... }")
			}
			fn := *args.Block
			// A task is not part of the caller's control flow: break and
			// return inside it have nowhere to go.
			fn.CapturedReturnFrame = nil
			fn.CapturedBreakFrame = nil
			// Writes inside the task stay private to it, like a {{ }} field.
			closure := fn.Closure.Derive(true)
			if self, ok := fn.Closure.Self(); ok {
				closure.EnterSelf(self)
			}
			fn.Closure = closure

			st, err := s.spawn(ctx, func(tctx context.Context) (Value, error) {
				return callFunc(tctx, fn)
			})
			if err != nil {
				return nil, err
			}
			return TaskValue{st: st, ResultType: fn.FnType.Ret(false)}, nil
		})

	Builtin("tasks").
		Doc("runs the block in a new task scope: tasks started inside it are cancelled and awaited when the block exits").
		Example(`tasks { let a = async { 1 }; let b = async { 2 }; a.await + b.await }`).
		Block(hm.NewFnType(NewRecordType(""), TypeVar('a'))).
		Returns(TypeVar('a')).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			if args.Block == nil {
				return nil, fmt.Errorf("tasks requires a block argument")
			}
			sctx, closeScope := ContextWithTaskScope(ctx)
			v, err := callFunc(sctx, *args.Block)
			if err := closeScope(err); err != nil {
				return nil, err
			}
			return v, nil
		})

	Builtin("sleep").
		Doc("waits for the given duration (Go syntax: \"300ms\", \"5m\", \"1h30m\"); returns early with an error if cancelled").
		Example(`sleep("1ms")`).
		Params("duration", NonNull(StringType)).
		Returns(TypeVar('n')).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			raw := args.GetString("duration")
			d, err := time.ParseDuration(raw)
			if err != nil {
				return nil, fmt.Errorf("sleep: invalid duration %q: %w", raw, err)
			}
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-timer.C:
				return NullValue{}, nil
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			}
		})

	Method(TaskTypeModule, "await").
		Doc("waits for the task and returns its value, or raises its error").
		Example(`tasks { async { "hi" }.await }`).
		Returns(TypeVar('a')).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return self.(TaskValue).st.await(ctx)
		})

	Method(TaskTypeModule, "isDone").
		Doc("reports whether the task has finished, without waiting").
		Example(`tasks { let t = async { 1 }; t.await; t.isDone }`).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return BoolValue{Val: self.(TaskValue).st.isDone()}, nil
		})

	Method(TaskTypeModule, "cancel").
		Doc("cancels the task and waits for it to unwind").
		Example(`tasks { async { sleep("1h") }.cancel }`).
		Returns(TypeVar('n')).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			self.(TaskValue).st.stop(errTaskCancelled)
			return NullValue{}, nil
		})

	Method(TaskTypeModule, "timeout").
		Doc("waits for the task for at most the given duration; on timeout the task is cancelled and the result is null").
		Example(`tasks { async { sleep("1h"); 1 }.timeout("1ms") }`).
		Params("duration", NonNull(StringType)).
		Returns(Nullable(TypeVar('a'))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			st := self.(TaskValue).st
			raw := args.GetString("duration")
			d, err := time.ParseDuration(raw)
			if err != nil {
				return nil, fmt.Errorf("timeout: invalid duration %q: %w", raw, err)
			}
			st.observed.Store(true)
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-st.done:
				return st.val, st.err
			case <-timer.C:
				st.stop(errTaskTimedOut)
				return NullValue{}, nil
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			}
		})

	Method(ListTypeModule, "race").
		Doc("waits for the first task to finish, cancels (and awaits) the rest, and returns the winner's value or raises its error").
		Example(`tasks { [async { sleep("1h"); 1 }, async { 2 }].race }`).
		Returns(TypeVar('t')).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			tasks, err := tasksOf(self, "race")
			if err != nil {
				return nil, err
			}
			i, err := firstDone(ctx, tasks)
			if err != nil {
				return nil, err
			}
			for j, t := range tasks {
				if j != i {
					t.st.stop(errTaskRaceLost)
				}
			}
			return tasks[i].st.await(ctx)
		})

	Method(ListTypeModule, "awaitAny").
		Doc("waits for the first task to finish and returns that task, leaving the others running").
		Example(`tasks { [async { sleep("1h"); 1 }, async { 2 }].awaitAny.await }`).
		Returns(TypeVar('a')).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			tasks, err := tasksOf(self, "awaitAny")
			if err != nil {
				return nil, err
			}
			i, err := firstDone(ctx, tasks)
			if err != nil {
				return nil, err
			}
			return tasks[i], nil
		})

	Method(ListTypeModule, "awaitAll").
		Doc("waits for every task and returns their values in order; the first failure cancels the rest and is raised").
		Example(`tasks { [async { 1 }, async { 2 }].awaitAll }`).
		Returns(NonNull(ListOf(TypeVar('t')))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			tasks, err := tasksOf(self, "awaitAll")
			if err != nil {
				return nil, err
			}
			results := make([]Value, len(tasks))
			pending := slices.Clone(tasks)
			idx := make([]int, len(tasks))
			for i := range idx {
				idx[i] = i
			}
			for len(pending) > 0 {
				k, err := firstDone(ctx, pending)
				if err != nil {
					return nil, err
				}
				v, err := pending[k].st.await(ctx)
				if err != nil {
					for j, t := range pending {
						if j != k {
							t.st.stop(errTaskCancelled)
						}
					}
					return nil, err
				}
				results[idx[k]] = v
				pending = slices.Delete(pending, k, k+1)
				idx = slices.Delete(idx, k, k+1)
			}
			var elemType hm.Type
			if res, ok := taskResultType(tasks[0].Type()); ok {
				elemType = res
			}
			return ListValue{Elements: results, ElemType: elemType}, nil
		})

	Method(ListTypeModule, "eachCompleted").
		Doc("calls the block with each task's value in completion order; a failed task raises").
		Example(`tasks { [async { sleep("5ms"); 1 }, async { 2 }].eachCompleted { v => print(v) } }`).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "value",
				Value: hm.NewScheme(nil, TypeVar('t')),
			}),
			TypeVar('b'),
		)).
		Returns(TypeVar('n')).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			if args.Block == nil {
				return nil, fmt.Errorf("eachCompleted requires a block argument")
			}
			tasks, err := tasksOf(self, "eachCompleted")
			if err != nil {
				return nil, err
			}
			pending := slices.Clone(tasks)
			for len(pending) > 0 {
				k, err := firstDone(ctx, pending)
				if err != nil {
					return nil, err
				}
				v, err := pending[k].st.await(ctx)
				if err != nil {
					return nil, err
				}
				pending = slices.Delete(pending, k, k+1)
				if _, err := callFunc(ctx, *args.Block, v); err != nil {
					return nil, err
				}
			}
			return NullValue{}, nil
		})
}
