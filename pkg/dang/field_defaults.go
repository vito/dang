package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// Field defaults are scoped like constructor parameter defaults.
//
// A type's field defaults (`x: T = expr`, `let x = expr`) run once, while the
// object is being built: before new(), and before any later copy-on-write.
// Without an explicit new() they are the constructor's parameter defaults
// (`Computed("hello").combined`), so they are scoped like them: they see the
// other fields' values and the enclosing scope, but not `self`, and not the
// type's methods and computed fields, which belong to the finished object. A
// field only new() sets has no value yet, so it can't be used either. And
// like a parameter default, a field default can't assign other fields: a
// bare `x = expr` in the type body (or in a block of a default) would
// override x's default and any constructor argument behind the back of what
// other defaults already read.
//
// What a default reads is then always the value the object is built with,
// including for closures created there: neither new() nor any later write
// reaches them, so they can go anywhere, like any closure that only reads its
// scope.
//
// Statics (`self { }` members) may use `self` and each other, since they are
// evaluated once and immutable, but their block can't reassign them either.
//
// Ordering is loose: defaults are inferred in dependency order and forced
// lazily at runtime, so a default may refer to a field declared after it.

const fieldDefaultScope = "they run while the object is being built, so they only see the other fields' values; use a computed field or new()"

type typeBodyKey struct{}

// typeBody is a type body whose field defaults are being inferred: its object
// type, the members its field defaults can't use (see unavailableInDefaults),
// and the type body it is nested in, when the type is declared in another
// type's body (directly or in one of its field defaults). For a `self { }`
// block, statics is set and object is the meta-type: only the write check
// applies.
type typeBody struct {
	object      *Type
	unavailable map[string]memberKind
	statics     bool
	outer       *typeBody
}

func typeBodyFrom(ctx context.Context) *typeBody {
	tb, _ := ctx.Value(typeBodyKey{}).(*typeBody)
	return tb
}

// withTypeBody marks ctx as inferring object's field defaults.
func withTypeBody(ctx context.Context, object *Type, unavailable map[string]memberKind) context.Context {
	return context.WithValue(ctx, typeBodyKey{}, &typeBody{
		object:      object,
		unavailable: unavailable,
		outer:       typeBodyFrom(ctx),
	})
}

// withStaticsBody marks ctx as inferring the `self { }` members of the type
// whose meta-type is meta.
func withStaticsBody(ctx context.Context, meta *Type) context.Context {
	return context.WithValue(ctx, typeBodyKey{}, &typeBody{
		object:  meta,
		statics: true,
		outer:   typeBodyFrom(ctx),
	})
}

// withoutTypeBody leaves the innermost type body, for a method's body: methods
// run against their receiver, not the object under construction. The type
// bodies the type itself is declared in still apply (see checkDefaultName).
func withoutTypeBody(ctx context.Context) context.Context {
	tb := typeBodyFrom(ctx)
	if tb == nil {
		return ctx
	}
	return context.WithValue(ctx, typeBodyKey{}, tb.outer)
}

// outsideTypeBodies leaves every type body, for a name that isn't resolved in
// scope: the field names of a `{{ }}` selection, which belong to the
// receiver.
func outsideTypeBodies(ctx context.Context) context.Context {
	if typeBodyFrom(ctx) == nil {
		return ctx
	}
	return context.WithValue(ctx, typeBodyKey{}, (*typeBody)(nil))
}

// memberKind is what a member a field default can't use is.
type memberKind int

const (
	methodMember memberKind = iota
	computedMember
	newOnlyMember
)

