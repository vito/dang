package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// Top-level bindings are immutable once initialized: no function, method,
// new(), type body, or closure inside one may write one. That makes every
// named function and method free of shared-state effects (their own `self`
// writes are forks), which is what lets closure purity (and the `{{ }}`
// check) stay lexical.
//
// Top-level script code — top-level forms, and the blocks lexically inside
// them outside any function, method, or type body — may still reassign
// top-level bindings while the module initializes, e.g.
// `let total = 0; xs.each { x => total += x }`. A REPL prompt is script code
// too, so reassigning at the prompt keeps working.

type topLevelFrameKey struct{}

type functionBodyKey struct{}

// withFunctionBody marks ctx as inferring the body of a named function,
// method, constructor, or type body, as opposed to top-level script code.
func withFunctionBody(ctx context.Context) context.Context {
	return context.WithValue(ctx, functionBodyKey{}, true)
}

func inFunctionBody(ctx context.Context) bool {
	in, _ := ctx.Value(functionBodyKey{}).(bool)
	return in
}

// withTopLevelFrame records the frame top-level declarations of the module
// (or REPL session) being inferred land in.
func withTopLevelFrame(ctx context.Context, env hm.Env) context.Context {
	frame := envFrame(env)
	if frame == nil {
		return ctx
	}
	return context.WithValue(ctx, topLevelFrameKey{}, frame)
}

func topLevelFrameFrom(ctx context.Context) TypeScope {
	f, _ := ctx.Value(topLevelFrameKey{}).(TypeScope)
	return f
}

// isTopLevelFrame reports whether frame is the top-level frame top or one it
// extends: a session fork (a literate failure snippet) or a file's clone of
// its module scope still sees the outer top-level bindings as top-level.
func isTopLevelFrame(frame, top TypeScope) bool {
	if frame == nil {
		return false
	}
	for cur := top; cur != nil; {
		if cur == frame {
			return true
		}
		t, ok := cur.(*Type)
		if !ok || t.Parent == nil {
			return false
		}
		cur = t.Parent
	}
	return false
}

// envFrame returns the frame new bindings in env are added to.
func envFrame(env hm.Env) TypeScope {
	switch e := env.(type) {
	case *OverlayTypeScope:
		return envFrame(e.primary)
	case *Type:
		return e
	}
	return nil
}

// resolveBinding returns the frame declaring name as seen from env, mirroring
// SchemeOf's lookup order, or nil when it is not a Dang-declared binding.
func resolveBinding(env hm.Env, name string) TypeScope {
	switch e := env.(type) {
	case *OverlayTypeScope:
		if f := resolveBinding(e.primary, name); f != nil {
			return f
		}
		return resolveBinding(e.lexical, name)
	case *Type:
		if _, ok := e.vars[name]; ok && !e.narrowed[name] {
			return e
		}
		if e.Parent != nil {
			return resolveBinding(e.Parent, name)
		}
	}
	return nil
}

// assignmentRoot returns the binding an assignment rebinds: the root symbol of
// `x = v` / `x.a.b = v` (name), or isSelf for `self.a.b = v`. first is the
// first field on a path, for messages. ok is false for unsupported targets.
func assignmentRoot(target Node) (name string, isSelf bool, first string, ok bool) {
	switch t := target.(type) {
	case *Symbol:
		return t.Name, false, "", true
	case *Select:
		var node Node = t
		first = t.Field.Name
		for {
			sel, isSel := node.(*Select)
			if !isSel {
				break
			}
			first = sel.Field.Name
			node = sel.Receiver
		}
		switch root := node.(type) {
		case *Symbol:
			return root.Name, false, first, true
		case *SelfKeyword:
			return "", true, first, true
		}
	}
	return "", false, "", false
}

// checkTopLevelWrite rejects an assignment whose root is a top-level binding.
func checkTopLevelWrite(ctx context.Context, env hm.Env, r *Reassignment) error {
	top := topLevelFrameFrom(ctx)
	if top == nil {
		return nil
	}
	if !inFunctionBody(ctx) {
		return nil
	}
	name, isSelf, _, ok := assignmentRoot(r.Target)
	if !ok || isSelf {
		return nil
	}
	if !isTopLevelFrame(resolveBinding(env, name), top) {
		return nil
	}
	return NewInferError(
		fmt.Errorf("cannot assign to `%s`: top-level bindings are immutable once initialized; use a local `let`, a field, or return the new value", name),
		r,
	)
}
