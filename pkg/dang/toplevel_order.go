package dang

import (
	"context"
	"fmt"
	"sort"

	"github.com/vito/dang/v2/pkg/hm"
)

// Top-level initialization order.
//
// A module's top-level declarations (`let x = ...`, `pub x = ...`) are lazy
// fields: EvaluateFormsWithPhases installs them all, then forces them in
// source order, and only then runs the top-level statements. A declaration
// read before its turn is forced on that read, wherever it happens, including
// inside a `{{ }}` field, whose fields run concurrently.
//
// Two checks follow, both over what code reads:
//
//   - A declaration whose initializer writes top-level state (directly,
//     through a block it runs, or by forcing another such declaration) must
//     not be forced from a `{{ }}` field: two fields could run initializers
//     writing the same binding at once. Declarations earlier in source order
//     have already run when a later one's initializer runs, so only forward
//     references are a problem there; a function body may run while
//     initialization is still under way, so any reference from one counts.
//   - A top-level statement must not write a binding that a declaration
//     after it reads: the declaration has already run, and saw the value from
//     before the write.
//
// Reads count wherever they are made on a declaration's behalf: in its
// initializer, or in the body of any function, method or constructor it
// references, transitively. Named functions can't write top-level bindings
// (purity.go), but they do read them. Function bodies are inferred after
// declarations, so everything is recorded during inference and checked by the
// effect solver at the end, over the fixed point of the reference graph.
//
// A function value's type says what it reads (its effect), but a binding or
// member whose type came from another function (a local or declaration
// typed by its initializer, a field default, a `let` reassigned) says what
// that function reads, and a method or named function a value is stored in
// says what its own body reads. So every store of a function value (an
// assignment, a `let`, a field default, an implicit constructor argument)
// records it in the target's slot (slotOf): the function's own effect for a
// method or named function, else an effect standing for the binding or
// member (memberEffect), which reads of it call. Calling a slot counts as
// calling everything stored in it (followSlots), so a value is followed
// wherever it goes: returned, held by locals, passed as parameters. This
// conflates slots by type and member, not by value: every Box.get call may
// call any function stored in any Box's get, which messages say. Calling an
// interface member calls that member of every implementing type
// (linkInterfaceMembers).
//
// The state lives on the effect solver, so it spans every file of a
// directory module, whose phases run in lockstep across files in file order.

// topPos orders top-level forms: by file (in the order the variables phase
// visits files), then position.
type topPos struct {
	file, line, col int
}

func (p topPos) before(q topPos) bool {
	if p.file != q.file {
		return p.file < q.file
	}
	if p.line != q.line {
		return p.line < q.line
	}
	return p.col < q.col
}

// topDecl is a top-level declaration.
type topDecl struct {
	name string
	pos  topPos
	// writes names a top-level binding its initializer writes directly or
	// through a closure it runs.
	writes string
	// reads holds the top-level bindings its initializer reads, and calls
	// the functions it references.
	reads map[effTarget]bool
	calls map[*Effect]bool
	// refs holds the functions it stores `&ref`s to: whoever reads the
	// declaration may call them.
	refs map[*Effect]bool
}

// topForm is the top-level form being inferred: a declaration's
// initializer, or a statement (decl == nil).
type topForm struct {
	decl *topDecl
	pos  topPos
	// valueRefs holds the operands of the `&ref`s that are the
	// declaration's value (valueRefNodes).
	valueRefs map[Node]bool
}

// fieldRef is a reference, inside a `{{ }}` field, to a top-level
// declaration (target) or to a function (fn), made in the initializer of
// decl (nil from a statement or function) or in a function body (inFn).
type fieldRef struct {
	target effTarget
	fn     *Effect
	name   string
	decl   *topDecl
	inFn   bool
	node   Node
}

// stmtWrite is a top-level statement's write of target.
type stmtWrite struct {
	pos    topPos
	target effTarget
	node   Node
}

type topOrder struct {
	files map[hm.Env]int
	decls map[effTarget]*topDecl
	// fnReads and fnCalls hold what each function body (keyed by its
	// effect) reads and references.
	fnReads   map[*Effect]map[effTarget]bool
	fnCalls   map[*Effect]map[*Effect]bool
	fieldRefs []fieldRef
	writes    []stmtWrite
	// slots holds, for each slot's effect (see slotOf), the effects of the
	// function values stored into it: calling the slot may call them.
	slots map[*Effect]map[*Effect]bool
	// slotEdges holds the calls followSlots adds: a slot to what it may
	// hold.
	slotEdges map[*Effect]map[*Effect]bool
	// members holds an effect for each binding or member holding function
	// values whose effect isn't its own (a local or declaration typed by
	// its initializer, such a field, or an interface member), standing for
	// what calling it may call (memberEffect).
	members map[effTarget]*Effect
	// types holds the object types declared, which may implement an
	// interface whose members are called.
	types map[*Type]bool
	// memberHomes maps each member effect back to its binding or member.
	memberHomes map[*Effect]effTarget
}

