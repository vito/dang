package dang

import (
	"context"
	"fmt"
)

// `{{ }}` purity.
//
// `{{ }}` evaluates its fields concurrently. Within a field, these are
// errors:
//
//   - calling a closure created outside the field that writes anything it
//     does not declare: those writes reach shared state, concurrently;
//   - calling a function-typed parameter: that makes the parameter "must be
//     pure", checked at callers (effects.go);
//   - writing a binding declared outside the field (the method's locals,
//     args, or self fields; top-level bindings are already immutable),
//     directly or from a closure created inside the field. At runtime such
//     writes land in the field's private fork and would be silently
//     discarded when it finishes, so they are rejected as surprising even
//     though they do not race.
//
// Everything here is decided on the spot, in source order: closure effects
// are complete before the closure can be referenced, and named functions and
// methods never write shared state.

const concurrentFields = "{{ }} fields run concurrently"

// withFieldFrame enters one `{{ }}` field (a record literal field, a selected
// field, or a field selected across a list).
func withFieldFrame(ctx context.Context, own TypeScope) context.Context {
	fr := &effFrame{parent: effFrameFrom(ctx), field: true}
	if own != nil {
		fr.owns = append(fr.owns, own)
	}
	return context.WithValue(ctx, effFrameKey{}, fr)
}

// enclosingFields calls fn for each field frame enclosing the current
// position, innermost first.
func enclosingFields(ctx context.Context, fn func(field *effFrame)) {
	for fr := effFrameFrom(ctx); fr != nil; fr = fr.parent {
		if fr.field {
			fn(fr)
		}
	}
}

// fieldDeclares reports whether field encloses the frame a closure was
// created in.
func fieldDeclares(field *effFrame, created *effFrame) bool {
	for fr := created; fr != nil; fr = fr.parent {
		if fr == field {
			return true
		}
	}
	return false
}

// declaredWithin reports whether target is declared inside field: by the
// field itself, or by a function body created inside it.
func declaredWithin(ctx context.Context, field *effFrame, target TypeScope) bool {
	for fr := effFrameFrom(ctx); fr != nil && fr != field; fr = fr.parent {
		if !fr.field && fr.inside(target) {
			return true
		}
	}
	return field.inside(target)
}

func checkFieldWrite(ctx context.Context, target effTarget, node Node) {
	reported := false
	enclosingFields(ctx, func(field *effFrame) {
		if reported || declaredWithin(ctx, field, target.frame) {
			return
		}
		reported = true
		_ = reportEffectError(ctx, NewInferError(fmt.Errorf(
			"%s; this assignment writes `%s`, which is declared outside the field", concurrentFields, target.name), node))
	})
}

func checkFieldCall(ctx context.Context, callee *Effect, node Node, name string) {
	switch callee.kind {
	case effParam:
		// A parameter called inside a field, within the function owning it,
		// must be bound to a pure function.
		inField := false
		for fr := effFrameFrom(ctx); fr != nil && fr.eff != callee.owner; fr = fr.parent {
			if fr.field {
				inField = true
				break
			}
		}
		if inField {
			requirePure(callee, true, fmt.Sprintf("`%s` calls `%s` inside {{ }}, whose fields run concurrently", callee.owner.name, callee.name))
		}
	case effClosure:
		reported := false
		enclosingFields(ctx, func(field *effFrame) {
			if reported || fieldDeclares(field, callee.createdIn) {
				// Writes of a closure created inside the field were checked
				// as it was inferred, and its parameter calls too.
				return
			}
			for p := range callee.params {
				requirePure(p, true, fmt.Sprintf("`%s` calls `%s`, which is called inside {{ }}, whose fields run concurrently", callee.name, p.name))
			}
			if callee.impure() {
				reported = true
				_ = reportEffectError(ctx, NewInferError(fmt.Errorf(
					"%s; `%s` %s", concurrentFields, name, callee.describeWrites(name)), node))
			}
		})
	}
}
