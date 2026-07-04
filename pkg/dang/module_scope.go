package dang

import "context"

// moduleScope is an opaque, per-compilation-unit identity used to enforce
// module-level visibility of `let` members. A directory module, a single-file
// program, and the prelude each get exactly one. Two Dang-declared types belong
// to the same module iff they carry the same *moduleScope (compared by pointer;
// label is for debugging/errors only).
//
// The rule it enforces: a `let` (private) member is reachable anywhere within
// its home module — any file, any sibling type, via `self` or otherwise — but
// rejected from another module. This mirrors Dang's top-level `let`, which is
// already module-private (visible across a module's files, not exported across
// the import boundary). See type-statics.md and the enforcement in
// Select.Infer.
type moduleScope struct {
	label string
}

type moduleScopeKey struct{}

// withModuleScope tags ctx with the identity of the module whose code is being
// hoisted/inferred. Set once at each program entry point (RunFile, RunDir,
// loadPrelude). Types declared under this ctx are stamped with m; member
// accesses inferred under it compare against m.
func withModuleScope(ctx context.Context, m *moduleScope) context.Context {
	return context.WithValue(ctx, moduleScopeKey{}, m)
}

// moduleScopeFromContext returns the current module identity, or nil when the
// entry point did not set one. A nil result disables visibility enforcement
// (fail-open): enforcement only ever rejects a genuine cross-module access, so
// an un-instrumented path silently under-enforces rather than misfiring.
func moduleScopeFromContext(ctx context.Context) *moduleScope {
	m, _ := ctx.Value(moduleScopeKey{}).(*moduleScope)
	return m
}
