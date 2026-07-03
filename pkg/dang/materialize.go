package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
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
// Lists count when every element is itself literal; a Block counts when its
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
// value-handoff boundaries where wrapCoerce will materialize the result.
func assignableForValue(have, want hm.Type, value Node) (hm.Subs, error) {
	subs, err := hm.Assignable(have, want)
	if err == nil {
		return subs, nil
	}
	if isLiteralExpr(value) || containsDegradableScalar(have) {
		if coerceSubs, coerceErr := hm.AssignableWithCoercion(have, want); coerceErr == nil {
			return coerceSubs, nil
		}
	}
	if diag := diagnoseAssignment(have, want); diag != nil {
		return nil, withUnionProvenance(diag, have, want)
	}
	return nil, withUnionProvenance(err, have, want)
}

// isDegradableScalar reports whether mod is a custom scalar whose values may
// degrade to String at value-handoff boundaries. Every non-primitive scalar
// value is a string underneath (ScalarValue/RegexpValue), so they all qualify
// — except the codec namespaces (JSON/YAML/TOML): those are scalar-kind
// static modules whose bare name evaluates to the module object, not a
// string.
func isDegradableScalar(mod *Type) bool {
	if mod.Kind != ScalarKind || isPrimitiveScalar(mod) {
		return false
	}
	return !builtins.staticModuleSeen[mod]
}

// containsDegradableScalar reports whether t carries a degradable custom
// scalar at any depth reachable by matched-wrapper unification (NonNull,
// lists) — i.e. whether AssignableWithCoercion could apply the scalar→String
// degrade to a value of type t.
func containsDegradableScalar(t hm.Type) bool {
	switch v := t.(type) {
	case hm.NonNullType:
		return containsDegradableScalar(v.Type)
	case ListType:
		return containsDegradableScalar(v.Type)
	case GraphQLListType:
		return containsDegradableScalar(v.Type)
	case *Type:
		return isDegradableScalar(v)
	default:
		return false
	}
}

