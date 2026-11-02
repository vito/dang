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
}

type topFormKey struct{}

type storedRefKey struct{}

// withStoredRef marks node, the operand of a `&ref`, as stored rather than
// called.
func withStoredRef(ctx context.Context, node Node) context.Context {
	return context.WithValue(ctx, storedRefKey{}, node)
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
	return context.WithValue(ctx, topFormKey{}, &topForm{decl: d, pos: d.pos})
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

// checkInitializerWrite reports a write (at node) of top-level binding target
// from a top-level declaration's initializer, or a block or `{{ }}` field
// written in it. Initializers write only their own locals, so forcing them
// concurrently can't race. The check is syntactic: an impure closure can't be
// stored (effects.go), so any closure an initializer runs that writes outer
// state is written inside it. Function bodies are left to
// checkTopLevelWrite, which rejects their top-level writes everywhere.
func checkInitializerWrite(ctx context.Context, target effTarget, node Node) {
	f := topFormFrom(ctx)
	if f == nil || f.decl == nil || inFunctionBody(ctx) ||
		!isTopLevelFrame(target.frame, topLevelFrameFrom(ctx)) {
		return
	}
	_ = reportEffectError(ctx, NewInferError(fmt.Errorf(
		"cannot assign to `%s` in `%s`'s initializer: top-level declarations run lazily on first read, maybe concurrently from {{ }} fields, so an initializer may only write its own locals; move the write into a top-level statement, or build the value in a local and return it",
		target.name, f.decl.name), node))
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
// stored rather than passed doesn't call it: a declaration holding one only
// passes its reads on to whoever reads the declaration.
func noteTopCall(ctx context.Context, callee *Effect, name string, node Node) {
	o := topOrderFrom(ctx)
	if o == nil || callee == nil || callee.kind != effClosure {
		return
	}
	if stored, _ := ctx.Value(storedRefKey{}).(Node); stored == node {
		if f := topFormFrom(ctx); f != nil && f.decl != nil {
			if f.decl.refs == nil {
				f.decl.refs = map[*Effect]bool{}
			}
			f.decl.refs[callee] = true
		}
		return
	}
	if f := topFormFrom(ctx); f != nil && f.decl != nil {
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
// directly).
type via struct {
	fn string
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
		for t := range o.fnReads[f] {
			reads[t] = via{fn: f.name}
		}
		a.fnReads[f] = reads
	}
	for changed := true; changed; {
		changed = false
		for f, callees := range o.fnCalls {
			for g := range callees {
				for t, v := range a.fnReads[g] {
					if _, ok := a.fnReads[f][t]; !ok {
						a.fnReads[f][t] = v
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
				if _, ok := reads[t]; !ok {
					reads[t] = v
				}
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
						if _, ok := reads[rt]; !ok {
							reads[rt] = v
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
// first, with the function it reads each through ("" directly).
func (a *topAnalysis) readsOf(ref fieldRef) ([]*topDecl, []string) {
	if ref.fn == nil {
		if d := a.o.decls[ref.target]; d != nil {
			return []*topDecl{d}, []string{""}
		}
		return nil, nil
	}
	var ds []*topDecl
	vias := map[*topDecl]string{}
	for t, v := range a.fnReads[ref.fn] {
		if d := a.o.decls[t]; d != nil {
			ds = append(ds, d)
			vias[d] = v.fn
		}
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i].pos.before(ds[j].pos) })
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = vias[d]
	}
	return ds, out
}

// check reports the violations of both rules.
func (o *topOrder) check(errs *effErrors) {
	if len(o.fieldRefs) == 0 && len(o.writes) == 0 {
		return
	}
	a := o.analyze()
	for _, ref := range o.fieldRefs {
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
			break
		}
	}
	for _, sw := range o.writes {
		var reader *topDecl
		var through string
		for _, d := range o.decls {
			v, ok := a.declReads[d][sw.target]
			if ok && sw.pos.before(d.pos) && (reader == nil || d.pos.before(reader.pos)) {
				reader, through = d, v.fn
			}
		}
		if reader == nil {
			continue
		}
		reads := fmt.Sprintf("reads `%s`", sw.target.name)
		if through != "" {
			reads = fmt.Sprintf("reads `%s` (through `%s`)", sw.target.name, through)
		}
		errs.add(sw.node, fmt.Sprintf(
			"top-level declarations run before top-level statements, so `%s` (line %d) %s before this statement writes it; write `%s` in a declaration instead (e.g. compute it in `%s`'s initializer), or read it in a statement",
			reader.name, reader.pos.line, reads, sw.target.name, reader.name))
	}
}

func fieldRefMessage(ref fieldRef, d *topDecl, through, writes string) string {
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
	reads := fmt.Sprintf("`%s` reads `%s`", ref.name, d.name)
	if through != "" && through != ref.name {
		reads = fmt.Sprintf("`%s` reads `%s` (through `%s`)", ref.name, d.name, through)
	}
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
