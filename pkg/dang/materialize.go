package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

// Coerce wraps an expression so that its value is materialized at the given
// target type at runtime. Inference inserts Coerce nodes at boundaries where a
// value flows into a typed context (field init, reassignment, call arguments,
// function returns, type hints), so the evaluator does not need to remember to
// call materializeValue at each handoff site.
type Coerce struct {
	InferredTypeHolder
	Expr   Node
	Target hm.Type
	Path   string

	// ConvertScalars is set on an explicit cast between custom scalars
	// (scalarCastable): their values become Target's scalars first (see
	// convertScalars).
	ConvertScalars bool
}

var _ Node = (*Coerce)(nil)
var _ Evaluator = (*Coerce)(nil)

func (c *Coerce) DeclaredSymbols() []string   { return nil }
func (c *Coerce) ReferencedSymbols() []string { return c.Expr.ReferencedSymbols() }
func (c *Coerce) Body() hm.Expression         { return c.Expr }
func (c *Coerce) GetSourceLocation() *SourceLocation {
	if sl, ok := c.Expr.(SourceLocatable); ok {
		return sl.GetSourceLocation()
	}
	return nil
}

func (c *Coerce) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(c, func() (hm.Type, error) {
		if _, err := c.Expr.Infer(ctx, env, fresh); err != nil {
			return nil, err
		}
		c.SetInferredType(c.Target)
		return c.Target, nil
	})
}

func (c *Coerce) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, c, func() (Value, error) {
		val, err := EvalNode(ctx, scope, c.Expr)
		if err != nil {
			return nil, err
		}
		if c.ConvertScalars {
			val, err = convertScalars(ctx, scope, val, c.Target, c.Path)
			if err != nil {
				return nil, err
			}
		}
		return materializeValue(ctx, scope, val, c.Target, c.Path)
	})
}

func (c *Coerce) Walk(fn func(Node) bool) {
	if !fn(c) {
		return
	}
	c.Expr.Walk(fn)
}

// isLiteralExpr reports whether n is a syntactic literal whose value is
// statically known and may auto-coerce to a compatible scalar/enum at a
// value-handoff boundary. Templates count regardless of interpolations: the
// runtime value is always a String, so materialization works the same way.
// Lists count when every element is itself literal, and maps when every value
// is (keys are always String!, so they never coerce); a Block counts when its
// tail expression is literal, so a function body like `{ "abc" }` flows into
// a declared ID! return type.
func isLiteralExpr(n Node) bool {
	switch v := n.(type) {
	case *String, *Int, *Float, *Boolean, *Template:
		return true
	case *List:
		for _, el := range v.Elements {
			if !isLiteralExpr(el) {
				return false
			}
		}
		return true
	case *MapLiteral:
		for _, entry := range v.Entries {
			if !isLiteralExpr(entry.Value) {
				return false
			}
		}
		return true
	case *Block:
		if len(v.Forms) == 0 {
			return false
		}
		return isLiteralExpr(v.Forms[len(v.Forms)-1])
	default:
		return false
	}
}

// assignableForValue is hm.Assignable, but it falls back to
// AssignableWithCoercion in the two coercible situations: the value is a
// syntactic literal (which may refine into a scalar/enum), or its type
// carries a custom scalar (which may degrade into String). Use at
// value-handoff boundaries where wrapCoerce will materialize the result. env
// is the environment the value is handed off in, for checkDecodeTarget.
func assignableForValue(env hm.Env, have, want hm.Type, value Node) (hm.Subs, error) {
	subs, err := hm.Assignable(have, want)
	if err == nil {
		return subs, nil
	}
	if isLiteralExpr(value) || containsDegradableScalar(have) {
		if coerceSubs, coerceErr := hm.AssignableWithCoercion(have, want); coerceErr == nil {
			if err := checkEnumLiterals(value, want.Apply(coerceSubs).(hm.Type)); err != nil {
				return nil, err
			}
			return coerceSubs, nil
		}
	}
	if assignableDecoded(have, want) {
		if err := checkDecodeTarget(env, want); err != nil {
			return nil, err
		}
		return hm.NewSubs(), nil
	}
	if nullErr := nullIntoRigid(have, want, value); nullErr != nil {
		return nil, nullErr
	}
	if diag := diagnoseAssignment(have, want); diag != nil {
		return nil, withUnionProvenance(diag, have, want)
	}
	return nil, withUnionProvenance(err, have, want)
}

// nullableRigidError reports a possibly-null value (an index, `map.get`, a
// nullable local) where a generic function's own `a` is wanted, with the
// ways to make it one.
type nullableRigidError struct {
	want hm.Type
	hint string
}

func (e *nullableRigidError) Error() string {
	return fmt.Sprintf("cannot use a possibly-null value as %s: callers may pick a non-null type for %s; %s", e.want, e.want, e.hint)
}

// nullIntoRigid returns a nullableRigidError when have is a possibly-null
// form of a rigid want, or nil.
func nullIntoRigid(have, want hm.Type, value Node) error {
	wantTV, ok := want.(hm.TypeVariable)
	if !ok || !wantTV.IsRigid() {
		return nil
	}
	if _, nullable := have.(hm.NullableTypeVariable); !nullable {
		return nil
	}
	expr := ""
	if value != nil {
		// A function body is checked as a whole; its value is its last form.
		for {
			b, ok := unwrapGrouped(value).(*Block)
			if !ok || len(b.Forms) == 0 {
				break
			}
			value = b.Forms[len(b.Forms)-1]
		}
		expr = strings.TrimSpace(Format(unwrapGrouped(value)))
		if strings.Contains(expr, "\n") || strings.Contains(expr, "/*") || len(expr) > 40 {
			expr = ""
		}
	}
	fallback := fmt.Sprintf("where `other` is itself an %s", want)
	var hint string
	switch {
	case isNullNode(value):
		hint = fmt.Sprintf("pass a value of type %s instead", want)
	case expr == "":
		hint = fmt.Sprintf("assert it non-null with `!`, fall back with `?? other` %s, or bind it with `let` and check it for null first", fallback)
	case isSymbolNode(value):
		hint = fmt.Sprintf("check it for null first (`if (%s == null) { raise ... }`), assert it with `%s!`, or fall back with `%s ?? other` %s", expr, expr, expr, fallback)
	default:
		hint = fmt.Sprintf("assert it with `%s!`, fall back with `%s ?? other` %s, or bind it with `let` and check it for null first", expr, expr, fallback)
	}
	return &nullableRigidError{want: want, hint: hint}
}

func isSymbolNode(n Node) bool {
	_, ok := unwrapGrouped(n).(*Symbol)
	return ok
}

func isNullNode(n Node) bool {
	if n == nil {
		return false
	}
	_, ok := unwrapGrouped(n).(*Null)
	return ok
}

