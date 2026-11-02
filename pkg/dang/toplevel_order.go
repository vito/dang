package dang

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/vito/dang/v2/pkg/hm"
)

// Top-level initialization.
//
// A module's top-level declarations (`let x = ...`, `pub x = ...`) are lazy
// fields: EvaluateFormsWithPhases installs them all, then forces them in
// source order, and only then runs the top-level statements. A declaration
// read before its turn is forced on that read, wherever it happens, including
// inside a `{{ }}` field, whose fields run concurrently. Two rules make that
// safe without following function values anywhere:
//
//  1. Initializers write only locals. A declaration's initializer, including
//     the blocks and `{{ }}` fields written in it, may not write any
//     top-level binding. Named functions and methods can't either
//     (purity.go), and impure closures can't be stored (effects.go), so
//     initializers share no mutable state and forcing them concurrently
//     can't race, however they are reached.
//  2. Statement-written state stays in statements. Let W be the top-level
//     bindings some top-level statement writes (in a block or nested
//     function written in one too; `box.n = v` writes `box`). Only statement
//     code may read W: a declaration ran before every statement, and a
//     function's, method's or type's code may run while declarations are
//     initialized, so either could see a value from before the write.
//
// Both are syntactic. W lives on the effect solver, so it spans every file of
// a directory module, and, since each REPL, literate-doc or playground entry
// is inferred with a solver of its own, one entry: a later entry's
// declarations run after an earlier entry's statements.

// topForm is the top-level form being inferred: a declaration's initializer
// (decl is its name) or a statement (decl == ""), of the module whose
// top-level frame is top.
type topForm struct {
	decl string
	top  TypeScope
}

// topOrder holds what rule 2 needs once everything is inferred: function and
// type bodies are inferred before statements.
type topOrder struct {
	// written is W: each binding some top-level statement writes, with its
	// first write.
	written map[effTarget]Node
	// reads are the reads of top-level bindings outside statement code.
	reads []outsideRead
}

// outsideRead is a read (at node) of target from code other than top-level
// statements: reader names that code, kind says what it is.
type outsideRead struct {
	target effTarget
	node   Node
	reader string
	kind   readerKind
}

type readerKind int

const (
	readerDecl readerKind = iota
	readerFunction
	readerType
)

type topFormKey struct{}

func topOrderFrom(ctx context.Context) *topOrder {
	if s, ok := ctx.Value(effSolverKey{}).(*effectSolver); ok {
		return &s.top
	}
	return nil
}

// topFormFrom returns the top-level form being inferred. A Dang module
// imported from inside a form is compiled with the importer's ctx, under a
// top-level frame of its own: the importer's form says nothing about the
// module's code, so it is ignored there.
func topFormFrom(ctx context.Context) *topForm {
	f, _ := ctx.Value(topFormKey{}).(*topForm)
	if f == nil || f.top != topLevelFrameFrom(ctx) {
		return nil
	}
	return f
}

// topLevelEnv reports whether env is the module's top-level scope, as opposed
// to a type or statics body inferred with the same phases.
func topLevelEnv(ctx context.Context, env hm.Env) bool {
	top := topLevelFrameFrom(ctx)
	return top != nil && envFrame(env) == top
}

// withTopDecl marks ctx as inferring the initializer of top-level
// declaration form (in env).
func withTopDecl(ctx context.Context, env hm.Env, form Node) context.Context {
	field, ok := form.(*FieldDecl)
	if topOrderFrom(ctx) == nil || !ok || !topLevelEnv(ctx, env) {
		return ctx
	}
	return context.WithValue(ctx, topFormKey{}, &topForm{decl: field.Name.Name, top: topLevelFrameFrom(ctx)})
}

// withTopStatement marks ctx as inferring a top-level statement (in env).
func withTopStatement(ctx context.Context, env hm.Env) context.Context {
	if topOrderFrom(ctx) == nil || !topLevelEnv(ctx, env) {
		return ctx
	}
	return context.WithValue(ctx, topFormKey{}, &topForm{top: topLevelFrameFrom(ctx)})
}

