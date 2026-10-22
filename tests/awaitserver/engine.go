package awaitserver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// This file is the fake "engine": the state awaitables observe and the
// race awaitAny runs. IDs are recipes, as in Dagger: an ID names a fact
// (a deadline, the Nth mailbox event, a build) and never blocks to compute;
// only syncing it does.

var (
	mailboxesMu   sync.Mutex
	mailboxes     = map[string]*Mailbox{}
	mailboxSeq    int
	cancellations atomic.Int64
)

// Timer is a deadline. It is pure: the same instant is the same Timer.
type Timer struct {
	at time.Time
}

func (t *Timer) ID() string      { return "timer:" + t.At() }
func (t *Timer) At() string      { return t.at.UTC().Format(time.RFC3339Nano) }
func (t *Timer) IsNode()         {}
func (t *Timer) IsSyncer()       {}
func (t *Timer) GetID() string   { return t.ID() }
func (t *Timer) GetSync() string { return t.ID() }

// Build stands in for an evaluator such as a Container: syncing it does work,
// which a lost race cancels.
type Build struct {
	Name     string
	duration time.Duration
	fail     bool
}

func (b *Build) ID() string {
	return fmt.Sprintf("build:%s:%s:%t", b.Name, b.duration, b.fail)
}
func (b *Build) IsNode()         {}
func (b *Build) IsSyncer()       {}
func (b *Build) GetID() string   { return b.ID() }
func (b *Build) GetSync() string { return b.ID() }

// Mailbox is an append-only event log. Reads take a cursor and never consume.
type Mailbox struct {
	id      string
	Name    string
	mu      sync.Mutex
	events  []mailboxEvent
	changed chan struct{}
}

type mailboxEvent struct {
	kind  MailboxEventKind
	label string
	text  string
}

func (m *Mailbox) ID() string    { return m.id }
func (m *Mailbox) IsNode()       {}
func (m *Mailbox) GetID() string { return m.id }

func (m *Mailbox) append(kind MailboxEventKind, label, text string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, mailboxEvent{kind: kind, label: label, text: text})
	close(m.changed)
	m.changed = make(chan struct{})
	return len(m.events)
}

// waitAfter blocks until an event with seq > after exists and returns it.
func (m *Mailbox) waitAfter(ctx context.Context, after int) (int, mailboxEvent, error) {
	for {
		m.mu.Lock()
		if len(m.events) > after {
			ev := m.events[after]
			m.mu.Unlock()
			return after + 1, ev, nil
		}
		ch := m.changed
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return 0, mailboxEvent{}, context.Cause(ctx)
		case <-ch:
		}
	}
}

func (m *Mailbox) ready(after int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events) > after
}

func newMailbox(name string) *Mailbox {
	mailboxesMu.Lock()
	defer mailboxesMu.Unlock()
	mailboxSeq++
	m := &Mailbox{
		id:      fmt.Sprintf("mailbox:%d", mailboxSeq),
		Name:    name,
		changed: make(chan struct{}),
	}
	mailboxes[m.id] = m
	return m
}

func lookupMailbox(id string) (*Mailbox, error) {
	mailboxesMu.Lock()
	defer mailboxesMu.Unlock()
	m, ok := mailboxes[id]
	if !ok {
		return nil, fmt.Errorf("no mailbox %q in this session", id)
	}
	return m, nil
}

// MailboxEvent names "the first event after `after`" in a mailbox. It exists
// as a name immediately; its fields block until the event does.
type MailboxEvent struct {
	box   *Mailbox
	after int
}

func (e *MailboxEvent) ID() string      { return fmt.Sprintf("next:%s:%d", e.box.id, e.after) }
func (e *MailboxEvent) IsNode()         {}
func (e *MailboxEvent) IsSyncer()       {}
func (e *MailboxEvent) GetID() string   { return e.ID() }
func (e *MailboxEvent) GetSync() string { return e.ID() }