// assignableDecoded reports whether a value of type have may flow into want
// because it carries codec-decoded data (DecodedType) that the boundary's
// Coerce will materialize: have is Decoded, or a list/map of it, where want
// has the same list/map shape. A Decoded leaf may enter any type, nullability
// included — materializeDecoded checks the data (null included) against it
// and raises when it does not fit. Every other part of have must already fit
// want, which is why only Decoded itself may cross into a non-null slot.
//
// want must be fully known: admitting Decoded into a type variable (say the
// `a!` of `ident(x: a!): a!`) would leave the variable unbound, so the call's
// result could then be used as anything. (A bare variable never gets here:
// plain unification binds it to Decoded first.)
func assignableDecoded(have, want hm.Type) bool {
	switch h := have.(type) {
	case DecodedType:
		return len(want.FreeTypeVar()) == 0
	case hm.NonNullType:
		if w, ok := want.(hm.NonNullType); ok {
			return assignableDecoded(h.Type, w.Type)
		}
		return assignableDecoded(h.Type, want)
	case ListType:
		switch w := want.(type) {
		case ListType:
			return assignableDecoded(h.Type, w.Type)
		case GraphQLListType:
			return assignableDecoded(h.Type, w.Type)
		}
	case MapType:
		if w, ok := want.(MapType); ok {
			return assignableDecoded(h.Type, w.Type)
		}
	}
	return false
}

// checkDecodeTarget rejects decoding into want at a site inferred in env when
// materializing want would construct a type that has no constructor or shape
// to decode into (see hasDecodableShape), or an object type the site cannot
// reach: a type declared in a function, block or type body (Type.localFrame)
// whose declaring scope does not enclose the site. Such a type has a
// constructor per call or per instance of its declaring scope, found by
// identity in the decoding scope (constructorFor), so from outside that scope
// there is none to call, nor one right one to pick. Its value can still get
// out (an `if` expression, a method's result), so the type can appear here by
// inference.
//
// The walk mirrors materializeDecoded: through non-null, unions, lists and
// maps, an anonymous record's fields, and a named object's constructor
// parameters, except those every codec ignores (they are never decoded). A
// parameter with a default is walked: decoding builds it whenever the data has
// its key. A type with no constructor is only rejected where every value
// decoded there must become one: reached through non-null types, list and map
// elements, and required fields, but not a nullable type, a defaulted
// parameter or a union's option, which data can satisfy without it.
func checkDecodeTarget(env hm.Env, want hm.Type) error {
	type visit struct {
		mod      *Type
		required bool
	}
	seen := map[visit]bool{}
	// via is the field path from want down to t ("Holder.inner"), for the
	// error, or "" while still at want itself. required is whether every
	// value decoded at t must be a t (see above).
	var walk func(t hm.Type, via string, required bool) error
	noConstructor := func(t hm.Type, via string) error {
		if via != "" {
			return fmt.Errorf("cannot decode %s: its type %s has no constructor to decode into", via, t)
		}
		return fmt.Errorf("cannot decode into %s: it has no constructor", t)
	}
	walkFields := func(mod *Type, fields []Keyed[*hm.Scheme], via string, required bool) error {
		for _, field := range fields {
			if ignoredByAllCodecs(mod, field.Key) {
				continue
			}
			ft, mono := field.Value.Type()
			if !mono {
				continue
			}
			next := field.Key
			if via != "" {
				next = via + "." + field.Key
			} else if mod.Named != "" {
				next = mod.Named + "." + field.Key
			}
			if err := walk(ft, next, required); err != nil {
				return err
			}
		}
		return nil
	}
	walk = func(t hm.Type, via string, required bool) error {
		nn, nonNull := t.(hm.NonNullType)
		if nonNull {
			t = nn.Type
		}
		required = required && nonNull
		if union, ok := t.(*hm.UnionType); ok {
			for _, option := range union.Options {
				if err := walk(option, via, false); err != nil {
					return err
				}
			}
			return nil
		}
		if elem, ok := listElementTarget(t); ok {
			return walk(elem, via, required)
		}
		if val, ok := mapValueTarget(t); ok {
			return walk(val, via, required)
		}
		v, ok := t.(*Type)
		if !ok {
			// A scope standing for a type is a namespace, such as a native
			// module's (see dangModule.exportsType): nothing to construct.
			if _, isScope := t.(*OverlayTypeScope); isScope && required {
				return noConstructor(t, via)
			}
			return nil
		}
		mod := canonicalModule(v)
		if seen[visit{mod, required}] {
			return nil
		}
		if !hasDecodableShape(mod) {
			if required {
				return noConstructor(mod, via)
			}
			return nil
		}
		if mod.Kind != ObjectKind {
			return nil
		}
		seen[visit{mod, required}] = true
		if mod.Named == "" {
			return walkFields(mod, mod.AsRecord().Fields, via, required)
		}
		if mod.localFrame != nil && !envEncloses(env, mod.localFrame) {
			const why = "is declared in a function, block or type body that does not enclose this code, so there is no constructor for it here"
			if via != "" {
				return fmt.Errorf("cannot decode %s here: its type %s %s", via, mod.Name(), why)
			}
			return fmt.Errorf("cannot decode into %s here: it %s", mod.Name(), why)
		}
		if mod.constructorType != nil {
			if args, ok := mod.constructorType.Arg().(*RecordType); ok {
				return walkFields(mod, args.Fields, via, required)
			}
		}
		return nil
	}
	return walk(want, "", true)
}

// hasDecodableShape reports whether decoded data can materialize as mod:
// mod has a constructor to build its values from the data (an object type
// declared in Dang, or a schema input object), or a shape of its own (an
// anonymous record, an enum, a scalar). A namespace has neither: an import's,
// a builtin one such as Random, or a schema's object type, which
// only its server builds. Interfaces and unions are left to the type
// patterns that match decoded data (see abstractDecodeTarget).
func hasDecodableShape(mod *Type) bool {
	switch mod.Kind {
	case ObjectKind:
		return mod.Named == "" || mod.declaredInSource || mod.constructorType != nil
	case InputKind:
		return mod.constructorType != nil
	}
	return true
}

// ignoredByAllCodecs reports whether every registered field naming ignores
// field of mod (@JSON.ignore, @YAML.ignore and @TOML.ignore), so no decode
// ever reads it. The static check cannot tell where decoded data comes from.
func ignoredByAllCodecs(mod *Type, field string) bool {
	for _, n := range fieldNamings() {
		if _, ignore := fieldKey(n, mod, field); !ignore {
			return false
		}
	}
	return true
}

// envEncloses reports whether frame is env's own frame or one of the frames
// env resolves through.
func envEncloses(env hm.Env, frame TypeScope) bool {
	seen := map[hm.Env]bool{}
	var walk func(e hm.Env) bool
	walk = func(e hm.Env) bool {
		if e == nil || seen[e] {
			return false
		}
		seen[e] = true
		switch s := e.(type) {
		case *OverlayTypeScope:
			return walk(s.primary) || walk(s.lexical)
		case *Type:
			if TypeScope(s) == frame {
				return true
			}
			if s.Parent != nil {
				return walk(s.Parent)
			}
		}
		return false
	}
	return walk(env)
}

