package dang

import (
	"context"

	"github.com/vito/dang/v2/pkg/hm"
)

// Runtime type arguments: what a generic function's type variables stand for
// in one call. A generic body is checked once, with its signature's variables
// rigid, so values it builds from nothing (an empty `[]`, a `.map` result)
// would carry those variables as their element types. A call's arguments say
// what the variables are this time: `[a: Numeric](xs: [a!]!)` called with a
// [Float!]! has a stand for Float, so an empty list the body builds is a
// [Float!]!, whose sum is 0.0. The body runs with them in its context, and
// runtimeType resolves a type through them.

// typeArgs returns the type arguments of the generic calls ctx is in: each
// rigid variable (as the body sees it) bound to the type it stands for. A
// call's frame carries them (see FunctionValue.Call).
func typeArgs(ctx context.Context) hm.Subs {
	return currentFrame(ctx).typeArgs
}

// runtimeType returns t as a value built where ctx is has it: its bound weak
// variables resolved, and the type variables of the generic calls ctx is in
// replaced by what they stand for in them.
func runtimeType(ctx context.Context, t hm.Type) hm.Type {
	return runtimeTypeIn(typeArgs(ctx), t)
}

// runtimeTypeIn is runtimeType under the type arguments args.
func runtimeTypeIn(args hm.Subs, t hm.Type) hm.Type {
	if t == nil {
		return nil
	}
	t = hm.ResolveWeak(t)
	if len(args) == 0 || len(t.FreeTypeVar()) == 0 {
		return t
	}
	return t.Apply(args).(hm.Type)
}

// resultType is the runtime type of what f returns, which its signature says
// in terms of the generic call it was created in, if any: a block's result
// in a generic body (`xs.map { x => x * 2 }`) is the call's a!, not `a!`.
func (f FunctionValue) resultType() hm.Type {
	if f.FnType == nil {
		return nil
	}
	return runtimeTypeIn(f.TypeArgs, f.FnType.ReturnType())
}

// callTypeArgs returns the type arguments f's body runs with when called
// with args (and the block ctx hands the call, if any): those of the scope f
// was created in (an enclosing generic call's, for a block or nested
// function), plus, for a generic function, what its own signature variables
// stand for, read off the arguments' runtime types and the block's result
// type. A variable only the call's expected result fixes stays unknown at
// runtime. A block is never generic itself: its parameters are typed by the
// enclosing call's variables or concrete types.
func (f FunctionValue) callTypeArgs(ctx context.Context, args map[string]Value) hm.Subs {
	var own hm.Subs
	if f.Generic {
		own = hm.NewSubs()
		if rec, ok := f.FnType.Arg().(*RecordType); ok {
			for _, name := range f.Args {
				val, ok := args[name]
				if !ok {
					continue
				}
				scheme, found := rec.SchemeOf(name)
				if !found {
					continue
				}
				declared, _ := scheme.Type()
				if len(declared.FreeTypeVar()) == 0 {
					continue
				}
				bindValueType(own, declared, val)
			}
		}
		if bt := f.FnType.Block(); bt != nil {
			if block, ok := currentBlockArg(ctx).(FunctionValue); ok {
				if rt := block.resultType(); rt != nil {
					bindTypeType(own, bt.ReturnType(), rt)
				}
			}
		}
	}
	if len(own) == 0 {
		return f.TypeArgs
	}
	merged := make(hm.Subs, len(f.TypeArgs)+len(own))
	for tv, t := range f.TypeArgs {
		merged[tv] = t
	}
	// Its own variables shadow any of the same name it closes over.
	for tv, t := range own {
		merged[tv] = t
	}
	return merged
}

// captureTypeArgs returns the type arguments of the generic calls ctx is in
// that a function or block declared there sees: those of the enclosing
// generic functions whose type variables were in scope where it is written
// (scope, recorded at inference), and nothing of a call that merely happens
// to be running when it is created (an object's method made during one).
func captureTypeArgs(ctx context.Context, scope hm.TypeVarSet) hm.Subs {
	args := typeArgs(ctx)
	if len(args) == 0 || len(scope) == 0 {
		return nil
	}
	var captured hm.Subs
	for tv, t := range args {
		if !scope.Contains(tv) {
			continue
		}
		if captured == nil {
			captured = hm.NewSubs()
		}
		captured[tv] = t
	}
	return captured
}

