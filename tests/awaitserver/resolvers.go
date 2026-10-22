package awaitserver

// THIS CODE WILL BE UPDATED WITH SCHEMA CHANGES. PREVIOUS IMPLEMENTATION FOR SCHEMA CHANGES WILL BE KEPT IN THE COMMENT SECTION. IMPLEMENTATION FOR UNCHANGED SCHEMA WILL BE KEPT.

import (
	"context"
	"fmt"
	"time"
)

type Resolver struct{}

// Sync is the resolver for the sync field.
func (r *buildResolver) Sync(ctx context.Context, obj *Build) (string, error) {
	if err := await(ctx, obj); err != nil {
		return "", err
	}
	return obj.ID(), nil
}

// Send is the resolver for the send field.
func (r *mailboxResolver) Send(ctx context.Context, obj *Mailbox, message string) (int, error) {
	return obj.append(MailboxEventKindMessage, "", message), nil
}

// Schedule is the resolver for the schedule field.
func (r *mailboxResolver) Schedule(ctx context.Context, obj *Mailbox, label string, after string, every *string) (bool, error) {
	a, err := time.ParseDuration(after)
	if err != nil {
		return false, err
	}
	var e *time.Duration
	if every != nil {
		d, err := time.ParseDuration(*every)
		if err != nil {
			return false, err
		}
		e = &d
	}
	schedule(obj, label, a, e)
	return true, nil
}

// Next is the resolver for the next field. Naming the event never blocks.
func (r *mailboxResolver) Next(ctx context.Context, obj *Mailbox, after int) (*MailboxEvent, error) {
	return &MailboxEvent{box: obj, after: after}, nil
}

func (r *mailboxEventResolver) event(ctx context.Context, obj *MailboxEvent) (int, mailboxEvent, error) {
	return obj.box.waitAfter(ctx, obj.after)
}

// Seq is the resolver for the seq field.
func (r *mailboxEventResolver) Seq(ctx context.Context, obj *MailboxEvent) (int, error) {
	seq, _, err := r.event(ctx, obj)
	return seq, err
}

// Kind is the resolver for the kind field.
func (r *mailboxEventResolver) Kind(ctx context.Context, obj *MailboxEvent) (MailboxEventKind, error) {
	_, ev, err := r.event(ctx, obj)
	return ev.kind, err
}

// Label is the resolver for the label field.
func (r *mailboxEventResolver) Label(ctx context.Context, obj *MailboxEvent) (string, error) {
	_, ev, err := r.event(ctx, obj)
	return ev.label, err
}

// Text is the resolver for the text field.
func (r *mailboxEventResolver) Text(ctx context.Context, obj *MailboxEvent) (string, error) {
	_, ev, err := r.event(ctx, obj)
	return ev.text, err
}

// Sync is the resolver for the sync field.
func (r *mailboxEventResolver) Sync(ctx context.Context, obj *MailboxEvent) (string, error) {
	if _, _, err := r.event(ctx, obj); err != nil {
		return "", err
	}
	return obj.ID(), nil
}

// Node is the resolver for the node field.
func (r *queryResolver) Node(ctx context.Context, id string) (Node, error) {
	obj, err := load(id)
	if err != nil {
		return nil, err
	}
	node, ok := obj.(Node)
	if !ok {
		return nil, fmt.Errorf("%T is not a Node", obj)
	}
	return node, nil
}

// Sleep is the resolver for the sleep field.
func (r *queryResolver) Sleep(ctx context.Context, duration string) (bool, error) {
	d, err := time.ParseDuration(duration)
	if err != nil {
		return false, err
	}
	if err := sleepUntil(ctx, time.Now().Add(d)); err != nil {
		return false, err
	}
	return true, nil
}

// Timer is the resolver for the timer field: a relative deadline, pinned to
// its absolute instant so the returned ID replays as the same deadline.
func (r *queryResolver) Timer(ctx context.Context, after string) (string, error) {
	d, err := time.ParseDuration(after)
	if err != nil {
		return "", err
	}
	return (&Timer{at: time.Now().Add(d)}).ID(), nil
}

// Mailbox is the resolver for the mailbox field.
func (r *queryResolver) Mailbox(ctx context.Context, name string) (string, error) {
	return newMailbox(name).ID(), nil
}

// Build is the resolver for the build field.
func (r *queryResolver) Build(ctx context.Context, name string, duration string, fail *bool) (*Build, error) {
	d, err := time.ParseDuration(duration)
	if err != nil {
		return nil, err
	}
	return &Build{Name: name, duration: d, fail: fail != nil && *fail}, nil
}

// AwaitAny is the resolver for the awaitAny field.
func (r *queryResolver) AwaitAny(ctx context.Context, of []string, timeout *string, mode *AwaitMode) (*int, error) {
	m := AwaitModeFirstSettled
	if mode != nil {
		m = *mode
	}
	return awaitAny(ctx, of, timeout, m)
}

// Cancellations is the resolver for the cancellations field.
func (r *queryResolver) Cancellations(ctx context.Context) (int, error) {
	return int(cancellations.Load()), nil
}

// Sync is the resolver for the sync field.
func (r *timerResolver) Sync(ctx context.Context, obj *Timer) (string, error) {
	if err := await(ctx, obj); err != nil {
		return "", err
	}
	return obj.ID(), nil
}

// Build returns BuildResolver implementation.
func (r *Resolver) Build() BuildResolver { return &buildResolver{r} }

// Mailbox returns MailboxResolver implementation.
func (r *Resolver) Mailbox() MailboxResolver { return &mailboxResolver{r} }

// MailboxEvent returns MailboxEventResolver implementation.
func (r *Resolver) MailboxEvent() MailboxEventResolver { return &mailboxEventResolver{r} }

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

// Timer returns TimerResolver implementation.
func (r *Resolver) Timer() TimerResolver { return &timerResolver{r} }

type buildResolver struct{ *Resolver }
type mailboxResolver struct{ *Resolver }
type mailboxEventResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }
type timerResolver struct{ *Resolver }
