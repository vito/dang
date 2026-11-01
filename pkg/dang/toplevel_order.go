package dang

import (
	"context"
	"fmt"

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
// So a declaration whose initializer writes top-level state (directly, through
// a block it runs, or by forcing another such declaration) must not be forced
// from a `{{ }}` field: two fields could run initializers writing the same
// binding at once. Declarations earlier in source order have already run when
// a later one's initializer runs, so only forward references are a problem
// there; a function body may run while initialization is still under way, so
// any reference from one counts.
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
	// writes names a top-level binding its initializer may write: directly,
	// through a closure it runs, or by forcing another declaration that does.
	writes string
	// reads holds the top-level bindings its initializer reads.
	reads map[effTarget]bool
}

// topForm is the top-level form being inferred: a declaration's
// initializer, or a statement (decl == nil).
type topForm struct {
	decl *topDecl
	pos  topPos
}

type topOrder struct {
	files map[hm.Env]int
	decls map[effTarget]*topDecl
}

type topFormKey struct{}

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
// target (at node), outside any function body. A statement may not write a
// binding that a declaration after it reads: declarations all run before
// statements, so the declaration would see the value from before the write.
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
	var reader *topDecl
	for _, d := range topOrderFrom(ctx).decls {
		if d.reads[target] && f.pos.before(d.pos) && (reader == nil || d.pos.before(reader.pos)) {
			reader = d
		}
	}
	if reader == nil {
		return
	}
	_ = reportEffectError(ctx, NewInferError(fmt.Errorf(
		"top-level declarations run before top-level statements, so `%s` (line %d) reads `%s` before this statement writes it; write `%s` in a declaration instead (e.g. compute it in `%s`'s initializer), or read it in a statement",
		reader.name, reader.pos.line, target.name, target.name, reader.name), node))
}

// noteTopRead handles a read (at node) of name, resolved in env: a
// declaration's initializer records what it reads; forcing a declaration
// whose initializer writes top-level state may write it too, and forcing one
// from a `{{ }}` field may run it concurrently.
func noteTopRead(ctx context.Context, env hm.Env, name string, node Node) error {
	o := topOrderFrom(ctx)
	if o == nil {
		return nil
	}
	frame := resolveBinding(env, name)
	if frame == nil {
		return nil
	}
	target := effTarget{frame: frame, name: name}
	f := topFormFrom(ctx)
	if f != nil && f.decl != nil && isTopLevelFrame(frame, topLevelFrameFrom(ctx)) {
		if f.decl.reads == nil {
			f.decl.reads = map[effTarget]bool{}
		}
		f.decl.reads[target] = true
	}
	d := o.decls[target]
	if d == nil || d.writes == "" {
		return nil
	}
	if f != nil && f.decl != nil && f.decl != d && f.decl.writes == "" {
		f.decl.writes = d.writes
	}
	if !inField(ctx) {
		return nil
	}
	switch {
	case inFunctionBody(ctx):
		return reportEffectError(ctx, NewInferError(fmt.Errorf(
			"%s; reading `%s` here may run its initializer, which may write `%s`, while other fields run: this code may run while top-level declarations are still being initialized; read `%s` outside the {{ }}, in a top-level statement, or pass it in",
			concurrentFields, name, d.writes, name), node))
	case f != nil && f.decl != nil && f.decl.pos.before(d.pos):
		return reportEffectError(ctx, NewInferError(fmt.Errorf(
			"%s; `%s` is declared after `%s`, so reading it here runs its initializer, which may write `%s`, while other fields run; declare `%s` before `%s`, or read it before the {{ }} (`let %s1 = %s`)",
			concurrentFields, name, f.decl.name, d.writes, name, f.decl.name, name, name), node))
	}
	return nil
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