// containsDecoded reports whether t carries DecodedType, on its own or within
// a list, map, union, or anonymous record's fields.
func containsDecoded(t hm.Type) bool {
	switch v := t.(type) {
	case DecodedType:
		return true
	case hm.NonNullType:
		return containsDecoded(v.Type)
	case ListType:
		return containsDecoded(v.Type)
	case MapType:
		return containsDecoded(v.Type)
	case *hm.UnionType:
		for _, option := range v.Options {
			if containsDecoded(option) {
				return true
			}
		}
	case *Type:
		// Anonymous records ({{...}}) compare structurally, field by field;
		// named types compare by identity, never reaching their fields' data.
		if v.Kind == ObjectKind && v.Named == "" {
			for _, field := range v.AsRecord().Fields {
				if ft, mono := field.Value.Type(); mono && containsDecoded(ft) {
					return true
				}
			}
		}
	}
	return false
}

// checkEnumLiterals rejects a string literal handed to an enum slot that
// names none of the enum's values. Its
// value is known statically, so there is no need to wait for
// materialization to fail at runtime. It follows
// the literal through lists, maps and a block's tail; anything else (a
// template, a union slot) is left to runtime validation.
func checkEnumLiterals(value Node, want hm.Type) error {
	switch t := want.(type) {
	case hm.NonNullType:
		return checkEnumLiterals(value, t.Type)
	case ListType:
		if l, ok := value.(*List); ok {
			for _, el := range l.Elements {
				if err := checkEnumLiterals(el, t.Type); err != nil {
					return err
				}
			}
		}
	case MapType:
		if m, ok := value.(*MapLiteral); ok {
			for _, entry := range m.Entries {
				if err := checkEnumLiterals(entry.Value, t.Type); err != nil {
					return err
				}
			}
		}
	case *Type:
		if t.Kind != EnumKind {
			return nil
		}
		switch v := value.(type) {
		case *String:
			if !enumHasValue(t, v.Value) {
				return NewInferError(
					fmt.Errorf("%q is not a value of %s; expected one of %s",
						v.Value, t, strings.Join(enumValueNames(t), ", ")),
					v,
				)
			}
		case *Block:
			if len(v.Forms) > 0 {
				return checkEnumLiterals(v.Forms[len(v.Forms)-1], want)
			}
		}
	}
	return nil
}

// enumValueNames lists an enum's values in declaration order.
func enumValueNames(enum *Type) []string {
	var names []string
	for name := range enum.Bindings(PublicVisibility) {
		if enumHasValue(enum, name) {
			names = append(names, name)
		}
	}
	return names
}

// isDegradableScalar reports whether mod is a custom scalar whose values may
// degrade to String at value-handoff boundaries. Every non-primitive scalar
// value is a string underneath (ScalarValue), so they all qualify.
func isDegradableScalar(mod *Type) bool {
	return mod.Kind == ScalarKind && !isPrimitiveScalar(mod)
}

// containsDegradableScalar reports whether t carries a degradable custom
// scalar at any depth reachable by matched-wrapper unification (NonNull,
// lists, maps) — i.e. whether AssignableWithCoercion could apply the
// scalar→String degrade to a value of type t.
func containsDegradableScalar(t hm.Type) bool {
	switch v := t.(type) {
	case hm.NonNullType:
		return containsDegradableScalar(v.Type)
	case ListType:
		return containsDegradableScalar(v.Type)
	case MapType:
		return containsDegradableScalar(v.Type)
	case GraphQLListType:
		return containsDegradableScalar(v.Type)
	case *Type:
		return isDegradableScalar(v)
	default:
		return false
	}
}

// scalarCastable reports whether an explicit cast (`x :: T`) may convert a
// value of type have to want by turning custom scalars into other custom
// scalars: every custom scalar is text underneath (ScalarValue), so its text
// can become any other's, through that one's new() hook like a String's
// (`j :: Dagger.JSON!`, `d :: JSON!`). The two match through non-null, list
// and map wrappers, and the cast may not drop nullability. This is for
// explicit casts only: at a value handoff, distinct scalars never mix.
func scalarCastable(have, want hm.Type) bool {
	switch w := want.(type) {
	case hm.NonNullType:
		h, ok := have.(hm.NonNullType)
		return ok && scalarCastable(h.Type, w.Type)
	case ListType:
		h, ok := unwrapNonNull(have).(ListType)
		return ok && scalarCastable(h.Type, w.Type)
	case MapType:
		h, ok := unwrapNonNull(have).(MapType)
		return ok && scalarCastable(h.Type, w.Type)
	case *Type:
		h, ok := unwrapNonNull(have).(*Type)
		return ok && isDegradableScalar(h) && isDegradableScalar(w)
	}
	return false
}

// convertScalars converts the custom scalar values in val to the scalars
// target names, for an explicit cast between them (scalarCastable). A value
// of another scalar becomes one of target's through target's new() hook, as
// its text would from a String; without a hook it keeps its text, and, for
// a schema's scalar, how it is held (ScalarValue.Encoded). Lists and maps
// convert element by element; materializeValue does the rest.
func convertScalars(ctx context.Context, scope ValueScope, val Value, target hm.Type, path string) (Value, error) {
	switch v := val.(type) {
	case ScalarValue:
		mod, ok := unwrapNonNull(target).(*Type)
		if !ok || !isDegradableScalar(mod) || v.ScalarType == mod {
			return val, nil
		}
		converted, hooked, err := applyScalarHook(ctx, scope, mod, v.Val)
		if err != nil {
			return nil, materializeError(path, "%s", err.Error())
		}
		if !hooked {
			// Only a schema's scalar holds a value the server sent as data
			// (Encoded); a Dang-owned one holds its text.
			converted = ScalarValue{Val: v.Val, ScalarType: mod, Encoded: v.Encoded && mod.SourceSchema != nil}
		}
		return converted, nil
	case ListValue:
		elemTarget, ok := listElementTarget(target)
		if !ok {
			return val, nil
		}
		elements := make([]Value, len(v.Elements))
		for i, elem := range v.Elements {
			converted, err := convertScalars(ctx, scope, elem, elemTarget, joinIndexPath(path, i))
			if err != nil {
				return nil, err
			}
			elements[i] = converted
		}
		return ListValue{Elements: elements, ElemType: v.ElemType}, nil
	case MapValue:
		valTarget, ok := mapValueTarget(target)
		if !ok {
			return val, nil
		}
		entries := make(map[string]Value, len(v.Entries))
		for _, k := range v.Keys {
			converted, err := convertScalars(ctx, scope, v.Entries[k], valTarget, joinKeyPath(path, k))
			if err != nil {
				return nil, err
			}
			entries[k] = converted
		}
		return MapValue{Keys: append([]string{}, v.Keys...), Entries: entries, ValType: v.ValType}, nil
	}
	return val, nil
}