// memberEffect returns the effect standing for calls of the binding or
// member home: what is stored in it, or what the implementations of an
// interface member call.
func (o *topOrder) memberEffect(home effTarget) *Effect {
	if e := o.members[home]; e != nil {
		return e
	}
	if o.members == nil {
		o.members = map[effTarget]*Effect{}
	}
	e := newClosureEffect(home.name)
	o.members[home] = e
	if o.memberHomes == nil {
		o.memberHomes = map[*Effect]effTarget{}
	}
	o.memberHomes[e] = home
	return e
}

// slotName names the slot with effect slot for messages: `Type.member` for
// a member of a type, the plain name otherwise. Types are all known by the
// time messages are written.
func (o *topOrder) slotName(slot *Effect) string {
	home, ok := o.memberHomes[slot]
	if !ok {
		return slot.name
	}
	if t, ok := home.frame.(*Type); ok && (t.Kind == InterfaceKind || o.types[t]) {
		return t.Named + "." + home.name
	}
	return home.name
}

// ownEffect reports whether eff is the effect of the function declared as
// home itself (a method or named function), which says what calling it
// does, rather than one its type took from another function.
func ownEffect(eff *Effect, home effTarget) bool {
	if eff == nil {
		return false
	}
	if t, ok := home.frame.(*Type); ok && t.Kind == InterfaceKind {
		return false
	}
	return eff.home == home
}

// noteTopMember records a call (at node) of the binding or member name of
// frame, of type t, when its effect isn't its own: it may hold any function
// stored in it, and an interface member is any of its implementations.
func noteTopMember(ctx context.Context, frame TypeScope, name string, t hm.Type, node Node) {
	o := topOrderFrom(ctx)
	if o == nil || frame == nil || fnTypeOf(t) == nil {
		return
	}
	home := effTarget{frame: frame, name: name}
	if ownEffect(effectOf(t), home) {
		return
	}
	noteTopCall(ctx, o.memberEffect(home), name, node)
}

// ctorArgSlot returns the slot an argument passed for key to a call of a
// function with effect callee is stored in, when the callee is an implicit
// constructor: the field key.
func ctorArgSlot(ctx context.Context, callee *Effect, key string) *Effect {
	o := topOrderFrom(ctx)
	if o == nil || callee == nil || callee.ctorOf == nil {
		return nil
	}
	return o.memberEffect(effTarget{frame: callee.ctorOf, name: key})
}

// bindingSlot returns the slot a `let` (or field default) named name, in
// env, is: what its value is stored in.
func bindingSlot(ctx context.Context, env hm.Env, name string) *Effect {
	o := topOrderFrom(ctx)
	frame := envFrame(env)
	if o == nil || frame == nil {
		return nil
	}
	return o.memberEffect(effTarget{frame: frame, name: name})
}

// noteTopType records a declared object type, for interface members to
// find their implementations (linkInterfaceMembers).
func noteTopType(ctx context.Context, t *Type) {
	if o := topOrderFrom(ctx); o != nil {
		if o.types == nil {
			o.types = map[*Type]bool{}
		}
		o.types[t] = true
	}
}

type topFormKey struct{}

type storedRefKey struct{}

type slotValueKey struct{}

// withStoredRef marks node, the operand of a `&ref`, as stored rather than
// called.
func withStoredRef(ctx context.Context, node Node) context.Context {
	return context.WithValue(ctx, storedRefKey{}, node)
}

// slotValue is a value stored into a slot (noteSlotStore): the function
// values its operands (valueOperands) refer to are stored, not called.
type slotValue struct {
	operands map[Node]bool
	slot     *Effect
}

// withSlotValue marks value as stored into slot.
func withSlotValue(ctx context.Context, value Node, slot *Effect) context.Context {
	operands := map[Node]bool{}
	eachValueLeaf(value, func(leaf Node) {
		if ref, ok := leaf.(*FunctionRef); ok {
			operands[ref.Expr] = true
		}
	})
	return context.WithValue(ctx, slotValueKey{}, slotValue{operands: operands, slot: slot})
}