// unavailableInDefaults maps the members of the type declared by forms that
// its field defaults can't use to what they are: its methods and computed
// fields, and, when the type has an explicit new(), the fields without a
// default, which only new() sets.
func unavailableInDefaults(forms []Node, explicitNew bool) map[string]memberKind {
	unavailable := map[string]memberKind{}
	for _, form := range forms {
		field, ok := form.(*FieldDecl)
		if !ok {
			continue
		}
		name := field.Name.Name
		switch fd := field.funDecl(); {
		case fd != nil && (len(fd.Args) > 0 || fd.BlockParam != nil):
			unavailable[name] = methodMember
		case fd != nil:
			unavailable[name] = computedMember
		case field.Value == nil && explicitNew:
			unavailable[name] = newOnlyMember
		}
	}
	return unavailable
}

// checkDefaultName rejects name, resolved in env, when it is a member of a
// type whose body is being inferred that its field defaults can't use. That
// includes code in a type declared in such a body, methods and all: they are
// created while the enclosing object is being built, and would keep using
// it.
func checkDefaultName(ctx context.Context, env hm.Env, name string) error {
	for tb := typeBodyFrom(ctx); tb != nil; tb = tb.outer {
		kind, ok := tb.unavailable[name]
		if !ok || resolveBinding(env, name) != TypeScope(tb.object) {
			continue
		}
		owner := tb.object.Named
		if inner, ok := selfTypeFrame(env).(*Type); ok && inner != tb.object {
			what := fmt.Sprintf("a method of %s", owner)
			switch kind {
			case computedMember:
				what = fmt.Sprintf("a computed field of %s", owner)
			case newOnlyMember:
				what = fmt.Sprintf("a field only %s's new() sets", owner)
			}
			return fmt.Errorf("`%s` is declared in %s's body, so it can't use `%s`, %s: %s is still being built when %s is created; declare %s outside %s and pass it what it needs",
				inner.Named, owner, name, what, owner, inner.Named, inner.Named, owner)
		}
		switch kind {
		case methodMember:
			return fmt.Errorf("field defaults can't use `%s`, a method of %s: %s", name, owner, fieldDefaultScope)
		case computedMember:
			return fmt.Errorf("field defaults can't use `%s`, a computed field of %s: %s", name, owner, fieldDefaultScope)
		default:
			return fmt.Errorf("field defaults can't use `%s`: it has no default, so only %s's new() sets it, after the defaults run; use it in new() or a computed field", name, owner)
		}
	}
	return nil
}

// checkDefaultSelf rejects `self` (resolved in env) in a field default.
func checkDefaultSelf(ctx context.Context, env hm.Env) error {
	self := selfTypeFrame(env)
	for tb := typeBodyFrom(ctx); tb != nil; tb = tb.outer {
		if !tb.statics && self == TypeScope(tb.object) {
			return fmt.Errorf("field defaults can't use `self`: %s", fieldDefaultScope)
		}
	}
	return nil
}

// checkDefaultWrite rejects an assignment (target) to a field of a type
// whose body is being inferred, outside its methods: directly in the body,
// in a default's block, or in a type declared there. Locals declared there
// can still be written; methods, computed fields and new() leave the body.
func checkDefaultWrite(ctx context.Context, env hm.Env, target Node) error {
	name, isSelf, _, ok := assignmentRoot(target)
	if !ok || isSelf {
		// `self.x = …` is rejected as a use of `self`.
		return nil
	}
	for tb := typeBodyFrom(ctx); tb != nil; tb = tb.outer {
		if resolveBinding(env, name) != TypeScope(tb.object) {
			continue
		}
		owner := tb.object.Named
		if tb.statics {
			return fmt.Errorf("statics can't be reassigned in their `self { }` block: they are initialized once, from their defaults; give `%s` a different default", name)
		}
		if inner, ok := selfTypeFrame(env).(*Type); ok && inner != tb.object {
			return fmt.Errorf("`%s` is declared in %s's body, so it can't reassign `%s`, a field of %s: %s is still being built when %s is created; declare %s outside %s and pass it what it needs",
				inner.Named, owner, name, owner, owner, inner.Named, inner.Named, owner)
		}
		return fmt.Errorf("fields can't be reassigned in a type body, which runs while the object is being built; give `%s` a different default, or set it in new()", name)
	}
	return nil
}