// diagnoseAssignment expands an hm.Assignable failure into a detailed
// message listing the field-level incompatibilities between two
// object/interface types. Returns nil when no enrichment applies and the
// caller should use the bare unification error.
func diagnoseAssignment(have, want hm.Type) error {
	haveBase := have
	if nn, ok := haveBase.(hm.NonNullType); ok {
		haveBase = nn.Type
	}
	if meta, ok := haveBase.(*Type); ok && meta.IsMeta() {
		// Both print as the type's name, so say which is which.
		return fmt.Errorf("cannot use %s as %s: %s here is the type itself, not a value of that type",
			have, want, meta.Named)
	}
	haveMod, wantMod, issues, ok := walkAssignment(have, want)
	if !ok {
		return nil
	}
	if len(issues) == 0 {
		switch {
		case haveMod.Named == "" && wantMod.Named == "":
			// Records unify only with records of the very same field types.
			return fmt.Errorf("cannot use %s as %s: record fields must have exactly the same types", have, want)
		case haveMod.Named == "" && wantMod.Kind == InputKind:
			return fmt.Errorf("cannot use %s as %s: a record's fields and their types must match the input's exactly, nullability included", have, want)
		case haveMod.Named == "":
			// Named types are nominal; a record never stands in for one.
			return fmt.Errorf("cannot use %s as %s: a record can't stand in for %s, even with the same fields",
				have, want, wantMod.Named)
		}
		haveHome, wantHome := haveMod.HomeModule(), wantMod.HomeModule()
		if haveMod.Named == wantMod.Named && haveHome != nil && wantHome != nil && haveHome != wantHome {
			// Same type name, distinct module identities. This is a nominal
			// identity mismatch, not a missing `implements`: it happens when two
			// modules each hold their own copy of a type (per-module import
			// identity), and those copies never unify across the module boundary.
			// Both sides stringify to the same "Widget!", so the usual
			// "cannot use X as Y" head reads as a tautology — phrase it to make
			// the two-different-types point explicit instead, and steer the author
			// toward a shared interface rather than "declare an implementation",
			// which cannot bridge the boundary.
			return fmt.Errorf("the %q value here is a different type from the %q expected: they come from different modules and do not unify across the module boundary; share behavior through an interface instead",
				haveMod.Named, wantMod.Named)
		}
		return fmt.Errorf("cannot use %s as %s: %s is structurally compatible with %s but %s is not declared as an implementation",
			have, want, haveMod.Named, wantMod.Named, haveMod.Named)
	}
	return fmt.Errorf("cannot use %s as %s:\n  - %s", have, want, strings.Join(issues, "\n  - "))
}

// walkAssignment unwraps matched NonNull/List wrappers and, when it
// reaches a Type-vs-Type (or Type-vs-Union) pair, returns the
// field-level incompatibilities that prevent assignment. ok is true when
// the walk reached such a Type pair (regardless of whether issues were
// found); have and want are the unwrapped Types in that case.
func walkAssignment(have, want hm.Type) (haveMod, wantMod *Type, issues []string, ok bool) {
	if haveNN, haveOk := have.(hm.NonNullType); haveOk {
		if wantNN, wantOk := want.(hm.NonNullType); wantOk {
			return walkAssignment(haveNN.Type, wantNN.Type)
		}
	}
	if haveLT, haveOk := have.(ListType); haveOk {
		if wantLT, wantOk := want.(ListType); wantOk {
			return walkAssignment(haveLT.Type, wantLT.Type)
		}
	}
	if haveMT, haveOk := have.(MapType); haveOk {
		if wantMT, wantOk := want.(MapType); wantOk {
			return walkAssignment(haveMT.Type, wantMT.Type)
		}
	}
	if haveGLT, haveOk := have.(GraphQLListType); haveOk {
		if wantGLT, wantOk := want.(GraphQLListType); wantOk {
			return walkAssignment(haveGLT.Type, wantGLT.Type)
		}
	}
	hMod, hOk := have.(*Type)
	if !hOk {
		return nil, nil, nil, false
	}
	if wMod, wOk := want.(*Type); wOk {
		if !isStructuralKind(hMod.Kind) || !isStructuralKind(wMod.Kind) {
			return nil, nil, nil, false
		}
		return hMod, wMod, diagnoseModule(hMod, wMod), true
	}
	if wantUnion, wOk := want.(*hm.UnionType); wOk {
		return bestUnionMatch(hMod, wantUnion)
	}
	return nil, nil, nil, false
}

// bestUnionMatch picks the union option that yields the most informative
// Type diagnosis: the structurally closest option (fewest field-level
// issues). If multiple options reach a Type pair, the one with the
// shortest issue list wins. ID-handle unions (Object | ID) usually pick
// the Object half here.
func bestUnionMatch(have *Type, want *hm.UnionType) (*Type, *Type, []string, bool) {
	var (
		bestHave   *Type
		bestWant   *Type
		bestIssues []string
		bestN      = -1
		found      bool
	)
	for _, option := range want.Options {
		hMod, wMod, issues, ok := walkAssignment(have, option)
		if !ok {
			continue
		}
		found = true
		if bestN == -1 || len(issues) < bestN {
			bestN = len(issues)
			bestHave = hMod
			bestWant = wMod
			bestIssues = issues
		}
	}
	return bestHave, bestWant, bestIssues, found
}

// diagnoseModule lists field-level incompatibilities that prevent
// assigning a value of have to want. It walks every public field of want
// and reports each missing or shape-incompatible field with neutral
// terminology (the comparison runs in both object-implements-interface
// and interface-vs-interface contexts).
func diagnoseModule(have, want *Type) []string {
	var issues []string
	wantFields := want.Bindings(PublicVisibility)
	if want.Named == "" && have.Named == "" {
		// A record's fields are all its data; compare every one, whatever
		// visibility the record type was built with.
		wantFields = func(yield func(string, *hm.Scheme) bool) {
			for _, field := range want.AsRecord().Fields {
				if !yield(field.Key, field.Value) {
					return
				}
			}
		}
	}
	for name, wantScheme := range wantFields {
		wantType, _ := wantScheme.Type()
		haveScheme, found := have.LocalSchemeOf(name)
		if !found {
			issues = append(issues, fmt.Sprintf("missing field %q", name))
			continue
		}
		haveType, _ := haveScheme.Type()
		if err := diagnoseField(name, haveType, wantType); err != nil {
			issues = append(issues, err.Error())
		}
	}
	return issues
}

