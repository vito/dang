package hm

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

// Type represents all possible type constructors with nullability support
type Type interface {
	Substitutable
	Name() string
	Normalize(TypeVarSet, TypeVarSet) (Type, error)
	Types() Types
	Eq(Type) bool
	// Supertypes returns the direct supertypes of this type.
	// For example, NonNullType{String} returns [String].
	// Module types implementing interfaces return those interfaces.
	// Most types return nil (no supertypes).
	Supertypes() []Type
	// fmt.Formatter
	fmt.Stringer
}

// Coercible is implemented by types that accept coercion from other types.
// This is used primarily for custom scalar types that can accept values
// of built-in types (e.g., a URL scalar accepting a String literal).
type Coercible interface {
	AcceptsCoercionFrom(Type) bool
}

// Substitutable is any type that can have substitutions applied and knows its free type variables
type Substitutable interface {
	Apply(Subs) Substitutable
	FreeTypeVar() TypeVarSet
}

// TypeVariable represents a type variable. Its low bits are its name, as
// written (the `a` of `id(x: a): a`) or numbered by a fresher (see FreshVar);
// its high bits are an id that tells apart variables sharing a name. An
// author-written variable has none, while every variable a fresher mints, and
// every copy Freshen makes, gets a new one, unique in the process: so no two
// fresh variables are ever the same, however many freshers minted them, and
// none is ever an author's.
type TypeVariable int64

const (
	nameBits = 24
	nameMask = 1<<nameBits - 1

	// rigidBit marks rigid (skolem) type variables, keeping them apart from
	// every flexible variable. A rigid variable stands for an author-written,
	// universally-quantified type parameter (e.g. the `b` in `do(&yield: b):
	// b`). Unlike a flexible variable, it must NOT unify with a concrete type
	// while checking the body it was declared in — that is what makes `yield
	// * 2` a definition-time error rather than a runtime surprise.
	rigidBit = 1 << 62
)

// lastID is the id of the last fresh variable minted.
var lastID atomic.Int64

// Freshen returns a new flexible variable named like tv.
func Freshen(tv TypeVariable) TypeVariable {
	return tv&nameMask | TypeVariable(lastID.Add(1)<<nameBits)
}

// Rigid returns the rigid (skolem) counterpart of a flexible type variable.
func Rigid(tv TypeVariable) TypeVariable { return tv | rigidBit }

// EqIgnoringIDs reports whether a and b are equal up to a consistent renaming
// of their flexible type variables: `{x: [b]!}` matches `{x: [a]!}`, but
// `{x: [a]!, y: [a]!}` does not match `{x: [a]!, y: [b]!}`. It is for
// comparing shapes that separate inferences produced, each with variables of
// its own, such as two `{{xs: []}}`s, or a `{{x: []}}` and a parameter
// written `{{x: [a]!}}`. Rigid variables are compared as they are.
func EqIgnoringIDs(a, b Type) bool {
	if a.Eq(b) {
		return true
	}
	return canonical(a).Eq(canonical(b))
}

// canonicalBase offsets the names canonical gives variables past any written
// letter; with id 0, none of them is a fresh variable either. Only canonical
// types, compared and dropped, ever hold them.
const canonicalBase = 0x800000

// canonical renames t's flexible variables after the order they first occur
// in it, walking its components in order (record fields by key).
func canonical(t Type) Type {
	var subs Subs
	var walk func(Type)
	visit := func(tv TypeVariable) {
		if tv.IsRigid() {
			return
		}
		if _, seen := subs[tv]; seen {
			return
		}
		if subs == nil {
			subs = NewSubs()
		}
		subs.Add(tv, TypeVariable(canonicalBase+len(subs)))
	}
	walk = func(t Type) {
		switch v := t.(type) {
		case TypeVariable:
			visit(v)
		case NullableTypeVariable:
			visit(v.TypeVariable)
		case *UnionType:
			// A union lists no components in Types.
			for _, o := range v.Options {
				walk(o)
			}
		default:
			for _, c := range t.Types() {
				walk(c)
			}
		}
	}
	walk(t)
	if len(subs) == 0 {
		return t
	}
	return t.Apply(subs).(Type)
}

// IsRigid reports whether tv is a rigid (skolem) type variable.
func (tv TypeVariable) IsRigid() bool {
	return tv&rigidBit != 0
}

// freshBase offsets the names of the fresh variables minted past 'z' beyond
// Unicode, where they can repeat neither a letter nor each other.
const freshBase = 0x110000

