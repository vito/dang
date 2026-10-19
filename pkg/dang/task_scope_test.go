package dang

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// A `{{ }}` task gets its own `self` cell, and its frame resolves names through
// it: field writes inside the task (either form) fork the task's `self`, never
// the enclosing method's, and bare and `self.` forms see each other's writes.
func TestTaskScopeSelfIsPrivate(t *testing.T) {
	ctx := context.Background()

	root := NewObject(NewType("Acc", ObjectKind))
	root.Bind("total", IntValue{Val: 0}, PublicVisibility)
	method := newSelfFrame(root.snapshotReceiver())
	method.Bind("arg", IntValue{Val: 1}, PrivateVisibility)
	body := method.Derive(false)
	before, _ := body.Self()

	task := forkTaskScope(body)
	self, ok := task.Self()
	require.True(t, ok)
	require.Same(t, before, self, "the task starts from the enclosing self")

	// `self.total = 5` inside the task.
	task.MutateSelf(forkWithField(self.(*Object), "total", IntValue{Val: 5}))
	after, _ := body.Self()
	require.Same(t, before, after, "the task's self. write leaked out")
	v, _, err := task.Lookup(ctx, "total")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 5}, v, "bare read in the task misses its self. write")

	// `total = 6` inside the task (from a nested block).
	task.Derive(false).Update("total", IntValue{Val: 6})
	inTask, _ := task.Self()
	v, _, err = inTask.(*Object).Lookup(ctx, "total")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 6}, v, "self. read in the task misses its bare write")
	after, _ = body.Self()
	require.Same(t, before, after, "the task's bare write leaked out")

	// Writes to the method's args stay in the task too, without touching self.
	task.Update("arg", IntValue{Val: 2})
	v, _, err = body.Lookup(ctx, "arg")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 1}, v, "the task's arg write leaked out")
	inTaskAfterArg, _ := task.Self()
	require.Same(t, inTask, inTaskAfterArg, "an arg write forked self")
}

// Concurrent bare field writes through one `self` cell (a block invoked from
// several `{{ }}` tasks) must each land: a fork is installed only over the
// generation it was built from, so neither write drops the other.
func TestConcurrentBareWritesKeepEveryFork(t *testing.T) {
	ctx := context.Background()
	const writers = 8

	for range 200 {
		root := NewObject(NewType("Acc", ObjectKind))
		for i := range writers {
			root.Bind(fieldName(i), IntValue{Val: 0}, PublicVisibility)
		}
		body := newSelfFrame(root.snapshotReceiver()).Derive(false)

		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				body.Update(fieldName(i), IntValue{Val: i + 1})
			}()
		}
		close(start)
		wg.Wait()

		for i := range writers {
			v, found, err := body.Lookup(ctx, fieldName(i))
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, IntValue{Val: i + 1}, v, "write to %s was lost", fieldName(i))
		}
	}
}

func fieldName(i int) string {
	return string(rune('a' + i))
}
