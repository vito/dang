package hm

import (
	"fmt"
	"strings"
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

// TypeVariable represents a type variable
type TypeVariable rune

func (tv TypeVariable) Name() string {
	return string(tv)
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
	return string(tv)
}

func (tv TypeVariable) Format(s fmt.State, c rune) {
	_, _ = fmt.Fprintf(s, "%s", string(tv))
}

// FunctionType represents a function type
type FunctionType struct {
	arg   Type
	ret   Type
	block *FunctionType // Optional block argument type (Ruby-style blocks)
}

func NewFnType(arg, ret Type) *FunctionType {
	return &FunctionType{arg: arg, ret: ret}
}

func (ft *FunctionType) Name() string {
	return ft.String()
}

func (ft *FunctionType) Apply(subs Subs) Substitutable {
	result := &FunctionType{
		arg: ft.arg.Apply(subs).(Type),
		ret: ft.ret.Apply(subs).(Type),
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
	result := &FunctionType{arg: arg, ret: ret}
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
	return Types{ft.arg, ft.ret}
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
	return fmt.Sprintf("(%s): %s",
		strings.TrimSuffix(strings.TrimPrefix(ft.arg.Name(), "{"), "}"),
		ft.ret.Name())
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

// NonNullType represents a non-nullable type wrapper
type NonNullType struct {
	Type Type
}

func (t NonNullType) Name() string {
	return fmt.Sprintf("%s!", t.Type.Name())
}

func (t NonNullType) Apply(subs Subs) Substitutable {
	applied := t.Type.Apply(subs).(Type)
	if applied == t.Type {
		return t
	}
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
	return fmt.Sprintf("%s!", t.Type)
}

func (t NonNullType) Format(s fmt.State, c rune) {
	_, _ = fmt.Fprintf(s, "%s!", t.Type)
}
