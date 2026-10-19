package dang

import (
	"bytes"
	"context"
	"testing"

	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/ioctx"
)

// chainDepth counts the frames on an object's parent chain.
func chainDepth(o *Object) int {
	depth := 0
	for ; o != nil; o = o.Parent {
		depth++
	}
	return depth
}

// Repeated copy-on-write field writes — through a path (`a.x += 1`,
// `self.inner.x += 1`) or bare (`x += 1` in a method or new()) — must not grow
// the written value's parent chain: every read of a field the writes didn't
// touch, and every Bindings/MarshalJSON/equality walk, would otherwise cost
// O(writes so far).
func TestCopyOnWriteKeepsChainsShallow(t *testing.T) {
	const src = `
		type Inner { x: Int! }
		type Foo {
			x: Int!
			inner: Inner! = Inner(0)

			bump(n: Int!): Foo! {
				let i = 0
				loop {
					if (i >= n) { break }
					self.x += 1
					self.inner.x += 1
					i += 1
				}
				self
			}

			bumpBare(n: Int!): Foo! {
				let i = 0
				loop {
					if (i >= n) { break }
					x += 1
					i += 1
				}
				self
			}
		}

		type Built {
			count: Int! = 0
			tag: String! = "t"
			new(n: Int!) {
				let i = 0
				loop {
					if (i >= n) { break }
					count += 1
					i += 1
				}
				self
			}
		}

		let a = Foo(0)
		let i = 0
		loop {
			if (i >= 1000) { break }
			a.x += 1
			a.inner.x += 1
			i += 1
		}
		assert { a.x == 1000 and a.inner.x == 1000 }

		let b = Foo(0).bump(1000)
		assert { b.x == 1000 and b.inner.x == 1000 }

		let c = b
		c.x += 1
		c.inner.x += 1
		assert { c.x == 1001 and b.x == 1000 and b.inner.x == 1000 }

		let d = Foo(0).bumpBare(1000)
		assert { d.x == 1000 and d.inner.x == 0 }

		let e = Built(1000)
		assert { e.count == 1000 and e.tag == "t" }
	`

	parsed, err := ParseWithRecovery("cow", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	file := parsed.(*FileBlock)
	typeScope, valueScope := BuildScopesFromImports("", nil)
	if _, err := InferFormsWithPhases(context.Background(), file.Forms, typeScope, hm.NewSimpleFresher()); err != nil {
		t.Fatalf("infer: %v", err)
	}
	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	ctx = ioctx.StderrToContext(ctx, &out)
	for _, node := range file.Forms {
		if _, err := EvalNode(ctx, valueScope, node); err != nil {
			t.Fatalf("eval: %v", err)
		}
	}

	// Instance root + a write generation, plus at most a method's receiver
	// snapshot for values a method returned.
	const maxDepth = 4
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		val, found, err := valueScope.Lookup(ctx, name)
		if err != nil || !found {
			t.Fatalf("lookup %s: found=%v err=%v", name, found, err)
		}
		obj := val.(*Object)
		if d := chainDepth(obj); d > maxDepth {
			t.Errorf("%s: parent chain depth %d after repeated writes, want <= %d", name, d, maxDepth)
		}
		inner, found, err := obj.Lookup(ctx, "inner")
		if err != nil {
			t.Fatalf("lookup %s.inner: %v", name, err)
		}
		if !found {
			continue
		}
		if d := chainDepth(inner.(*Object)); d > maxDepth {
			t.Errorf("%s.inner: parent chain depth %d after repeated writes, want <= %d", name, d, maxDepth)
		}
	}
}