// diagnoseAssignment expands an hm.Assignable failure into a detailed
// message listing the field-level incompatibilities between two
// object/interface types. Returns nil when no enrichment applies and the
// caller should use the bare unification error.
func diagnoseAssignment(have, want hm.Type) error {
	haveMod, wantMod, issues, ok := walkAssignment(have, want)
	if !ok {
		return nil
	}
	if len(issues) == 0 {
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
	for name, wantScheme := range want.Bindings(PublicVisibility) {
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
// tightened to the new target.
func wrapCoerce(node Node, target hm.Type, path string) Node {
	if node == nil || target == nil {
		return node
	}
	if c, ok := node.(*Coerce); ok {
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
		return materializeDecoded(ctx, scope, v.Raw, target, path, v.Codec)
	case StringValue:
		return materializeStringValue(ctx, scope, v, target, path)
	case ScalarValue:
		// Degrade a custom scalar flowing into a String slot; any other
		// target passes through (the value is trusted to match its type).
		if unwrapNonNull(target) == StringType {
			return StringValue{Val: v.Val}, nil
		}
		return val, nil
	case RegexpValue:
		if unwrapNonNull(target) == StringType {
			return StringValue{Val: v.Source}, nil
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
		return ListValue{Elements: elements, ElemType: elemTarget}, nil
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
		if mod == RegexpType {
			re, err := compileRegexp(val.Val)
			if err != nil {
				return nil, materializeError(path, "%s", err.Error())
			}
			return RegexpValue{Re: re, Source: val.Val}, nil
		}
		if mod == PathType {
			return newPathValue(val.Val), nil
		}
		// A scalar declared with a new() hook computes its canonical string
		// at materialization (the Regexp-compiles-here precedent).
		if hook, argName, ok := mod.ScalarHook(); ok {
			v, err := runScalarHook(ctx, scope, mod, hook, argName, val.Val)
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

func materializeDecoded(ctx context.Context, scope ValueScope, raw any, target hm.Type, path string, codec Codec) (Value, error) {
	if nn, ok := target.(hm.NonNullType); ok {
		if raw == nil {
			return nil, materializeDeferredError(path, "null is not allowed for %s", target.Name())
		}
		return materializeDecoded(ctx, scope, raw, nn.Type, path, codec)
	}

	if raw == nil {
		return NullValue{}, nil
	}

	switch target.(type) {
	case hm.TypeVariable, hm.NullableTypeVariable:
		return DeferredValue{Raw: raw, Codec: codec}, nil
	}

	if union, ok := target.(*hm.UnionType); ok {
		return materializeDecodedUnion(ctx, scope, raw, union, path, codec)
	}

	if target == StringType {
		s, ok := raw.(string)
		if !ok {
			return nil, materializeDeferredError(path, "expected string, got %s", decodedKind(raw))
		}
		return StringValue{Val: s}, nil
	}

	if target == IntType {
		num, ok := raw.(json.Number)
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
		num, ok := raw.(json.Number)
		if !ok {
			return nil, materializeDeferredError(path, "expected number, got %s", decodedKind(raw))
		}
		val, err := num.Float64()
		if err != nil {
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
			materialized, err := materializeDecoded(ctx, scope, elem, elemTarget, joinIndexPath(path, i), codec)
			if err != nil {
				return nil, err
			}
			elements[i] = materialized
		}
		return ListValue{Elements: elements, ElemType: elemTarget}, nil
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
			if !ok {
				return nil, materializeDeferredError(path, "expected string for scalar %s, got %s", mod.Name(), decodedKind(raw))
			}
			if mod == StringType {
				return StringValue{Val: s}, nil
			}
			if mod == PathType {
				return newPathValue(s), nil
			}
			if hook, argName, ok := mod.ScalarHook(); ok {
				v, err := runScalarHook(ctx, scope, mod, hook, argName, s)
				if err != nil {
					return nil, materializeDeferredError(path, "%s", err.Error())
				}
				return v, nil
			}
			return ScalarValue{Val: s, ScalarType: mod}, nil
		case ObjectKind:
			if mod.Named == "" {
				return materializeAnonymousObject(ctx, scope, raw, mod, path, codec)
			}
			return materializeNamedObject(ctx, scope, raw, mod, path, codec)
		case InputKind:
			return materializeNamedObject(ctx, scope, raw, mod, path, codec)
		}
	}

	return nil, materializeDeferredError(path, "cannot materialize decoded data as %s", target.Name())
}

func materializeDecodedUnion(ctx context.Context, scope ValueScope, raw any, union *hm.UnionType, path string, codec Codec) (Value, error) {
	var lastErr error
	for _, option := range union.Options {
		val, err := materializeDecoded(ctx, scope, raw, option, path, codec)
		if err == nil {
			return val, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, materializeDeferredError(path, "cannot materialize decoded data as %s", union.Name())
	}
	return DeferredValue{Raw: raw, Codec: codec}, nil
}

func materializeNamedObject(ctx context.Context, scope ValueScope, raw any, mod *Type, path string, codec Codec) (Value, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, materializeDeferredError(path, "expected object for %s, got %s", mod.Name(), decodedKind(raw))
	}

	constructorVal, found, err := scope.Lookup(ctx, mod.Named)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, materializeDeferredError(path, "constructor for %s not found", mod.Name())
	}
	callable, ok := constructorVal.(Callable)
	if !ok {
		return nil, materializeDeferredError(path, "%s is not callable", mod.Name())
	}

	args, err := materializeConstructorArgs(ctx, scope, obj, mod, callable, path, codec)
	if err != nil {
		return nil, err
	}

	return callable.Call(ctx, scope, args)
}

func materializeConstructorArgs(ctx context.Context, scope ValueScope, obj map[string]any, mod *Type, callable Callable, path string, codec Codec) (map[string]Value, error) {
	args := map[string]Value{}

	if constructor, ok := callable.(*ConstructorFunction); ok {
		paramTypes := parameterTypes(callable)
		for _, param := range constructor.Parameters {
			name := param.Name.Name
			paramType := paramTypes[name]
			if paramType == nil {
				paramType = param.GetInferredType()
			}
			fieldPath := joinFieldPath(path, name)
			key, ignore := codec.fieldKey(mod, name)
			if !ignore {
				if rawVal, found := obj[key]; found {
					materialized, err := materializeDecoded(ctx, scope, rawVal, paramType, fieldPath, codec)
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
	for _, name := range callable.ParameterNames() {
		paramType := paramTypes[name]
		fieldPath := joinFieldPath(path, name)
		key, ignore := codec.fieldKey(mod, name)
		if !ignore {
			if rawVal, found := obj[key]; found {
				materialized, err := materializeDecoded(ctx, scope, rawVal, paramType, fieldPath, codec)
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

func materializeAnonymousObject(ctx context.Context, scope ValueScope, raw any, mod *Type, path string, codec Codec) (Value, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, materializeDeferredError(path, "expected object, got %s", decodedKind(raw))
	}

	value := NewObject(mod)
	for _, field := range mod.AsRecord().Fields {
		name := field.Key
		fieldType, mono := field.Value.Type()
		if !mono {
			return nil, materializeDeferredError(joinFieldPath(path, name), "field type is not monomorphic")
		}
		fieldPath := joinFieldPath(path, name)
		key, ignore := codec.fieldKey(mod, name)
		if !ignore {
			if rawVal, found := obj[key]; found {
				materialized, err := materializeDecoded(ctx, scope, rawVal, fieldType, fieldPath, codec)
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

func parameterTypes(val Value) map[string]hm.Type {
	result := map[string]hm.Type{}
	ft, ok := val.Type().(*hm.FunctionType)
	if !ok {
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

func decodedNumberToInt(num json.Number) (int, error) {
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
	case json.Number:
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