// eachValueLeaf calls fn with each expression whose value may be value's:
// through parentheses, a block's last form and the branches of an `if`,
// `case` or `??`.
func eachValueLeaf(value Node, fn func(Node)) {
	switch n := value.(type) {
	case nil:
	case *Grouped:
		eachValueLeaf(n.Expr, fn)
	case *Block:
		if n != nil && len(n.Forms) > 0 {
			eachValueLeaf(n.Forms[len(n.Forms)-1], fn)
		}
	case *Conditional:
		eachValueLeaf(n.Then, fn)
		eachValueLeaf(n.Else, fn)
	case *Case:
		for _, c := range n.Clauses {
			eachValueLeaf(c.Expr, fn)
		}
	case *Default:
		eachValueLeaf(n.Left, fn)
		eachValueLeaf(n.Right, fn)
	default:
		fn(value)
	}
}

// slotOf returns the slot an assignment target of type targetType is: the
// function declared there itself (a method or named function, written as a
// field path or a bare name), whose effect is its own, or else the
// binding's or member's effect standing for what is stored in it
// (memberEffect): a local, a declaration or a field typed by its
// initializer has another function's effect. A function value's type says
// what it reads (its effect) until it is stored in such a slot; from there
// it can be returned, held by locals and passed as parameters without any
// type saying what it reads.
func slotOf(ctx context.Context, env hm.Env, target Node, targetType hm.Type) *Effect {
	o := topOrderFrom(ctx)
	eff := effectOf(targetType)
	if o == nil || eff == nil {
		return nil
	}
	var home effTarget
	switch t := target.(type) {
	case *Select:
		typed, ok := t.Receiver.(interface{ GetInferredType() hm.Type })
		if !ok {
			return nil
		}
		rt, _ := stripNonNull(typed.GetInferredType())
		ts, ok := rt.(*Type)
		if !ok {
			return nil
		}
		home = effTarget{frame: ts, name: t.Field.Name}
	case *Symbol:
		frame := resolveBinding(env, t.Name)
		if frame == nil {
			return nil
		}
		home = effTarget{frame: frame, name: t.Name}
	default:
		return nil
	}
	if ownEffect(eff, home) {
		return eff
	}
	return o.memberEffect(home)
}

// noteSlotStore records the assignment of value, of type valueType, to
// slot: calling the slot may call the value. Every expression the value
// may come from counts (eachValueLeaf): its type's effect, and for a
// function value read by name (`x.get = f`, f taking arguments) what its
// binding may hold too.
func noteSlotStore(ctx context.Context, env hm.Env, slot *Effect, value Node, valueType hm.Type) {
	noteSlotEffect(ctx, slot, effectOf(valueType))
	o := topOrderFrom(ctx)
	if o == nil {
		return
	}
	eachValueLeaf(value, func(leaf Node) {
		typed, ok := leaf.(interface{ GetInferredType() hm.Type })
		if !ok {
			return
		}
		lt := typed.GetInferredType()
		if fnTypeOf(lt) == nil {
			return
		}
		noteSlotEffect(ctx, slot, effectOf(lt))
		var home effTarget
		switch v := leaf.(type) {
		case *Symbol:
			frame := resolveBinding(env, v.Name)
			if frame == nil {
				return
			}
			home = effTarget{frame: frame, name: v.Name}
		case *Select:
			rtyped, ok := v.Receiver.(interface{ GetInferredType() hm.Type })
			if !ok {
				return
			}
			rt, _ := stripNonNull(rtyped.GetInferredType())
			ts, ok := rt.(*Type)
			if !ok {
				return
			}
			home = effTarget{frame: ts, name: v.Field.Name}
		default:
			return
		}
		if !ownEffect(effectOf(lt), home) {
			noteSlotEffect(ctx, slot, o.memberEffect(home))
		}
	})
}

// noteSlotEffect records that slot may hold a function with effect value.
// Stores in top-level statements are skipped: every declaration has run by
// then.
func noteSlotEffect(ctx context.Context, slot, value *Effect) {
	o := topOrderFrom(ctx)
	if o == nil || slot == nil || inTopStatement(ctx) {
		return
	}
	if value == nil || value == slot {
		return
	}
	addCall(&o.slots, slot, value)
}