// FreshVar returns a new variable named after the nth fresh one: 'a'..'z'
// first, then a1..z1, a2..z2 and so on.
func FreshVar(n int) TypeVariable {
	name := TypeVariable(freshBase + n)
	if n < len(letters) {
		name = TypeVariable(letters[n])
	}
	return Freshen(name & nameMask)
}

const letters = "abcdefghijklmnopqrstuvwxyz"

// name renders the variable as it was written or numbered (see FreshVar).
func (tv TypeVariable) name() string {
	n := int(tv & nameMask)
	if n >= freshBase {
		n -= freshBase
		return string(letters[n%len(letters)]) + strconv.Itoa(n/len(letters))
	}
	return string(rune(n))
}

func (tv TypeVariable) Name() string {
	return tv.name()
}

func (tv TypeVariable) Apply(subs Subs) Substitutable {
	if t, exists := subs[tv]; exists {
		return t
	}
	return tv
}

func (tv TypeVariable) FreeTypeVar() TypeVarSet {
	return NewTypeVarSet(tv)
}

func (tv TypeVariable) Normalize(k, v TypeVarSet) (Type, error) {
	return tv, nil
}

func (tv TypeVariable) Types() Types {
	return nil
}

func (tv TypeVariable) Eq(other Type) bool {
	if ot, ok := other.(TypeVariable); ok {
		return tv == ot
	}
	return false
}

func (tv TypeVariable) Supertypes() []Type {
	return nil
}

func (tv TypeVariable) String() string {
	return tv.name()
}

func (tv TypeVariable) Format(s fmt.State, c rune) {
	_, _ = fmt.Fprintf(s, "%s", tv.name())
}

// NullableTypeVariable is a type variable that carries a nullability taint.
// It is produced by null literals. When unified with a NonNullType during
// binding, it strips the NonNull wrapper, ensuring that null always resolves
// to a nullable type.
type NullableTypeVariable struct {
	TypeVariable
}

func (ntv NullableTypeVariable) Name() string {
	return ntv.String()
}

func (ntv NullableTypeVariable) Apply(subs Subs) Substitutable {
	// Look up by the underlying TypeVariable key. Applying a substitution must
	// preserve the nullable taint: a? with a := T! resolves to T, and a? with
	// a := b resolves to b?.
	if t, exists := subs[ntv.TypeVariable]; exists {
		return makeNullable(t)
	}
	return ntv
}

func makeNullable(t Type) Type {
	switch tt := t.(type) {
	case NullableTypeVariable:
		return tt
	case NonNullType:
		return tt.Type
	case TypeVariable:
		return NullableTypeVariable{TypeVariable: tt}
	default:
		return t
	}
}

func (ntv NullableTypeVariable) Eq(other Type) bool {
	if ot, ok := other.(NullableTypeVariable); ok {
		return ntv.TypeVariable == ot.TypeVariable
	}
	return false
}

func (ntv NullableTypeVariable) String() string {
	return ntv.TypeVariable.name() + "?"
}

func (ntv NullableTypeVariable) Format(s fmt.State, c rune) {
	_, _ = fmt.Fprintf(s, "%s?", ntv.TypeVariable.name())
}

// Effect is an opaque annotation on a function type describing what calling
// the function may do besides producing its result (e.g. which shared state it
// may write). hm never interprets it: substitution, normalization and
// unification carry it along untouched, and Eq ignores it. The host language
// owns its meaning and solves effect constraints itself, after inference,
// because effects of hoisted declarations are not known yet when their call
// sites are unified.
type Effect interface {
	EffectName() string
}

// FunctionType represents a function type
type FunctionType struct {
	arg    Type
	ret    Type
	block  *FunctionType // Optional block argument type (Ruby-style blocks)
	effect Effect        // Optional latent effect; nil means "unknown to hm"
}

func NewFnType(arg, ret Type) *FunctionType {
	return &FunctionType{arg: arg, ret: ret}
}

// Effect returns the function's latent effect annotation, if any.
func (ft *FunctionType) Effect() Effect {
	return ft.effect
}

// SetEffect sets the function's latent effect annotation in place. Only use
// it on a function type you just built; types are otherwise shared.
func (ft *FunctionType) SetEffect(e Effect) {
	ft.effect = e
}

// WithEffect returns a shallow copy of the function type carrying e.
func (ft *FunctionType) WithEffect(e Effect) *FunctionType {
	cp := *ft
	cp.effect = e
	return &cp
}

func (ft *FunctionType) Name() string {
	return ft.String()
}

func (ft *FunctionType) Apply(subs Subs) Substitutable {
	result := &FunctionType{
		arg:    ft.arg.Apply(subs).(Type),
		ret:    ft.ret.Apply(subs).(Type),
		effect: ft.effect,
	}
	if ft.block != nil {
		result.block = ft.block.Apply(subs).(*FunctionType)
	}
	return result
}

