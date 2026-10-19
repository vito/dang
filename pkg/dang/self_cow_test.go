package dang

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// selfDepth counts the frames above an instance's root.
func selfDepth(obj *Object) int {
	depth := 0
	for obj.Parent != nil {
		depth++
		obj = obj.Parent
	}
	return depth
}

// Bare field writes inside a method fork `self` rather than writing a receiver
// frame in place, and successive writes must not grow self's frame chain: a
// loop of N writes would otherwise make every read of an unwritten field walk
// N frames.
func TestBareFieldWritesForkSelfWithoutDeepening(t *testing.T) {
	ctx := context.Background()

	root := NewObject(NewType("Acc", ObjectKind))
	root.Bind("total", IntValue{Val: 0}, PublicVisibility)
	root.Bind("limit", IntValue{Val: 10}, PublicVisibility)

	recv := root.snapshotReceiver()
	frame := newSelfFrame(recv)
	body := frame.Derive(false) // a block inside the method body

	var gens []*Object
	for i := 1; i <= 100; i++ {
		body.Update("total", IntValue{Val: i})
		self, ok := body.Self()
		require.True(t, ok)
		gens = append(gens, self.(*Object))
	}

	for i, gen := range gens {
		// Every generation keeps its own value: none was written in place.
		v, found, err := gen.Lookup(ctx, "total")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, IntValue{Val: i + 1}, v)
		require.LessOrEqual(t, selfDepth(gen), selfDepth(recv))
	}

	// The receiver is untouched, and unwritten fields still resolve.
	v, _, err := recv.Lookup(ctx, "total")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 0}, v)
	v, _, err = body.Lookup(ctx, "limit")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 10}, v)
	v, _, err = body.Lookup(ctx, "total")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 100}, v)
}
