package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// Field defaults are scoped like constructor parameter defaults.
//
// A type's field defaults (`x: T = expr`, `let x = expr`) and the other forms
// in its body that aren't methods or computed fields (`x = expr`) run once,
// while the object is being built: before new(), and before any later
// copy-on-write. Without an explicit new() they are the constructor's
// parameter defaults (`Computed("hello").combined`), so they are scoped like
// them: they see the other fields' values and the enclosing scope, but not
// `self`, and not the type's methods and computed fields, which belong to the
// finished object. A field only new() sets has no value yet, so it can't be
// used either. What a default reads is then always a value the object is
// built with, including for closures created there: they read the fields as
// the type body leaves them (a bare `x = expr` in the body included), and
// neither new() nor any later write reaches them, so they can go anywhere,
// like any closure that only reads its scope.
//
// Ordering is loose: defaults are inferred in dependency order and forced
// lazily at runtime, so a default may refer to a field declared after it.

const fieldDefaultScope = "they run while the object is being built, so they only see the other fields' values; use a computed field or new()"

type typeBodyKey struct{}

// typeBody is a type body whose field defaults are being inferred: its object
// type, the members its field defaults can't use (see unavailableInDefaults),
// and the type body it is nested in, when the type is declared in another
// type's field default.
type typeBody struct {
	object      *Type
	unavailable map[string]string
	outer       *typeBody
}

func typeBodyFrom(ctx context.Context) *typeBody {
	tb, _ := ctx.Value(typeBodyKey{}).(*typeBody)
	return tb
}

// withTypeBody marks ctx as inferring object's field defaults.
func withTypeBody(ctx context.Context, object *Type, unavailable map[string]string) context.Context {
	return context.WithValue(ctx, typeBodyKey{}, &typeBody{
		object:      object,
		unavailable: unavailable,
		outer:       typeBodyFrom(ctx),
	})
}

// withoutTypeBody leaves the innermost type body, for a method's body (methods
// run against their receiver, not the object under construction) and for the
// field names of a `{{ }}` selection (resolved against the receiver's type,
// they aren't names in scope).
func withoutTypeBody(ctx context.Context) context.Context {
	tb := typeBodyFrom(ctx)
	if tb == nil {
		return ctx
	}
	return context.WithValue(ctx, typeBodyKey{}, tb.outer)
}

// unavailableInDefaults maps the members of the type declared by forms that
// its field defaults can't use to the error for using one: its methods and
// computed fields, and, when the type has an explicit new(), the fields
// without a default, which only new() sets.
func unavailableInDefaults(typeName string, forms []Node, explicitNew bool) map[string]string {
	unavailable := map[string]string{}
	for _, form := range forms {
		field, ok := form.(*FieldDecl)
		if !ok {
			continue
		}
		name := field.Name.Name
		switch fd := field.funDecl(); {
		case fd != nil && (len(fd.Args) > 0 || fd.BlockParam != nil):
			unavailable[name] = fmt.Sprintf("field defaults can't use `%s`, a method of %s: %s", name, typeName, fieldDefaultScope)
		case fd != nil:
			unavailable[name] = fmt.Sprintf("field defaults can't use `%s`, a computed field of %s: %s", name, typeName, fieldDefaultScope)
		case field.Value == nil && explicitNew:
			unavailable[name] = fmt.Sprintf("field defaults can't use `%s`: it has no default, so only %s's new() sets it, after the defaults run; use it in new() or a computed field", name, typeName)
		}
	}
	return unavailable
}

// checkDefaultName rejects name, resolved in env, when it is a member of a
// type whose field default is being inferred that defaults can't use.
func checkDefaultName(ctx context.Context, env hm.Env, name string) error {
	for tb := typeBodyFrom(ctx); tb != nil; tb = tb.outer {
		if msg, ok := tb.unavailable[name]; ok && resolveBinding(env, name) == TypeScope(tb.object) {
			return fmt.Errorf("%s", msg)
		}
	}
	return nil
}

// checkDefaultSelf rejects `self` (resolved in env) in a field default.
func checkDefaultSelf(ctx context.Context, env hm.Env) error {
	self := selfTypeFrame(env)
	for tb := typeBodyFrom(ctx); tb != nil; tb = tb.outer {
		if self == TypeScope(tb.object) {
			return fmt.Errorf("field defaults can't use `self`: %s", fieldDefaultScope)
		}
	}
	return nil
}