// inTopStatement reports whether ctx is inferring a top-level statement,
// which runs after every top-level declaration.
func inTopStatement(ctx context.Context) bool {
	f := topFormFrom(ctx)
	return f != nil && f.decl == nil
}

func topOrderFrom(ctx context.Context) *topOrder {
	if s, ok := ctx.Value(effSolverKey{}).(*effectSolver); ok {
		return &s.top
	}
	return nil
}

func topFormFrom(ctx context.Context) *topForm {
	f, _ := ctx.Value(topFormKey{}).(*topForm)
	return f
}

// topLevelEnv reports whether env is the module's top-level scope, as opposed
// to a type or statics body inferred with the same phases.
func topLevelEnv(ctx context.Context, env hm.Env) bool {
	top := topLevelFrameFrom(ctx)
	return top != nil && envFrame(env) == top
}

// fileIndex numbers env's file in the order files are first seen.
func (o *topOrder) fileIndex(env hm.Env) int {
	if o.files == nil {
		o.files = map[hm.Env]int{}
	}
	i, ok := o.files[env]
	if !ok {
		i = len(o.files)
		o.files[env] = i
	}
	return i
}

func (o *topOrder) posOf(env hm.Env, node Node) topPos {
	pos := topPos{file: o.fileIndex(env)}
	if loc := node.GetSourceLocation(); loc != nil {
		pos.line, pos.col = loc.Line, loc.Column
	}
	return pos
}

// declareTopVariables registers the top-level declarations of one file (in
// env), before any of them is inferred.
func declareTopVariables(ctx context.Context, env hm.Env, variables []Node) {
	o := topOrderFrom(ctx)
	if o == nil || !topLevelEnv(ctx, env) {
		return
	}
	frame := envFrame(env)
	o.fileIndex(env)
	for _, form := range variables {
		field, ok := form.(*FieldDecl)
		if !ok {
			continue
		}
		if o.decls == nil {
			o.decls = map[effTarget]*topDecl{}
		}
		o.decls[effTarget{frame: frame, name: field.Name.Name}] = &topDecl{
			name: field.Name.Name,
			pos:  o.posOf(env, field),
		}
	}
}

// withTopDecl marks ctx as inferring the initializer of top-level
// declaration form (in env).
func withTopDecl(ctx context.Context, env hm.Env, form Node) context.Context {
	o := topOrderFrom(ctx)
	field, ok := form.(*FieldDecl)
	if o == nil || !ok || !topLevelEnv(ctx, env) {
		return ctx
	}
	d := o.decls[effTarget{frame: envFrame(env), name: field.Name.Name}]
	if d == nil {
		return ctx
	}
	f := &topForm{decl: d, pos: d.pos, valueRefs: map[Node]bool{}}
	valueRefNodes(field.Value, f.valueRefs)
	return context.WithValue(ctx, topFormKey{}, f)
}

// valueRefNodes collects into refs the operands of the `&ref`s that are
// (part of) the value of node: the node itself, a block's last form, the
// branches of an `if`, `case` or `??`, and list and record elements.
func valueRefNodes(node Node, refs map[Node]bool) {
	switch n := node.(type) {
	case *FunctionRef:
		refs[n.Expr] = true
	case *Grouped:
		valueRefNodes(n.Expr, refs)
	case *Block:
		if n != nil && len(n.Forms) > 0 {
			valueRefNodes(n.Forms[len(n.Forms)-1], refs)
		}
	case *Conditional:
		valueRefNodes(n.Then, refs)
		valueRefNodes(n.Else, refs)
	case *Case:
		for _, c := range n.Clauses {
			valueRefNodes(c.Expr, refs)
		}
	case *Default:
		valueRefNodes(n.Left, refs)
		valueRefNodes(n.Right, refs)
	case *List:
		for _, e := range n.Elements {
			valueRefNodes(e, refs)
		}
	case *ObjectLiteral:
		for _, f := range n.Fields {
			valueRefNodes(f.Value, refs)
		}
	}
}

// withTopStatement marks ctx as inferring top-level statement form (in env).
func withTopStatement(ctx context.Context, env hm.Env, form Node) context.Context {
	o := topOrderFrom(ctx)
	if o == nil || !topLevelEnv(ctx, env) {
		return ctx
	}
	return context.WithValue(ctx, topFormKey{}, &topForm{pos: o.posOf(env, form)})
}