// diagnoseField compares a have-field against a want-field with the
// variance rules expected for value handoffs: covariant returns and
// contravariant arguments on function-typed fields, subtyping on plain
// fields. Zero-argument function shape (used to represent argless
// interface fields) is unwrapped on both sides so a field field and a
// zero-arg field of the same type are treated as compatible.
func diagnoseField(name string, have, want hm.Type) error {
	have = unwrapZeroArgFn(have)
	want = unwrapZeroArgFn(want)
	haveFn, haveIsFn := have.(*hm.FunctionType)
	wantFn, wantIsFn := want.(*hm.FunctionType)
	if !haveIsFn && !wantIsFn {
		if _, err := hm.Assignable(have, want); err != nil {
			return fmt.Errorf("field %q: type %s is not compatible with %s", name, have, want)
		}
		return nil
	}
	if haveIsFn != wantIsFn {
		return fmt.Errorf("field %q: shape mismatch (%s vs %s)", name, have, want)
	}
	if _, err := hm.Assignable(haveFn.Ret(false), wantFn.Ret(false)); err != nil {
		return fmt.Errorf("field %q: return type %s is not compatible with %s (covariance required)",
			name, haveFn.Ret(false), wantFn.Ret(false))
	}
	wantArgs, wantArgsOk := wantFn.Arg().(*RecordType)
	haveArgs, haveArgsOk := haveFn.Arg().(*RecordType)
	if !wantArgsOk || !haveArgsOk {
		return fmt.Errorf("field %q: arguments must be record types", name)
	}
	for _, wantArg := range wantArgs.Fields {
		haveArgScheme, found := haveArgs.SchemeOf(wantArg.Key)
		if !found {
			return fmt.Errorf("field %q: missing argument %q", name, wantArg.Key)
		}
		haveArgType, _ := haveArgScheme.Type()
		wantArgType, _ := wantArg.Value.Type()
		if _, err := hm.Assignable(wantArgType, haveArgType); err != nil {
			return fmt.Errorf("field %q, argument %q: type %s does not accept %s (contravariance required)",
				name, wantArg.Key, haveArgType, wantArgType)
		}
	}
	for _, haveArg := range haveArgs.Fields {
		if _, found := wantArgs.SchemeOf(haveArg.Key); found {
			continue
		}
		haveArgType, _ := haveArg.Value.Type()
		if _, isNonNull := haveArgType.(hm.NonNullType); isNonNull {
			return fmt.Errorf("field %q: extra required argument %q (must be optional)", name, haveArg.Key)
		}
	}
	return nil
}

// unwrapZeroArgFn returns t with a zero-argument function shape stripped
// off. Interfaces store argless fields as () -> T while objects store
// them as plain T; the unwrap lets the two representations compare equal.
func unwrapZeroArgFn(t hm.Type) hm.Type {
	fn, ok := t.(*hm.FunctionType)
	if !ok {
		return t
	}
	rt, ok := fn.Arg().(*RecordType)
	if !ok || len(rt.Fields) != 0 {
		return t
	}
	return fn.Ret(false)
}

func isStructuralKind(k Kind) bool {
	return k == ObjectKind || k == InterfaceKind || k == InputKind
}

// wrapCoerce inserts a Coerce wrapper around node so its value is materialized
// against target at runtime. If node is already a Coerce, the target is
// tightened to the new target, unless it converts scalars for a cast: that
// conversion is to the cast's own type, so it is wrapped instead.
func wrapCoerce(node Node, target hm.Type, path string) Node {
	if node == nil || target == nil {
		return node
	}
	if c, ok := node.(*Coerce); ok && !c.ConvertScalars {
		c.Target = target
		if path != "" {
			c.Path = path
		}
		return c
	}
	return &Coerce{Expr: node, Target: target, Path: path}
}

// materializeValue resolves deferred decoded values at expected-type boundaries.
//
// This is intentionally not a general runtime type assertion. Normal Dang
// values are trusted to match their inferred type and are returned unchanged,
// except for narrow explicit coercions (string-to-enum/custom-scalar and list
// elements that may themselves need materialization).
func materializeValue(ctx context.Context, scope ValueScope, val Value, target hm.Type, path string) (Value, error) {
	if target == nil {
		return val, nil
	}

	switch v := val.(type) {
	case NullValue:
		if _, nonNull := target.(hm.NonNullType); nonNull {
			return nil, materializeError(path, "null is not allowed for %s", target.Name())
		}
		return val, nil
	case DeferredValue:
		return materializeDeferred(ctx, scope, v, target, path)
	case StringValue:
		return materializeStringValue(ctx, scope, v, target, path)
	case ScalarValue:
		// Degrade a custom scalar flowing into a String slot; any other
		// target passes through (the value is trusted to match its type).
		if unwrapNonNull(target) == StringType {
			return StringValue{Val: v.Val}, nil
		}
		return val, nil
	case ListValue:
		elemTarget, ok := listElementTarget(target)
		if !ok {
			return val, nil
		}
		elements := make([]Value, len(v.Elements))
		for i, elem := range v.Elements {
			materialized, err := materializeValue(ctx, scope, elem, elemTarget, joinIndexPath(path, i))
			if err != nil {
				return nil, err
			}
			elements[i] = materialized
		}
		// The list now has the target's type and records it (see
		// recordedElemType): what this call has a generic target (`[a!]!`)
		// stand for, when it knows (a return type in a generic body). Where
		// it doesn't (a generic parameter), the list's own element type says
		// more, so a [Float!]! passed through a type variable still holds
		// Floats.
		elemType := runtimeType(ctx, elemTarget)
		if holdsTypeVars(elemType) {
			elemType = recordedOr(v.ElemType)
		}
		return ListValue{Elements: elements, ElemType: elemType}, nil
	case MapValue:
		valTarget, ok := mapValueTarget(target)
		if !ok {
			return val, nil
		}
		entries := make(map[string]Value, len(v.Entries))
		for _, k := range v.Keys {
			materialized, err := materializeValue(ctx, scope, v.Entries[k], valTarget, joinKeyPath(path, k))
			if err != nil {
				return nil, err
			}
			entries[k] = materialized
		}
		valType := runtimeType(ctx, valTarget)
		if holdsTypeVars(valType) {
			valType = recordedOr(v.ValType)
		}
		return MapValue{Keys: append([]string{}, v.Keys...), Entries: entries, ValType: valType}, nil
	default:
		return val, nil
	}
}

func materializeStringValue(ctx context.Context, scope ValueScope, val StringValue, target hm.Type, path string) (Value, error) {
	inner := unwrapNonNull(target)
	mod, ok := inner.(*Type)
	if !ok {
		return val, nil
	}

	switch mod.Kind {
	case EnumKind:
		if !enumHasValue(mod, val.Val) {
			return nil, materializeError(path, "invalid enum value %q for %s", val.Val, mod.Name())
		}
		return EnumValue{Val: val.Val, EnumType: mod}, nil
	case ScalarKind:
		if isPrimitiveScalar(mod) {
			return val, nil
		}
		// A scalar with a new() hook — Dang-defined or Go-native (Regexp's
		// compile check) — computes its canonical string at materialization.
		if v, hooked, err := applyScalarHook(ctx, scope, mod, val.Val); hooked {
			if err != nil {
				return nil, materializeError(path, "%s", err.Error())
			}
			return v, nil
		}
		return ScalarValue{Val: val.Val, ScalarType: mod}, nil
	default:
		return val, nil
	}
}