func (ft *FunctionType) FreeTypeVar() TypeVarSet {
	result := ft.arg.FreeTypeVar().Union(ft.ret.FreeTypeVar())
	if ft.block != nil {
		result = result.Union(ft.block.FreeTypeVar())
	}
	return result
}

func (ft *FunctionType) Normalize(k, v TypeVarSet) (Type, error) {
	arg, err := ft.arg.Normalize(k, v)
	if err != nil {
		return nil, err
	}
	ret, err := ft.ret.Normalize(k, v)
	if err != nil {
		return nil, err
	}
	result := &FunctionType{arg: arg, ret: ret, effect: ft.effect}
	if ft.block != nil {
		block, err := ft.block.Normalize(k, v)
		if err != nil {
			return nil, err
		}
		result.block = block.(*FunctionType)
	}
	return result, nil
}

func (ft *FunctionType) Types() Types {
	types := Types{ft.arg, ft.ret}
	if ft.block != nil {
		types = append(types, ft.block)
	}
	return types
}

func (ft *FunctionType) Eq(other Type) bool {
	if ot, ok := other.(*FunctionType); ok {
		argsEq := ft.arg.Eq(ot.arg)
		retsEq := ft.ret.Eq(ot.ret)
		blocksEq := (ft.block == nil && ot.block == nil) ||
			(ft.block != nil && ot.block != nil && ft.block.Eq(ot.block))
		return argsEq && retsEq && blocksEq
	}
	return false
}

func (ft *FunctionType) Supertypes() []Type {
	return nil
}

func (ft *FunctionType) String() string {
	args := strings.TrimSuffix(strings.TrimPrefix(ft.arg.String(), "{"), "}")
	if ft.block != nil {
		if args != "" {
			args += ", "
		}
		args += "&block: " + ft.block.String()
	}
	return fmt.Sprintf("(%s): %s", args, ft.ret.String())
}

func (ft *FunctionType) Format(s fmt.State, c rune) {
	_, _ = fmt.Fprintf(s, "%s", ft.String())
}

// Arg returns the argument type
func (ft *FunctionType) Arg() Type {
	return ft.arg
}

// Ret returns the return type, with optional recursive parameter for compatibility
func (ft *FunctionType) Ret(recursive bool) Type {
	// For now, ignore the recursive parameter since we're not implementing full HM features
	return ft.ret
}

// Convenience method for getting return type
func (ft *FunctionType) ReturnType() Type {
	return ft.ret
}

// Block returns the optional block argument type
func (ft *FunctionType) Block() *FunctionType {
	return ft.block
}

// SetBlock sets the block argument type
func (ft *FunctionType) SetBlock(block *FunctionType) {
	ft.block = block
}

// Types represents a slice of types
type Types []Type

// BorrowTypes creates a new slice of types with the given capacity
// This is for compatibility with object pooling patterns
func BorrowTypes(capacity int) Types {
	return make(Types, capacity)
}

// UnionType represents an inline "one of" type. A value is assignable to a
// UnionType if it is assignable to any of the options.
type UnionType struct {
	Options []Type

	// sources carries an optional opaque per-option annotation, index-aligned
	// with Options — provenance recorded by whoever widened arms into this
	// union, consumed by diagnostics. It is deliberately ignored by Eq (two
	// unions with the same options are the same type wherever their members
	// came from) and by String. Attach at construction only: union values are
	// shared, never cloned, so post-hoc mutation would leak provenance across
	// unrelated uses.
	sources []any
}

// NewUnionType creates a flattened, de-duplicated inline union type. If only
// one distinct option remains, that option is returned directly.
func NewUnionType(options ...Type) Type {
	return NewUnionTypeWithSources(options, nil)
}

// NewUnionTypeWithSources is NewUnionType with an opaque provenance
// annotation per option (sources may be shorter than options; missing or
// nil entries mean "unknown"). Flattening a nested union keeps its inner
// per-option sources, falling back to the outer annotation; de-duplication
// keeps the first non-nil source for a repeated option.
func NewUnionTypeWithSources(options []Type, sources []any) Type {
	flattened := make([]Type, 0, len(options))
	flatSources := make([]any, 0, len(options))
	for i, option := range options {
		var src any
		if i < len(sources) {
			src = sources[i]
		}
		if union, ok := option.(*UnionType); ok {
			for j, inner := range union.Options {
				flattened = append(flattened, inner)
				flatSources = append(flatSources, union.optionSourceOr(j, src))
			}
			continue
		}
		flattened = append(flattened, option)
		flatSources = append(flatSources, src)
	}

	deduped := make([]Type, 0, len(flattened))
	dedupedSources := make([]any, 0, len(flattened))
	for i, option := range flattened {
		duplicate := false
		for j, existing := range deduped {
			if option.Eq(existing) {
				duplicate = true
				if dedupedSources[j] == nil {
					dedupedSources[j] = flatSources[i]
				}
				break
			}
		}
		if !duplicate {
			deduped = append(deduped, option)
			dedupedSources = append(dedupedSources, flatSources[i])
		}
	}

	if len(deduped) == 1 {
		return deduped[0]
	}
	return &UnionType{Options: deduped, sources: dedupedSources}
}