// noteTopWrite records that the top-level form being inferred may write
// target (at node), outside any function body.
func noteTopWrite(ctx context.Context, target effTarget, node Node) {
	f := topFormFrom(ctx)
	if f == nil || !isTopLevelFrame(target.frame, topLevelFrameFrom(ctx)) {
		return
	}
	if f.decl != nil {
		if f.decl.writes == "" {
			f.decl.writes = target.name
		}
		return
	}
	o := topOrderFrom(ctx)
	o.writes = append(o.writes, stmtWrite{pos: f.pos, target: target, node: node})
}

// noteTopRead records a read (at node) of name, resolved in env, whose type
// is t: by the enclosing function bodies and declaration initializer, and,
// inside a `{{ }}` field, as a reference to check once everything is known.
func noteTopRead(ctx context.Context, env hm.Env, name string, node Node, t hm.Type) {
	o := topOrderFrom(ctx)
	if o == nil {
		return
	}
	frame := resolveBinding(env, name)
	if frame == nil {
		return
	}
	if eff := effectOf(t); eff != nil && eff.kind == effClosure {
		noteTopCall(ctx, eff, name, node)
	}
	// A binding holding function values whose type isn't its own may
	// hold whatever is stored in it.
	noteTopMember(ctx, frame, name, t, node)
	if !isTopLevelFrame(frame, topLevelFrameFrom(ctx)) {
		return
	}
	target := effTarget{frame: frame, name: name}
	f := topFormFrom(ctx)
	if f != nil && f.decl != nil {
		if f.decl.reads == nil {
			f.decl.reads = map[effTarget]bool{}
		}
		f.decl.reads[target] = true
	}
	eachBody(ctx, func(eff *Effect) {
		addTarget(&o.fnReads, eff, target)
	})
	if inField(ctx) {
		// Checked at the end: type bodies are inferred before declarations
		// are registered.
		o.noteFieldRef(ctx, fieldRef{target: target, name: name, node: node})
	}
}

// noteTopCall records a reference (at node) to the function with effect
// callee, named name: what it reads, its referrers read. A `&ref` that is
// stored rather than passed doesn't call it by itself:
//
//   - one that is (part of) the value stored into a binding, field or
//     constructor argument is stored into that slot (slotOf,
//     noteSlotEffect), and a top-level declaration's initializer storing
//     one holds it, passing its reads on to whoever reads the declaration;
//   - anywhere else (an argument to a function, a list element), it counts
//     as called by the code storing it, which can't be followed further: a
//     function it is passed to may call it.
func noteTopCall(ctx context.Context, callee *Effect, name string, node Node) {
	o := topOrderFrom(ctx)
	if o == nil || callee == nil || callee.kind != effClosure {
		return
	}
	f := topFormFrom(ctx)
	if target, _ := ctx.Value(assignTargetKey{}).(Node); target == node {
		// The slot being assigned (`b.get = ...`) isn't called.
		return
	}
	if sv, ok := ctx.Value(slotValueKey{}).(slotValue); ok && sv.operands[node] {
		// Stored into a slot, not called (noteSlotEffect); an
		// initializer storing one also holds it.
		if f != nil && f.decl != nil {
			if f.decl.refs == nil {
				f.decl.refs = map[*Effect]bool{}
			}
			f.decl.refs[callee] = true
		}
		noteSlotEffect(ctx, sv.slot, callee)
		return
	}
	if stored, _ := ctx.Value(storedRefKey{}).(Node); stored == node {
		if f != nil && f.decl != nil {
			if f.decl.refs == nil {
				f.decl.refs = map[*Effect]bool{}
			}
			f.decl.refs[callee] = true
			if f.valueRefs[node] {
				return
			}
		}
	}
	if f != nil && f.decl != nil {
		if f.decl.calls == nil {
			f.decl.calls = map[*Effect]bool{}
		}
		f.decl.calls[callee] = true
	}
	eachBody(ctx, func(eff *Effect) {
		if eff != callee {
			addCall(&o.fnCalls, eff, callee)
		}
	})
	if inField(ctx) {
		o.noteFieldRef(ctx, fieldRef{fn: callee, name: name, node: node})
	}
}

// noteTypeBody records that a type's constructor runs its body (field
// defaults), whose code is attributed to body.
func noteTypeBody(ctx context.Context, ctor, body *Effect) {
	o := topOrderFrom(ctx)
	if o == nil || ctor == nil || body == nil {
		return
	}
	addCall(&o.fnCalls, ctor, body)
}