// materializeDecoded converts decoded data (raw) to a value of target. codec
// maps field names to input keys. strict is nil for a plain decode; for a
// strict one it collects the input keys no field of a target object reads
// (see strictDecode), which materializeDeferred reports once the whole value
// is built.
func materializeDecoded(ctx context.Context, scope ValueScope, raw any, target hm.Type, path string, naming FieldNaming, strict *strictDecode) (Value, error) {
	if nn, ok := target.(hm.NonNullType); ok {
		if raw == nil {
			if _, ok := nn.Type.(DecodedType); ok {
				// Decoded! takes data of any shape, null included; a
				// later cast decides what null means there.
				return DeferredValue{Raw: nil, Naming: naming, Strict: strict != nil}, nil
			}
			return nil, materializeDeferredError(path, "null is not allowed for %s", target.Name())
		}
		return materializeDecoded(ctx, scope, raw, nn.Type, path, naming, strict)
	}

	if raw == nil {
		return NullValue{}, nil
	}

	switch target.(type) {
	case hm.TypeVariable, hm.NullableTypeVariable, DecodedType:
		return DeferredValue{Raw: raw, Naming: naming, Strict: strict != nil}, nil
	}

	if union, ok := target.(*hm.UnionType); ok {
		return materializeDecodedUnion(ctx, scope, raw, union, path, naming, strict)
	}

	if target == StringType {
		s, ok := raw.(string)
		if !ok {
			return nil, materializeDeferredError(path, "expected string, got %s", decodedKind(raw))
		}
		return StringValue{Val: s}, nil
	}

	if target == IntType {
		num, ok := raw.(DataNumber)
		if !ok {
			return nil, materializeDeferredError(path, "expected int, got %s", decodedKind(raw))
		}
		val, err := decodedNumberToInt(num)
		if err != nil {
			return nil, materializeDeferredError(path, "expected integral number for Int, got %q", num.String())
		}
		return IntValue{Val: val}, nil
	}

	if target == FloatType {
		num, ok := raw.(DataNumber)
		if !ok {
			return nil, materializeDeferredError(path, "expected number, got %s", decodedKind(raw))
		}
		val, err := num.Float64()
		if err != nil || math.IsNaN(val) || math.IsInf(val, 0) {
			// A Float is finite, as Dang arithmetic keeps it: nan and inf
			// (TOML's nan, YAML's .inf) have no Float.
			return nil, materializeDeferredError(path, "invalid number %q", num.String())
		}
		return FloatValue{Val: val}, nil
	}

	if target == BooleanType {
		b, ok := raw.(bool)
		if !ok {
			return nil, materializeDeferredError(path, "expected boolean, got %s", decodedKind(raw))
		}
		return BoolValue{Val: b}, nil
	}

	if elemTarget, ok := listElementTarget(target); ok {
		arr, ok := raw.([]any)
		if !ok {
			return nil, materializeDeferredError(path, "expected array, got %s", decodedKind(raw))
		}
		elements := make([]Value, len(arr))
		for i, elem := range arr {
			materialized, err := materializeDecoded(ctx, scope, elem, elemTarget, joinIndexPath(path, i), naming, strict)
			if err != nil {
				return nil, err
			}
			elements[i] = materialized
		}
		return ListValue{Elements: elements, ElemType: elemTarget}, nil
	}

	if valTarget, ok := mapValueTarget(target); ok {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, materializeDeferredError(path, "expected object, got %s", decodedKind(raw))
		}
		// Decoded objects arrive as Go maps, so document order is already
		// gone (and TOML's decoder never kept it); sort the keys so the
		// map's insertion order is deterministic.
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		entries := make(map[string]Value, len(obj))
		for _, k := range keys {
			materialized, err := materializeDecoded(ctx, scope, obj[k], valTarget, joinKeyPath(path, k), naming, strict)
			if err != nil {
				return nil, err
			}
			entries[k] = materialized
		}
		return MapValue{Keys: keys, Entries: entries, ValType: valTarget}, nil
	}

	if mod, ok := target.(*Type); ok {
		switch mod.Kind {
		case EnumKind:
			s, ok := raw.(string)
			if !ok {
				return nil, materializeDeferredError(path, "expected string for enum %s, got %s", mod.Name(), decodedKind(raw))
			}
			if !enumHasValue(mod, s) {
				return nil, materializeDeferredError(path, "invalid enum value %q for %s", s, mod.Name())
			}
			return EnumValue{Val: s, EnumType: mod}, nil
		case ScalarKind:
			s, ok := raw.(string)
			if !ok && mod.SourceSchema != nil && !mod.hasScalarHook && mod.goScalarHook == nil {
				// A GraphQL scalar's value may be any JSON (see
				// graphQLResultToValue), so its encoded form decodes back.
				encoded, err := json.Marshal(raw)
				if err != nil {
					return nil, materializeDeferredError(path, "encoding %s value: %s", mod.Name(), err)
				}
				return ScalarValue{Val: string(encoded), ScalarType: mod, Encoded: true}, nil
			}
			if !ok {
				return nil, materializeDeferredError(path, "expected string for scalar %s, got %s", mod.Name(), decodedKind(raw))
			}
			if mod == StringType {
				return StringValue{Val: s}, nil
			}
			if v, hooked, err := applyScalarHook(ctx, scope, mod, s); hooked {
				if err != nil {
					return nil, materializeDeferredError(path, "%s", err.Error())
				}
				return v, nil
			}
			return ScalarValue{Val: s, ScalarType: mod}, nil
		case ObjectKind:
			if mod.Named == "" {
				return materializeAnonymousObject(ctx, scope, raw, mod, path, naming, strict)
			}
			return materializeNamedObject(ctx, scope, raw, mod, path, naming, strict)
		case InputKind:
			return materializeNamedObject(ctx, scope, raw, mod, path, naming, strict)
		}
	}

	return nil, materializeDeferredError(path, "cannot materialize decoded data as %s", target.Name())
}

// materializeDecodedUnion materializes raw as the first option it fits. Under
// strict decoding an option fits cleanly only if it leaves no unknown keys;
// if none does, the first option that fits at all is taken, with its unknown
// keys reported, since that is the likeliest reading of the input.
func materializeDecodedUnion(ctx context.Context, scope ValueScope, raw any, union *hm.UnionType, path string, naming FieldNaming, strict *strictDecode) (Value, error) {
	var lastErr error
	var fallback Value
	var fallbackStrict *strictDecode
	for _, option := range union.Options {
		attempt := strict.fork()
		val, err := materializeDecoded(ctx, scope, raw, option, path, naming, attempt)
		if err != nil {
			lastErr = err
			continue
		}
		if attempt == nil || len(attempt.unknown) == 0 {
			return val, nil
		}
		if fallback == nil {
			fallback, fallbackStrict = val, attempt
		}
	}
	if fallback != nil {
		strict.unknown = append(strict.unknown, fallbackStrict.unknown...)
		return fallback, nil
	}
	if lastErr != nil {
		return nil, materializeDeferredError(path, "cannot materialize decoded data as %s", union.Name())
	}
	return DeferredValue{Raw: raw, Naming: naming, Strict: strict != nil}, nil
}

