package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// Top-level bindings are immutable once initialized: no code — a free
// function, method, new(), block, nested function, or a top-level
// reassignment form — may write one. That makes every named function and
// method free of shared-state effects (their own `self` writes are forks), which
// is what lets closure purity (and the `{{ }}` check) stay lexical.

type topLevelFrameKey struct{}

// allowTopLevelScriptWrites relaxes the rule for top-level script code: forms
// at the top level (and blocks lexically inside them, outside any function,
// method, or type body) may still reassign top-level bindings while the module
// initializes, e.g. `let total = 0; xs.each { x => total += x }`. Functions,
// methods and constructors are effect-free either way. Turning it off rejects
// every top-level reassignment (fixing issue #196's ordering surprise by
// rejection) but breaks top-level scripting idioms used across the tests and
// docs.
const allowTopLevelScriptWrites = true

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
// being inferred land in. Entry points that compile a module or file set it;
// the REPL does not, so prompt-level reassignment keeps working there.
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
		if _, ok := e.vars[name]; ok {
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
	if allowTopLevelScriptWrites && !inFunctionBody(ctx) {
		return nil
	}
	name, isSelf, _, ok := assignmentRoot(r.Target)
	if !ok || isSelf {
		return nil
	}
	if resolveBinding(env, name) != top {
		return nil
	}
	return NewInferError(
		fmt.Errorf("cannot assign to `%s`: top-level bindings are immutable once initialized; use a local `let`, a field, or return the new value", name),
		r,
	)
}