func (o *topOrder) noteFieldRef(ctx context.Context, ref fieldRef) {
	ref.inFn = inFunctionBody(ctx)
	if f := topFormFrom(ctx); f != nil {
		ref.decl = f.decl
	}
	if ref.decl == nil && !ref.inFn {
		// A statement: every declaration has run.
		return
	}
	o.fieldRefs = append(o.fieldRefs, ref)
}

// eachBody calls fn with the effect of each function body enclosing ctx, up
// to the innermost type code (a method, constructor or type body): an
// enclosing body doesn't run that code just by declaring it.
func eachBody(ctx context.Context, fn func(*Effect)) {
	for fr := effFrameFrom(ctx); fr != nil; fr = fr.parent {
		if !fr.field && fr.eff != nil {
			fn(fr.eff)
		}
		if fr.sealed != nil {
			return
		}
	}
}

func addTarget(m *map[*Effect]map[effTarget]bool, eff *Effect, t effTarget) {
	if *m == nil {
		*m = map[*Effect]map[effTarget]bool{}
	}
	if (*m)[eff] == nil {
		(*m)[eff] = map[effTarget]bool{}
	}
	(*m)[eff][t] = true
}

func addCall(m *map[*Effect]map[*Effect]bool, from, to *Effect) {
	if *m == nil {
		*m = map[*Effect]map[*Effect]bool{}
	}
	if (*m)[from] == nil {
		(*m)[from] = map[*Effect]bool{}
	}
	(*m)[from][to] = true
}

// via is a read of a binding, through the function named fn ("" when read
// directly), which may have been stored in the slot named slot (for
// messages: slots are told apart by type, not value, so the function may be
// another value's).
type via struct {
	fn   string
	slot string
}

// throughClause describes v for messages, empty for a direct read, or a
// read through self (the function referenced at the site).
func (v via) throughClause(self string) string {
	if v.fn == "" || v.fn == self && v.slot == "" {
		return ""
	}
	if v.slot != "" && v.slot != v.fn {
		return fmt.Sprintf(" (through `%s`, which may be stored in `%s`)", v.fn, v.slot)
	}
	return fmt.Sprintf(" (through `%s`)", v.fn)
}

// better reports whether v is a better citation than cur: a read made
// without a slot in between, then a direct one, then by name. The order is
// total, so which via a read is cited with doesn't depend on map order.
func (v via) better(cur via) bool {
	if (v.slot == "") != (cur.slot == "") {
		return v.slot == ""
	}
	if (v.fn == "") != (cur.fn == "") {
		return v.fn == ""
	}
	if v.fn != cur.fn {
		return v.fn < cur.fn
	}
	return v.slot < cur.slot
}

// offer records a read of t through v in reads, unless a better one is
// there, reporting whether reads changed.
func offer(reads map[effTarget]via, t effTarget, v via) bool {
	if cur, ok := reads[t]; ok && !v.better(cur) {
		return false
	}
	reads[t] = v
	return true
}

// topAnalysis is the fixed point of what functions and declarations read.
type topAnalysis struct {
	o *topOrder
	// fnReads: every top-level binding a function may read, through the
	// functions it references, with the first function reading it.
	fnReads map[*Effect]map[effTarget]via
	// declReads: the same for declarations' initializers.
	declReads map[*topDecl]map[effTarget]via
	// writes: a top-level binding a declaration's initializer may write,
	// including by forcing other declarations.
	writes map[*topDecl]string
}