func materializeNamedObject(ctx context.Context, scope ValueScope, raw any, mod *Type, path string, naming FieldNaming, strict *strictDecode) (Value, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, materializeDeferredError(path, "expected object for %s, got %s", mod.Name(), decodedKind(raw))
	}

	callable, err := constructorFor(ctx, scope, mod)
	if err != nil {
		return nil, materializeDeferredError(path, "%s", err.Error())
	}

	args, err := materializeConstructorArgs(ctx, scope, obj, mod, callable, path, naming, strict)
	if err != nil {
		return nil, err
	}

	if strict.failed() {
		// The decode will raise its unknown keys, so this value is never
		// used: don't run the constructor (a custom new() may have effects,
		// or raise and hide the unknown keys). The arguments were still
		// walked, collecting the keys nested in them.
		return NullValue{}, nil
	}

	return callable.Call(ctx, scope, args)
}

// constructorFor finds the constructor that builds values of mod, by type
// identity rather than name, so a local binding shadowing the type's name
// cannot hijack construction, and a type reached only qualified (`Lib.T`) is
// still found. Where it lives follows where mod was declared:
//
//   - A schema input object's constructor is plain data, built from the type.
//   - A top-level type of an imported Dang module was bound by that module's
//     own evaluation, which runs once per module instance, so it is found
//     there, wherever (and whether) the importer's import has run. A decode
//     that is part of that evaluation (the module's own top-level code) finds
//     it through its scope chain instead, which reaches the module's root:
//     asking for the module's value scope there would wait on itself.
//   - Any other type was bound by its declaration in a scope enclosing the
//     decode (checkDecodeTarget guarantees it): the module's top level, or the
//     function, block or type body declaring it. Each evaluation, call and
//     instance binds its own, so evaluations of a prepared program never see
//     each other's, and a type declared in a function body uses that call's.
func constructorFor(ctx context.Context, scope ValueScope, mod *Type) (Callable, error) {
	mod = canonicalModule(mod)
	if mod.Kind == InputKind && mod.constructorType != nil {
		return InputObjectConstructor{TypeName: mod.Named, TypeScope: mod, FnType: mod.constructorType}, nil
	}
	if home := mod.homeModule; mod.localFrame == nil && home != nil && home.dang != nil && home.dang != evaluatingModule(ctx) {
		values, err := home.dang.valueScope(ctx)
		if err != nil {
			return nil, err
		}
		scope = values
	}
	if callable, ok := scope.LookupConstructor(mod); ok {
		return callable, nil
	}
	return nil, fmt.Errorf("constructor for %s not found", mod.Name())
}

func materializeConstructorArgs(ctx context.Context, scope ValueScope, obj map[string]any, mod *Type, callable Callable, path string, naming FieldNaming, strict *strictDecode) (map[string]Value, error) {
	args := map[string]Value{}

	if constructor, ok := callable.(*ConstructorFunction); ok {
		if strict != nil {
			names := make([]string, len(constructor.Parameters))
			for i, param := range constructor.Parameters {
				names[i] = param.Name.Name
			}
			strict.noteUnknownKeys(obj, mod, names, path, naming)
		}
		paramTypes := parameterTypes(callable)
		for _, param := range constructor.Parameters {
			name := param.Name.Name
			paramType := paramTypes[name]
			if paramType == nil {
				paramType = param.GetInferredType()
			}
			fieldPath := joinFieldPath(path, name)
			key, ignore := fieldKey(naming, mod, name)
			if !ignore {
				// An explicit null for a defaulted non-null parameter
				// counts as missing, so the default applies rather than
				// leaving null in a non-null field.
				if rawVal, found := obj[key]; found && !(rawVal == nil && nullTakesDefault(param)) {
					materialized, err := materializeDecoded(ctx, scope, rawVal, paramType, fieldPath, naming, strict)
					if err != nil {
						return nil, err
					}
					args[name] = materialized
					continue
				}
			}

			if param.Value != nil {
				// Omit defaulted parameters so constructor/default handling runs.
				continue
			}
			if isNullableType(paramType) {
				args[name] = NullValue{}
				continue
			}
			return nil, materializeDeferredError(fieldPath, "missing required field")
		}
		return args, nil
	}

	paramTypes := parameterTypes(callable)
	if strict != nil {
		strict.noteUnknownKeys(obj, mod, callable.ParameterNames(), path, naming)
	}
	for _, name := range callable.ParameterNames() {
		paramType := paramTypes[name]
		fieldPath := joinFieldPath(path, name)
		key, ignore := fieldKey(naming, mod, name)
		if !ignore {
			if rawVal, found := obj[key]; found {
				materialized, err := materializeDecoded(ctx, scope, rawVal, paramType, fieldPath, naming, strict)
				if err != nil {
					return nil, err
				}
				args[name] = materialized
				continue
			}
		}
		if isNullableType(paramType) {
			args[name] = NullValue{}
			continue
		}
		return nil, materializeDeferredError(fieldPath, "missing required field")
	}
	return args, nil
}

func materializeAnonymousObject(ctx context.Context, scope ValueScope, raw any, mod *Type, path string, naming FieldNaming, strict *strictDecode) (Value, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, materializeDeferredError(path, "expected object, got %s", decodedKind(raw))
	}

	fields := mod.AsRecord().Fields
	if strict != nil {
		names := make([]string, len(fields))
		for i, field := range fields {
			names[i] = field.Key
		}
		strict.noteUnknownKeys(obj, mod, names, path, naming)
	}

	value := NewObject(mod)
	for _, field := range fields {
		name := field.Key
		fieldType, mono := field.Value.Type()
		if !mono {
			return nil, materializeDeferredError(joinFieldPath(path, name), "field type is not monomorphic")
		}
		fieldPath := joinFieldPath(path, name)
		key, ignore := fieldKey(naming, mod, name)
		if !ignore {
			if rawVal, found := obj[key]; found {
				materialized, err := materializeDecoded(ctx, scope, rawVal, fieldType, fieldPath, naming, strict)
				if err != nil {
					return nil, err
				}
				value.Bind(name, materialized, PublicVisibility)
				continue
			}
		}
		if isNullableType(fieldType) {
			value.Bind(name, NullValue{}, PublicVisibility)
			continue
		}
		return nil, materializeDeferredError(fieldPath, "missing required field")
	}
	return value, nil
}

