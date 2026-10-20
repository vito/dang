package dang

import (
	"context"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

// Walking an Object's Parent chain used to recurse once per layer, so a deep
// enough chain overflowed the Go stack (a runaway self-recursive method crashed
// with millions of Object.Lookup frames). The walks are loops now: under a
// small stack cap, every one of them still reaches the root of a chain far
// deeper than that cap could hold as recursion.
func TestDeepParentChainWalksAreIterative(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(4 << 20))

	const depth = 200_000
	root := NewObject(NewType("Root", ObjectKind))
	root.Bind("rootVal", IntValue{Val: 1}, PublicVisibility)
	root.BindLazy("rootLazy", func(context.Context) (Value, error) {
		return IntValue{Val: 2}, nil
	}, PublicVisibility)

	var leaf ValueScope = root
	for range depth {
		leaf = leaf.Derive(false)
	}
	leafObj := leaf.(*Object)
	// Installed after deriving, so no layer above shares the root's cell and
	// Self has to walk the whole chain to find it.
	root.EnterSelf(root)

	ctx := context.Background()
	val, found, err := leaf.Lookup(ctx, "rootLazy")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, IntValue{Val: 2}, val)

	val, found, err = leaf.Lookup(ctx, "missing")
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, val)

	require.True(t, leaf.Has("rootVal"))
	require.False(t, leaf.Has("missing"))

	val, found = leafObj.lookupValue("rootVal")
	require.True(t, found)
	require.Equal(t, IntValue{Val: 1}, val)

	require.Equal(t, PublicVisibility, leaf.Visibility("rootVal"))
	require.Equal(t, PrivateVisibility, leaf.Visibility("missing"))

	self, ok := leaf.Self()
	require.True(t, ok)
	require.Same(t, root, self)

	bindings := leaf.Bindings(PublicVisibility)
	require.Len(t, bindings, 2)

	// An unsealed chain updates the binding where it lives (the root), and a
	// name bound nowhere lands on the layer that was written to.
	leaf.Update("rootVal", IntValue{Val: 3})
	got, _ := root.LookupLocal("rootVal")
	require.Equal(t, IntValue{Val: 3}, got)
	_, local := leaf.LookupLocal("rootVal")
	require.False(t, local)

	leaf.Update("fresh", IntValue{Val: 4})
	got, local = leaf.LookupLocal("fresh")
	require.True(t, local)
	require.Equal(t, IntValue{Val: 4}, got)
	require.False(t, root.Has("fresh"))
}

// Update stops at a sealed layer: the write lands there even when the name is
// bound further up the chain.
func TestUpdateStopsAtSealedLayer(t *testing.T) {
	root := NewObject(NewType("Root", ObjectKind))
	root.Bind("x", IntValue{Val: 1}, PublicVisibility)
	sealed := root.Derive(true)
	leaf := sealed.Derive(false).Derive(false)

	leaf.Update("x", IntValue{Val: 2})

	got, _ := root.LookupLocal("x")
	require.Equal(t, IntValue{Val: 1}, got, "the write leaked past the sealed layer")
	got, local := sealed.LookupLocal("x")
	require.True(t, local)
	require.Equal(t, IntValue{Val: 2}, got)
	require.Equal(t, PublicVisibility, sealed.Visibility("x"))
}

// Update learns that a chain binds a name once and then walks up without
// re-checking, except at a layer holding a pending initializer for it: that
// is what made the chain bind the name, so it says nothing about the layers
// above, and a name bound nowhere above it is written on that layer, as the
// recursive walk did. (This pins only where the walk stops. The write leaves
// the layer's pending initializer in place, which Lookup still prefers.)
func TestUpdateRechecksAtPendingLayer(t *testing.T) {
	root := NewObject(NewType("Root", ObjectKind))
	mid := root.Derive(false).Derive(false).(*Object)
	mid.BindLazy("x", func(context.Context) (Value, error) {
		return IntValue{Val: 1}, nil
	}, PublicVisibility)
	leaf := mid.Derive(false).Derive(false).Derive(false)

	leaf.Update("x", IntValue{Val: 2})

	require.False(t, root.Has("x"), "the write walked past the pending layer")
	_, local := mid.LookupLocal("x")
	require.True(t, local, "the write should stop at the pending layer")
}