func (o *topOrder) analyze() *topAnalysis {
	a := &topAnalysis{
		o:         o,
		fnReads:   map[*Effect]map[effTarget]via{},
		declReads: map[*topDecl]map[effTarget]via{},
		writes:    map[*topDecl]string{},
	}
	fns := map[*Effect]bool{}
	for f := range o.fnReads {
		fns[f] = true
	}
	for f, callees := range o.fnCalls {
		fns[f] = true
		for g := range callees {
			fns[g] = true
		}
	}
	for f := range fns {
		reads := map[effTarget]via{}
		// A block has no name of its own to cite: its reads are cited
		// through the first named function they reach.
		name := f.name
		if f.block {
			name = ""
		}
		for t := range o.fnReads[f] {
			reads[t] = via{fn: name}
		}
		a.fnReads[f] = reads
	}
	for changed := true; changed; {
		changed = false
		for f, callees := range o.fnCalls {
			for g := range callees {
				for t, v := range a.fnReads[g] {
					if v.fn == "" && !f.block {
						v.fn = o.slotName(f)
					}
					if o.slotEdges[f][g] {
						// Cite the slot nearest the reader: the one it
						// calls.
						v.slot = o.slotName(f)
					}
					if offer(a.fnReads[f], t, v) {
						changed = true
					}
				}
			}
		}
	}
	for _, d := range o.decls {
		reads := map[effTarget]via{}
		for t := range d.reads {
			reads[t] = via{}
		}
		for g := range d.calls {
			for t, v := range a.fnReads[g] {
				offer(reads, t, v)
			}
		}
		a.declReads[d] = reads
		if d.writes != "" {
			a.writes[d] = d.writes
		}
	}
	// Reading a declaration that holds a `&ref` may call it.
	for changed := true; changed; {
		changed = false
		for _, d := range o.decls {
			reads := a.declReads[d]
			for t := range reads {
				x := o.decls[t]
				if x == nil || x == d {
					continue
				}
				for g := range x.refs {
					for rt, v := range a.fnReads[g] {
						if offer(reads, rt, v) {
							changed = true
						}
					}
				}
			}
		}
	}
	// Forcing a declaration whose initializer writes may write too.
	for changed := true; changed; {
		changed = false
		for _, d := range o.decls {
			if a.writes[d] != "" {
				continue
			}
			for t := range a.declReads[d] {
				if x := o.decls[t]; x != nil && x != d && a.writes[x] != "" {
					a.writes[d] = a.writes[x]
					changed = true
					break
				}
			}
		}
	}
	return a
}

// readsOf returns the declarations a field reference may force, earliest
// first, with the function it reads each through.
func (a *topAnalysis) readsOf(ref fieldRef) ([]*topDecl, []via) {
	if ref.fn == nil {
		if d := a.o.decls[ref.target]; d != nil {
			return []*topDecl{d}, []via{{}}
		}
		return nil, nil
	}
	var ds []*topDecl
	vias := map[*topDecl]via{}
	for t, v := range a.fnReads[ref.fn] {
		if d := a.o.decls[t]; d != nil {
			ds = append(ds, d)
			vias[d] = v
		}
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i].pos.before(ds[j].pos) })
	out := make([]via, len(ds))
	for i, d := range ds {
		out[i] = vias[d]
	}
	return ds, out
}

// check reports the violations of both rules. s is the effect solver, whose
// bindings say which function values parameters stored in slots stand for.
func (o *topOrder) check(s *effectSolver, errs *effErrors) {
	if len(o.fieldRefs) == 0 && len(o.writes) == 0 {
		return
	}
	o.followSlots(s)
	a := o.analyze()
	// A read can be recorded more than once (a binding's type's effect,
	// and what may be stored in the binding): report each node once, for
	// the first reference recorded there.
	reported := map[Node]bool{}
	for _, ref := range o.fieldRefs {
		if reported[ref.node] {
			continue
		}
		ds, vias := a.readsOf(ref)
		for i, d := range ds {
			w := a.writes[d]
			if w == "" || d == ref.decl {
				continue
			}
			if !ref.inFn && !ref.decl.pos.before(d.pos) {
				continue
			}
			errs.add(ref.node, fieldRefMessage(ref, d, vias[i], w))
			reported[ref.node] = true
			break
		}
	}
	for _, sw := range o.writes {
		var reader *topDecl
		var through via
		for _, d := range o.decls {
			v, ok := a.declReads[d][sw.target]
			if ok && sw.pos.before(d.pos) && (reader == nil || d.pos.before(reader.pos)) {
				reader, through = d, v
			}
		}
		if reader == nil {
			continue
		}
		reads := fmt.Sprintf("reads `%s`%s", sw.target.name, through.throughClause(""))
		errs.add(sw.node, fmt.Sprintf(
			"top-level declarations run before top-level statements, so `%s` (line %d) %s before this statement writes it; write `%s` in a declaration instead (e.g. compute it in `%s`'s initializer), or read it in a statement",
			reader.name, reader.pos.line, reads, sw.target.name, reader.name))
	}
}