// checkInitializerWrite enforces rule 1: it reports a write (at node) of
// top-level binding target from a declaration's initializer. Function bodies
// are left to checkTopLevelWrite, which rejects their top-level writes
// everywhere.
func checkInitializerWrite(ctx context.Context, target effTarget, node Node) {
	f := topFormFrom(ctx)
	if f == nil || f.decl == "" || inFunctionBody(ctx) ||
		!isTopLevelFrame(target.frame, topLevelFrameFrom(ctx)) {
		return
	}
	_ = reportEffectError(ctx, NewInferError(fmt.Errorf(
		"cannot assign to `%s` in `%s`'s initializer: top-level declarations run lazily on first read, maybe concurrently from {{ }} fields, so an initializer may only write its own locals; move the write into a top-level statement, or build the value in a local and return it",
		target.name, f.decl), node))
}

// noteStatementWrite adds target to W when the write (at node) is statement
// code.
func noteStatementWrite(ctx context.Context, target effTarget, node Node) {
	f := topFormFrom(ctx)
	if f == nil || f.decl != "" || !isTopLevelFrame(target.frame, topLevelFrameFrom(ctx)) {
		return
	}
	o := topOrderFrom(ctx)
	if o.written == nil {
		o.written = map[effTarget]Node{}
	}
	if _, ok := o.written[target]; !ok {
		o.written[target] = node
	}
}

// noteTopRead records a read (at node) of name, resolved in env, from code
// that isn't statement code: a declaration's initializer, or the body of a
// function, method, constructor or type (field defaults, statics).
func noteTopRead(ctx context.Context, env hm.Env, name string, node Node) {
	o := topOrderFrom(ctx)
	f := topFormFrom(ctx)
	if o == nil || (f != nil && f.decl == "") {
		return
	}
	frame := resolveBinding(env, name)
	if !isTopLevelFrame(frame, topLevelFrameFrom(ctx)) {
		return
	}
	r := outsideRead{target: effTarget{frame: frame, name: name}, node: node}
	if f != nil && !inFunctionBody(ctx) {
		r.reader, r.kind = f.decl, readerDecl
	} else {
		r.reader, r.kind = "this code", readerFunction
		for fr := effFrameFrom(ctx); fr != nil; fr = fr.parent {
			if fr.field || fr.block {
				continue
			}
			r.reader = fr.eff.name
			if fr.typeBody {
				r.kind = readerType
			}
			break
		}
	}
	o.reads = append(o.reads, r)
}

// check enforces rule 2: it reports reads of W outside statement code, once
// per reader and binding.
func (o *topOrder) check(errs *effErrors) {
	if len(o.written) == 0 {
		return
	}
	seen := map[string]bool{}
	for _, r := range o.reads {
		w, ok := o.written[r.target]
		if !ok {
			continue
		}
		key := fmt.Sprintf("%d:%s:%s", r.kind, r.reader, r.target.name)
		if seen[key] {
			continue
		}
		seen[key] = true
		errs.add(r.node, outsideReadMessage(r, w))
	}
}

func outsideReadMessage(r outsideRead, write Node) string {
	name := r.target.name
	at := writeSite(r.node, write)
	switch r.kind {
	case readerDecl:
		return fmt.Sprintf(
			"`%s`'s initializer reads `%s`, which a top-level statement writes (%s), but top-level declarations run before top-level statements, so it would see the value from before the write; read `%s` in a top-level statement instead, or compute it in a declaration rather than writing it from statements",
			r.reader, name, at, name)
	case readerType:
		return fmt.Sprintf(
			"the body of `%s` reads `%s`, which a top-level statement writes (%s), but field defaults may run while top-level declarations are initialized, before any statement, so they could see the value from before the write; pass `%s` in as an argument instead",
			r.reader, name, at, name)
	default:
		return fmt.Sprintf(
			"`%s` reads `%s`, which a top-level statement writes (%s), but functions may be called while top-level declarations are initialized, before any statement, so it could see the value from before the write; pass `%s` as an argument instead",
			r.reader, name, at, name)
	}
}

// writeSite locates write for a message about read: its line, and its file
// when that differs.
func writeSite(read, write Node) string {
	wl := write.GetSourceLocation()
	if wl == nil {
		return "elsewhere"
	}
	if rl := read.GetSourceLocation(); rl != nil && wl.Filename != "" && rl.Filename != wl.Filename {
		return fmt.Sprintf("%s:%d", filepath.Base(wl.Filename), wl.Line)
	}
	return fmt.Sprintf("line %d", wl.Line)
}
