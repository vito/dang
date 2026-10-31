package dang

import (
	"context"
)

// Invalidating narrowings.
//
// A refinement (narrowing.go) holds from its null check until its binding may
// have been assigned null. What may do so is logged in inference order, which
// within a function body is source order:
//
//   - an assignment of a possibly-null value (`x = null`, also `self.x = null`
//     for the bare field `x`), made where the read can see it: in the reading
//     body or around it, or in a block (which runs where it is passed), but
//     not in a nested function, which only takes effect when called;
//   - a call of a nested function that assigns the binding null (its effect's
//     nullTargets);
//   - a call of a function parameter, which runs its caller's code, so it may
//     write any binding declared outside the function owning the parameter.
//
// A read of a narrowed name after such a write sees the binding's unrefined
// type (readNarrowed). Writes in a branch the read can't follow from are
// skipped: the other branch of the same `if`, or a branch that returns.
//
// Source order is not run order for a closure (a block argument or nested
// function), which may run again, or later. So a closure's reads of a
// refinement made outside it are checked once it is inferred against the
// writes in its own body, which a later run would see (exitNarrowBody), and
// against the writes made outside it after which it may run: later in its
// scope for a nested function, anywhere for an escaping closure
// (checkCaptures).

// narrowWrite is a logged write that may set a binding to null.
type narrowWrite struct {
	at     int
	target effTarget
	// wild: a call of a function parameter, which may write any binding
	// declared outside owner, the parameter's function (any top-level
	// binding when owner is unknown).
	wild  bool
	owner *effFrame
	// call: a call of a nested function assigning target.
	call bool
	// frame is the body the write or call is in.
	frame *effFrame
	node  Node
}

// narrowRead is a read of a refinement made outside the closure reading it.
type narrowRead struct {
	n    *Narrowing
	node Node
}

type narrowCapture struct {
	eff        *Effect
	reads      []narrowRead
	start, end int
	// block: a block argument, which runs during the call it is passed to
	// unless it escapes. A nested function can be called any time later in
	// scope.
	block bool
	// parent is the body the closure is created in, and scope the block
	// scope it is declared in.
	parent *effFrame
	scope  *narrowScope
	// rerun is the tick the outermost body around the closure that may run
	// again began at (0 if none).
	rerun int
}

type narrowLog struct {
	clock    int
	writes   []narrowWrite
	captures []narrowCapture
	errs     []narrowError
	// checks holds the tick each null comparison was inferred at.
	checks map[Node]int
	// skips are tick ranges of writes a read can't follow from.
	skips []*tickRange
	// noRerun are tick ranges of branches after which closure bodies frames
	// don't run again.
	noRerun []rerunRange
}

type rerunRange struct {
	from, to int
	frames   []*effFrame
}

// rerunSkips reports whether a write at tick at is in a branch after which
// body fr doesn't run again.
func (l *narrowLog) rerunSkips(at int, fr *effFrame) bool {
	for _, r := range l.noRerun {
		if at > r.from && at < r.to {
			for _, f := range r.frames {
				if f == fr {
					return true
				}
			}
		}
	}
	return false
}

type tickRange struct{ from, to int }

type narrowError struct {
	node Node
	msg  string
}

// narrowScope is a block scope, which ends at tick end (0 until then).
type narrowScope struct{ end int }

type narrowScopeKey struct{}

func narrowLogFrom(ctx context.Context) *narrowLog {
	if s, ok := ctx.Value(effSolverKey{}).(*effectSolver); ok {
		return &s.narrow
	}
	return nil
}

func (l *narrowLog) tick() int {
	l.clock++
	return l.clock
}

// noteCheck records that a null comparison was inferred now.
func noteCheck(ctx context.Context, check Node) {
	if l := narrowLogFrom(ctx); l != nil {
		if l.checks == nil {
			l.checks = map[Node]int{}
		}
		l.checks[check] = l.tick()
	}
}

// enterBranch starts a branch, returning the tick it starts at.
func enterBranch(ctx context.Context) int {
	if l := narrowLogFrom(ctx); l != nil {
		return l.tick()
	}
	return 0
}

// skipWrites makes reads skip the writes in ticks (from, now), returning a
// function undoing it, for the branch of an `if` the other can't see.
func skipWrites(ctx context.Context, from int) func() {
	l := narrowLogFrom(ctx)
	if l == nil {
		return func() {}
	}
	l.skips = append(l.skips, &tickRange{from: from, to: l.tick()})
	skip := l.skips[len(l.skips)-1]
	return func() {
		for i, r := range l.skips {
			if r == skip {
				l.skips = append(l.skips[:i:i], l.skips[i+1:]...)
				return
			}
		}
	}
}