// OptionSource returns the opaque provenance recorded for option i, or nil.
func (t *UnionType) OptionSource(i int) any {
	if i < len(t.sources) {
		return t.sources[i]
	}
	return nil
}

func (t *UnionType) optionSourceOr(i int, fallback any) any {
	if src := t.OptionSource(i); src != nil {
		return src
	}
	return fallback
}

func (t *UnionType) Name() string {
	parts := make([]string, len(t.Options))
	for i, option := range t.Options {
		parts[i] = option.String()
	}
	return strings.Join(parts, " | ")
}

func (t *UnionType) Apply(subs Subs) Substitutable {
	options := make([]Type, len(t.Options))
	for i, option := range t.Options {
		options[i] = option.Apply(subs).(Type)
	}
	// Re-thread sources: the rebuild would otherwise silently drop them
	// on every substitution.
	return NewUnionTypeWithSources(options, t.sources)
}

func (t *UnionType) FreeTypeVar() TypeVarSet {
	var result TypeVarSet
	for _, option := range t.Options {
		result = result.Union(option.FreeTypeVar())
	}
	return result
}

func (t *UnionType) Normalize(k, v TypeVarSet) (Type, error) {
	options := make([]Type, len(t.Options))
	for i, option := range t.Options {
		normalized, err := option.Normalize(k, v)
		if err != nil {
			return nil, err
		}
		options[i] = normalized
	}
	return NewUnionTypeWithSources(options, t.sources), nil
}

func (t *UnionType) Types() Types {
	return nil
}

func (t *UnionType) Eq(other Type) bool {
	otherUnion, ok := other.(*UnionType)
	if !ok || len(t.Options) != len(otherUnion.Options) {
		return false
	}
	for _, option := range t.Options {
		found := false
		for _, otherOption := range otherUnion.Options {
			if option.Eq(otherOption) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (t *UnionType) Supertypes() []Type {
	return nil
}

func (t *UnionType) String() string {
	parts := make([]string, len(t.Options))
	for i, option := range t.Options {
		parts[i] = option.String()
	}
	return strings.Join(parts, " | ")
}

// NonNullType represents a non-nullable type wrapper
type NonNullType struct {
	Type Type
}

func (t NonNullType) Name() string {
	if _, ok := t.Type.(*UnionType); ok {
		return fmt.Sprintf("(%s)!", t.Type)
	}
	return fmt.Sprintf("%s!", t.Type)
}

func (t NonNullType) Apply(subs Subs) Substitutable {
	applied := t.Type.Apply(subs).(Type)
	return NonNullType{applied}
}

func (t NonNullType) FreeTypeVar() TypeVarSet {
	return t.Type.FreeTypeVar()
}

func (t NonNullType) Normalize(k, v TypeVarSet) (Type, error) {
	normalized, err := t.Type.Normalize(k, v)
	if err != nil {
		return nil, err
	}
	return NonNullType{normalized}, nil
}

func (t NonNullType) Types() Types {
	// Return the inner type as a single-element component list
	// This allows NonNullType to be treated as a composite type during unification
	// So NonNull(Int) can unify with NonNull(a) by unifying Int with a
	return Types{t.Type}
}

func (t NonNullType) Eq(other Type) bool {
	if ot, ok := other.(NonNullType); ok {
		return t.Type.Eq(ot.Type)
	}
	return false
}

func (t NonNullType) Supertypes() []Type {
	ts := []Type{
		// NonNull T is a subtype of T, so T is a supertype
		t.Type,
	}
	innerSupers := t.Type.Supertypes()
	for _, i := range innerSupers {
		// Generalize into non-null form of each supertype
		ts = append(ts, NonNullType{i})
	}
	return ts
}

func (t NonNullType) String() string {
	if _, ok := t.Type.(*UnionType); ok {
		return fmt.Sprintf("(%s)!", t.Type)
	}
	return fmt.Sprintf("%s!", t.Type)
}

func (t NonNullType) Format(s fmt.State, c rune) {
	_, _ = fmt.Fprint(s, t.String())
}