// followSlots makes calling each slot call the function values stored in
// it. A function-typed parameter stored in one (`self.saved = block`)
// stands for every argument bound to it, and a block calling a parameter of
// the function it's in (`{ x => f(x) }`) for that parameter's arguments too.
func (o *topOrder) followSlots(s *effectSolver) {
	o.linkInterfaceMembers()
	args := map[*Effect][]*Effect{}
	for _, b := range s.bindings {
		// A call in a top-level statement runs after every declaration.
		if !b.inStatement {
			args[b.param] = append(args[b.param], b.arg)
		}
	}
	for slot, values := range o.slots {
		seen := map[*Effect]bool{}
		var follow func(e *Effect)
		follow = func(e *Effect) {
			if e == nil || seen[e] {
				return
			}
			seen[e] = true
			switch e.kind {
			case effClosure:
				if e != slot {
					addCall(&o.fnCalls, slot, e)
					addCall(&o.slotEdges, slot, e)
				}
				for p := range e.params {
					follow(p)
				}
			case effParam:
				for _, arg := range args[e] {
					follow(arg)
				}
			}
		}
		for v := range values {
			follow(v)
		}
	}
}

// linkInterfaceMembers makes calling an interface member call that member
// of every object type declared that implements the interface.
func (o *topOrder) linkInterfaceMembers() {
	types := map[*Type]bool{}
	for t := range o.types {
		types[t] = true
	}
	collect := func(e *Effect) {
		if t, ok := e.home.frame.(*Type); ok && t.Kind == ObjectKind {
			types[t] = true
		}
	}
	for f, callees := range o.fnCalls {
		collect(f)
		for g := range callees {
			collect(g)
		}
	}
	for f := range o.fnReads {
		collect(f)
	}
	for slot := range o.slots {
		collect(slot)
	}
	var ifaces []effTarget
	for home := range o.members {
		if t, ok := home.frame.(*Type); ok {
			if t.Kind == InterfaceKind {
				ifaces = append(ifaces, home)
			} else if t.Kind == ObjectKind {
				types[t] = true
			}
		}
	}
	for _, home := range ifaces {
		member := o.members[home]
		for t := range types {
			if !implementsIface(t, home.frame) {
				continue
			}
			if scheme, ok := t.LocalSchemeOf(home.name); ok {
				mt, _ := scheme.Type()
				if eff := effectOf(mt); eff != nil && eff.kind == effClosure {
					addCall(&o.fnCalls, member, eff)
				}
			}
			if impl := o.members[effTarget{frame: t, name: home.name}]; impl != nil {
				addCall(&o.fnCalls, member, impl)
			}
		}
	}
}

func implementsIface(t *Type, iface TypeScope) bool {
	seen := map[TypeScope]bool{}
	var walk func(ifaces []TypeScope) bool
	walk = func(ifaces []TypeScope) bool {
		for _, i := range ifaces {
			if i == iface {
				return true
			}
			if seen[i] {
				continue
			}
			seen[i] = true
			// An interface extending another implements it too.
			if it, ok := i.(*Type); ok && walk(it.interfaces) {
				return true
			}
		}
		return false
	}
	return walk(t.interfaces)
}

func fieldRefMessage(ref fieldRef, d *topDecl, through via, writes string) string {
	if ref.fn == nil {
		if ref.inFn {
			return fmt.Sprintf(
				"%s; reading `%s` here may run its initializer, which may write `%s`, while other fields run: this code may run while top-level declarations are still being initialized; read `%s` outside the {{ }}, in a top-level statement, or pass it in",
				concurrentFields, ref.name, writes, ref.name)
		}
		return fmt.Sprintf(
			"%s; `%s` is declared after `%s`, so reading it here runs its initializer, which may write `%s`, while other fields run; declare `%s` before `%s`, or read it before the {{ }} (`let %s1 = %s`)",
			concurrentFields, ref.name, ref.decl.name, writes, ref.name, ref.decl.name, ref.name, ref.name)
	}
	reads := fmt.Sprintf("`%s` reads `%s`%s", ref.name, d.name, through.throughClause(ref.name))
	if ref.inFn {
		return fmt.Sprintf(
			"%s; %s, whose initializer may write `%s`, so calling it here may run that initializer while other fields run: this code may run while top-level declarations are still being initialized; call `%s` outside the {{ }}",
			concurrentFields, reads, writes, ref.name)
	}
	return fmt.Sprintf(
		"%s; %s, which is declared after `%s`, so calling it here runs `%s`'s initializer, which may write `%s`, while other fields run; declare `%s` before `%s`, or call `%s` before the {{ }}",
		concurrentFields, reads, ref.decl.name, d.name, writes, d.name, ref.decl.name, ref.name)
}

// inField reports whether ctx is inside a `{{ }}` field.
func inField(ctx context.Context) bool {
	for fr := effFrameFrom(ctx); fr != nil; fr = fr.parent {
		if fr.field {
			return true
		}
	}
	return false
}