// materializeDeferred materializes a decode result at target. A strict decode
// collects every unknown key across the whole value (see strictDecode) and
// reports them together, one `<path>: field <key> not found` line each, as
// yaml.v3's KnownFields does, rather than stopping at the first. Any other
// error still stops materialization at once and is returned as it is.
func materializeDeferred(ctx context.Context, scope ValueScope, v DeferredValue, target hm.Type, path string) (Value, error) {
	if !v.Strict {
		return materializeDecoded(ctx, scope, v.Raw, target, path, v.Naming, nil)
	}
	strict := &strictDecode{}
	val, err := materializeDecoded(ctx, scope, v.Raw, target, path, v.Naming, strict)
	if err != nil {
		return nil, err
	}
	if err := strict.err(); err != nil {
		return nil, err
	}
	return val, nil
}

// strictDecode collects the unknown keys a strict decode meets while one
// value materializes. A nil *strictDecode is a plain decode.
type strictDecode struct {
	// unknown holds one `<path>: field <key> not found` line per key.
	unknown []string
	// parent is the collector a fork was made from.
	parent *strictDecode
}

// fork returns a collector for a tentative materialization (a union option),
// whose keys the caller merges back only if it keeps the result.
func (s *strictDecode) fork() *strictDecode {
	if s == nil {
		return nil
	}
	return &strictDecode{parent: s}
}

// failed reports whether an unknown key has been met, here or in the
// materialization a fork belongs to, so the decode is bound to fail.
func (s *strictDecode) failed() bool {
	for ; s != nil; s = s.parent {
		if len(s.unknown) > 0 {
			return true
		}
	}
	return false
}

// err reports every collected key, one per line, in the order materialization
// met them: an object's own keys (sorted, since decoded objects no longer
// remember document order) before those of its fields, fields in declaration
// order, list elements by index and map entries by key.
func (s *strictDecode) err() error {
	if len(s.unknown) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(s.unknown, "\n"))
}

// noteUnknownKeys is strict decoding's check on an object being materialized
// as mod from params (its constructor's parameters, or a record's fields): each
// key of obj must be the codec key (@FORMAT.field renames it; @FORMAT.ignore
// means none) of one of params or of one of mod's data fields. The data fields
// count even when no parameter sets them (a `let` field, or a field a custom
// new() computes), because encode writes them, so a value's own encoding
// decodes back strictly. The wording follows yaml.v3's KnownFields.
func (s *strictDecode) noteUnknownKeys(obj map[string]any, mod *Type, params []string, path string, naming FieldNaming) {
	known := make(map[string]bool, len(params))
	accept := func(name string) {
		if key, ignore := fieldKey(naming, mod, name); !ignore {
			known[key] = true
		}
	}
	for _, name := range params {
		accept(name)
	}
	for name, scheme := range mod.Bindings(PrivateVisibility) {
		if isCodecDataField(scheme) {
			accept(name)
		}
	}
	var unknown []string
	for k := range obj {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		s.unknown = append(s.unknown, materializeDeferredError(path, "field %s not found", k).Error())
	}
}

// nullTakesDefault reports whether decoded null for param means "use the
// default": param has one and is non-null as declared. (Its type in the
// constructor's signature is nullable, since the argument may be omitted.)
// A Decoded! field is the exception: it holds null data as it is.
func nullTakesDefault(param *FieldDecl) bool {
	if param.Value == nil {
		return false
	}
	nn, nonNull := param.GetInferredType().(hm.NonNullType)
	if !nonNull {
		return false
	}
	_, decoded := nn.Type.(DecodedType)
	return !decoded
}

func parameterTypes(val Value) map[string]hm.Type {
	result := map[string]hm.Type{}
	ft, ok := val.Type().(*hm.FunctionType)
	if !ok || ft == nil {
		return result
	}
	rec, ok := ft.Arg().(*RecordType)
	if !ok {
		return result
	}
	for _, field := range rec.Fields {
		if typ, mono := field.Value.Type(); mono {
			result[field.Key] = typ
		}
	}
	return result
}

func listElementTarget(target hm.Type) (hm.Type, bool) {
	inner := unwrapNonNull(target)
	switch list := inner.(type) {
	case ListType:
		return list.Type, true
	case GraphQLListType:
		return list.Type, true
	default:
		return nil, false
	}
}

func mapValueTarget(target hm.Type) (hm.Type, bool) {
	if m, ok := unwrapNonNull(target).(MapType); ok {
		return m.Type, true
	}
	return nil, false
}

func unwrapNonNull(target hm.Type) hm.Type {
	if nn, ok := target.(hm.NonNullType); ok {
		return nn.Type
	}
	return target
}

func isNullableType(target hm.Type) bool {
	if target == nil {
		return true
	}
	_, nonNull := target.(hm.NonNullType)
	return !nonNull
}

func enumHasValue(enum *Type, value string) bool {
	scheme, found := enum.SchemeOf(value)
	if !found {
		return false
	}
	typ, mono := scheme.Type()
	if !mono {
		return false
	}
	_, err := hm.Assignable(typ, hm.NonNullType{Type: enum})
	return err == nil
}

func isPrimitiveScalar(mod *Type) bool {
	switch mod {
	case StringType, IntType, FloatType, BooleanType:
		return true
	default:
		return false
	}
}

func decodedNumberToInt(num DataNumber) (int, error) {
	rat, ok := new(big.Rat).SetString(num.String())
	if !ok || !rat.IsInt() {
		return 0, fmt.Errorf("not an integer")
	}
	bigInt := rat.Num()
	if !bigInt.IsInt64() {
		return 0, fmt.Errorf("integer out of range")
	}
	i64 := bigInt.Int64()
	if int64(int(i64)) != i64 {
		return 0, fmt.Errorf("integer out of range")
	}
	return int(i64), nil
}

func joinFieldPath(path, field string) string {
	if path == "" || path == "$" {
		return field
	}
	return path + "." + field
}

func joinIndexPath(path string, index int) string {
	if path == "" || path == "$" {
		return fmt.Sprintf("[%d]", index)
	}
	return fmt.Sprintf("%s[%d]", path, index)
}

func joinKeyPath(path, key string) string {
	if path == "$" {
		path = ""
	}
	return fmt.Sprintf("%s[%s]", path, strconv.Quote(key))
}

func materializeError(path, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if path == "" || path == "$" {
		return fmt.Errorf("%s", msg)
	}
	return fmt.Errorf("%s: %s", path, msg)
}

func materializeDeferredError(path, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if path == "" || path == "$" {
		return fmt.Errorf("%s", msg)
	}
	return fmt.Errorf("%s: %s", path, msg)
}

func decodedKind(raw any) string {
	switch raw.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case DataNumber:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", raw)
	}
}

func materializePathForNode(node Node) string {
	switch n := node.(type) {
	case *Symbol:
		return n.Name
	case *Select:
		prefix := materializePathForNode(n.Receiver)
		if prefix == "" {
			return n.Field.Name
		}
		return joinFieldPath(prefix, n.Field.Name)
	default:
		return ""
	}
}