// load turns a recipe ID back into its object, like Dagger's node(id:).
func load(id string) (any, error) {
	kind, rest, _ := strings.Cut(id, ":")
	switch kind {
	case "timer":
		at, err := time.Parse(time.RFC3339Nano, rest)
		if err != nil {
			return nil, err
		}
		return &Timer{at: at}, nil
	case "build":
		parts := strings.Split(rest, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("bad build id %q", id)
		}
		d, err := time.ParseDuration(parts[1])
		if err != nil {
			return nil, err
		}
		return &Build{Name: parts[0], duration: d, fail: parts[2] == "true"}, nil
	case "mailbox":
		return lookupMailbox(id)
	case "next":
		i := strings.LastIndex(rest, ":")
		if i < 0 {
			return nil, fmt.Errorf("bad event id %q", id)
		}
		box, err := lookupMailbox(rest[:i])
		if err != nil {
			return nil, err
		}
		after, err := strconv.Atoi(rest[i+1:])
		if err != nil {
			return nil, err
		}
		return &MailboxEvent{box: box, after: after}, nil
	}
	return nil, fmt.Errorf("unknown id %q", id)
}

// ready is the non-blocking pre-pass: observers whose fact already holds win
// in index order, deterministically. Evaluators are never "ready" here.
func ready(obj any) bool {
	switch o := obj.(type) {
	case *Timer:
		return !time.Now().Before(o.at)
	case *MailboxEvent:
		return o.box.ready(o.after)
	}
	return false
}

// await blocks until the awaitable's fact exists.
func await(ctx context.Context, obj any) error {
	switch o := obj.(type) {
	case *Timer:
		return sleepUntil(ctx, o.at)
	case *MailboxEvent:
		_, _, err := o.box.waitAfter(ctx, o.after)
		return err
	case *Build:
		if err := sleepUntil(ctx, time.Now().Add(o.duration)); err != nil {
			cancellations.Add(1)
			return err
		}
		if o.fail {
			return fmt.Errorf("build %s failed", o.Name)
		}
		return nil
	}
	return fmt.Errorf("%T is not awaitable", obj)
}

func sleepUntil(ctx context.Context, at time.Time) error {
	d := time.Until(at)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-t.C:
		return nil
	}
}

var errTimedOut = errors.New("await timed out")

// awaitAny is the engine-side race. Structured: every loser is cancelled and
// has returned before the result does.
func awaitAny(ctx context.Context, ids []string, timeout *string, mode AwaitMode) (*int, error) {
	if len(ids) == 0 {
		return nil, errors.New("awaitAny: nothing to await")
	}
	objs := make([]any, len(ids))
	for i, id := range ids {
		obj, err := load(id)
		if err != nil {
			return nil, err
		}
		objs[i] = obj
	}
	for i, obj := range objs {
		if ready(obj) {
			return &i, nil
		}
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if timeout != nil {
		d, err := time.ParseDuration(*timeout)
		if err != nil {
			return nil, err
		}
		var stop context.CancelFunc
		ctx, stop = context.WithTimeoutCause(ctx, d, errTimedOut)
		defer stop()
	}

	type result struct {
		i   int
		err error
	}
	results := make(chan result, len(objs))
	var wg sync.WaitGroup
	for i, obj := range objs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- result{i, await(ctx, obj)}
		}()
	}

	var winner *int
	var failure error
	failed := 0
	for range objs {
		r := <-results
		if r.err == nil {
			winner = &r.i
			break
		}
		if errors.Is(r.err, errTimedOut) || errors.Is(r.err, context.Canceled) {
			if errors.Is(context.Cause(ctx), errTimedOut) {
				break
			}
			failure = r.err
			break
		}
		failed++
		if mode == AwaitModeFirstSettled || failed == len(objs) {
			failure = fmt.Errorf("awaitable %d: %w", r.i, r.err)
			break
		}
	}
	cancel(errors.New("lost the race"))
	wg.Wait()
	if winner != nil {
		return winner, nil
	}
	return nil, failure
}

func schedule(m *Mailbox, label string, after time.Duration, every *time.Duration) {
	go func() {
		next := time.Now().Add(after)
		for n := 0; n < 1000; n++ {
			time.Sleep(time.Until(next))
			m.append(MailboxEventKindTick, label, "")
			if every == nil {
				return
			}
			// fixed rate: skip missed ticks rather than bursting
			for next = next.Add(*every); time.Until(next) < 0; next = next.Add(*every) {
			}
		}
	}()
}