// skipBranch makes reads after a branch (started at tick start) skip its
// writes when it never falls through: for good when it returns, and until
// the end of the block it breaks or continues out of. Inside a `rescue`
// operand nothing is skipped: a raise before the exit may be rescued, and
// any call may raise.
func skipBranch(ctx context.Context, branch Node, start int) {
	l := narrowLogFrom(ctx)
	if l == nil || inRescue(ctx) {
		return
	}
	loopExits, continues := loopExitsIn(branch)
	switch {
	case returnsAlways(branch) && !loopExits:
		skipWrites(ctx, start)
		// The function is left: the blocks within it don't run again.
		var frames []*effFrame
		for fr := bodyFrame(ctx); fr != nil && (fr.block || fr.field); fr = fr.parent {
			frames = append(frames, fr)
		}
		l.noRerun = append(l.noRerun, rerunRange{from: start, to: l.tick(), frames: frames})
	case leavesIteration(branch):
		fr := bodyFrame(ctx)
		if fr == nil || !fr.block {
			return
		}
		fr.unskips = append(fr.unskips, skipWrites(ctx, start))
		if !continues {
			// Breaking (or returning) leaves the block for good.
			l.noRerun = append(l.noRerun, rerunRange{from: start, to: l.tick(), frames: []*effFrame{fr}})
		}
	}
}

// loopExitsIn reports whether node breaks or continues out of the block it
// is in (not counting blocks and functions within it), and whether it
// continues.
func loopExitsIn(node Node) (exits, continues bool) {
	node.Walk(func(n Node) bool {
		switch n.(type) {
		case *Break:
			exits = true
		case *Continue:
			exits, continues = true, true
		case *BlockArg, *FunDecl:
			return n == node
		}
		return true
	})
	return
}

type rescueKey struct{}

// withinRescue marks ctx as inferring a `rescue` operand, up to the
// function boundary (outsideRescue).
func withinRescue(ctx context.Context) context.Context {
	return context.WithValue(ctx, rescueKey{}, true)
}

func outsideRescue(ctx context.Context) context.Context {
	if inRescue(ctx) {
		return context.WithValue(ctx, rescueKey{}, false)
	}
	return ctx
}

func inRescue(ctx context.Context) bool {
	in, _ := ctx.Value(rescueKey{}).(bool)
	return in
}

// endBlockSkips undoes the skips a block body made for its breaks and
// continues, whose writes reach past the block.
func endBlockSkips(fr *effFrame) {
	if fr == nil {
		return
	}
	for _, undo := range fr.unskips {
		undo()
	}
	fr.unskips = nil
}

func (l *narrowLog) excluded(at int) bool {
	for _, r := range l.skips {
		if at > r.from && at < r.to {
			return true
		}
	}
	return false
}

// withNarrowScope enters a block scope; call the returned function when it
// ends.
func withNarrowScope(ctx context.Context) (context.Context, func()) {
	l := narrowLogFrom(ctx)
	if l == nil {
		return ctx, func() {}
	}
	scope := &narrowScope{}
	return context.WithValue(ctx, narrowScopeKey{}, scope), func() { scope.end = l.tick() }
}

// noteCall logs the writes made by calling (or passing) a function with
// effect callee. A block's own writes are logged where it is written, which
// is where it runs.
func (l *narrowLog) noteCall(ctx context.Context, callee *Effect, node Node) {
	frame := effFrameFrom(ctx)
	switch callee.kind {
	case effParam:
		l.writes = append(l.writes, narrowWrite{at: l.tick(), wild: true, owner: ownerFrame(frame, callee.owner), frame: frame, node: node})
	case effClosure:
		if !callee.block {
			for t := range callee.nullTargets {
				l.writes = append(l.writes, narrowWrite{at: l.tick(), target: t, call: true, frame: frame, node: node})
			}
			// Its reads of outer refinements happen here too.
			for _, r := range callee.narrowReads {
				for fr := frame; fr != nil; fr = fr.parent {
					if fr.field {
						continue
					}
					if fr.logStart < r.n.pos {
						break
					}
					fr.reads = append(fr.reads, r)
				}
			}
		}
		for p := range callee.params {
			l.writes = append(l.writes, narrowWrite{at: l.tick(), wild: true, owner: ownerFrame(frame, p.owner), frame: frame, node: node})
		}
	}
}

// ownerFrame finds the body of the function with effect owner on the stack.
func ownerFrame(fr *effFrame, owner *Effect) *effFrame {
	for ; fr != nil; fr = fr.parent {
		if fr.eff == owner && owner != nil {
			return fr
		}
	}
	return nil
}

// hits reports whether w may null n's binding, as seen from a read in body
// reader.
func (w narrowWrite) hits(n *Narrowing, reader *effFrame) bool {
	switch {
	case w.wild && w.owner != nil:
		if n.target.frame == nil || w.owner.inside(n.target.frame) {
			return false
		}
	case w.wild:
		if !n.topLevel {
			return false
		}
	default:
		if n.target.frame == nil || w.target != n.target {
			return false
		}
	}
	return inline(w.frame, reader)
}

// inline reports whether a write in body w runs where a read in body r can
// see it: in r or a body around it, or in blocks within those, which run
// where they are passed. A write in a nested function only runs when it is
// called, which is logged as a call.
func inline(w, r *effFrame) bool {
	for f := w; f != nil; f = f.parent {
		for a := r; a != nil; a = a.parent {
			if a == f {
				return true
			}
		}
		if !f.block && !f.field {
			return false
		}
	}
	return true
}