// isGenericSignature reports whether a function type's parameters mention
// type variables (not weak ones, which are a binding's single type): only a
// generic function's calls have type arguments of their own.
func isGenericSignature(ft *hm.FunctionType) bool {
	if ft == nil {
		return false
	}
	types := []hm.Type{ft.Arg()}
	if bt := ft.Block(); bt != nil {
		types = append(types, bt)
	}
	for _, t := range types {
		if t == nil {
			continue
		}
		for tv := range t.FreeTypeVar() {
			if !tv.IsWeak() {
				return true
			}
		}
	}
	return false
}

// rigidKey is the variable a function body sees for a signature variable:
// the signature keeps them flexible for callers, the body has them rigid
// (skolemSubs).
func rigidKey(tv hm.TypeVariable) hm.TypeVariable {
	if tv.IsRigid() {
		return tv
	}
	return hm.Rigid(tv)
}

// bindValueType records in subs what the type variables of declared stand
// for, given a value of it: `a!` and an Int is a := Int; `[a!]!` and a list
// is a := its element type. A variable stands for a nullable type (the a of
// `[a!]!` is Int for [Int!]!), so it is bound to the type without its `!`.
// Values that don't say (null, a list whose element type isn't known) bind
// nothing.
func bindValueType(subs hm.Subs, declared hm.Type, val Value) {
	inner, _ := stripNonNull(declared)
	switch d := inner.(type) {
	case hm.TypeVariable:
		bindVar(subs, d, valueRuntimeType(val))
	case hm.NullableTypeVariable:
		bindVar(subs, d.TypeVariable, valueRuntimeType(val))
	case ListType:
		lv, ok := val.(ListValue)
		if !ok {
			return
		}
		if lv.ElemType != nil && len(lv.ElemType.FreeTypeVar()) == 0 {
			bindTypeType(subs, d.Type, lv.ElemType)
			return
		}
		for _, elem := range lv.Elements {
			if _, isNull := elem.(NullValue); !isNull {
				bindValueType(subs, d.Type, elem)
				return
			}
		}
	case MapType:
		mv, ok := val.(MapValue)
		if !ok {
			return
		}
		if mv.ValType != nil && len(mv.ValType.FreeTypeVar()) == 0 {
			bindTypeType(subs, d.Type, mv.ValType)
			return
		}
		for _, k := range mv.Keys {
			if v := mv.Entries[k]; v != nil {
				if _, isNull := v.(NullValue); !isNull {
					bindValueType(subs, d.Type, v)
					return
				}
			}
		}
	}
}

// bindTypeType is bindValueType for a type of values.
func bindTypeType(subs hm.Subs, declared, actual hm.Type) {
	d, _ := stripNonNull(declared)
	a, _ := stripNonNull(actual)
	switch dt := d.(type) {
	case hm.TypeVariable:
		bindVar(subs, dt, a)
	case hm.NullableTypeVariable:
		bindVar(subs, dt.TypeVariable, a)
	case ListType:
		if at, ok := a.(ListType); ok {
			bindTypeType(subs, dt.Type, at.Type)
		}
	case MapType:
		if at, ok := a.(MapType); ok {
			bindTypeType(subs, dt.Type, at.Type)
		}
	}
}

func bindVar(subs hm.Subs, tv hm.TypeVariable, t hm.Type) {
	if t == nil || tv.IsWeak() || len(t.FreeTypeVar()) > 0 {
		return
	}
	key := rigidKey(tv)
	if _, done := subs[key]; done {
		return
	}
	t, _ = stripNonNull(t)
	subs[key] = t
	if !tv.IsRigid() {
		// The flexible form too: the body's return coercion targets the
		// caller-facing signature, which keeps the variable flexible.
		subs[tv] = t
	}
}

// valueRuntimeType is the type val has at runtime, when it says: nil for
// null, and for a list or map whose element type isn't known.
func valueRuntimeType(val Value) hm.Type {
	switch v := val.(type) {
	case nil, NullValue:
		return nil
	case ListValue:
		if v.ElemType == nil || len(v.ElemType.FreeTypeVar()) > 0 {
			return nil
		}
		return ListType{v.ElemType}
	case MapValue:
		if v.ValType == nil || len(v.ValType.FreeTypeVar()) > 0 {
			return nil
		}
		return MapType{v.ValType}
	}
	t := val.Type()
	if t == nil || len(t.FreeTypeVar()) > 0 {
		return nil
	}
	return t
}
