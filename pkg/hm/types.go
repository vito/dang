package hm

import (
	"fmt"
)

// Type represents all possible type constructors with nullability support
type Type interface {
	Substitutable
	Name() string
	Normalize(TypeVarSet, TypeVarSet) (Type, error)
	Types() Types
	Eq(Type) bool
	fmt.Formatter
	fmt.Stringer
}

// Substitutable is any type that can have substitutions applied and knows its free type variables
type Substitutable interface {
	Apply(Subs) Substitutable
	FreeTypeVar() TypeVarSet
}

// QualifiedType represents a type with nullability qualification
type QualifiedType struct {
	Type    Type
	NonNull bool
}

func NewQualifiedType(t Type, nonNull bool) *QualifiedType {
	return &QualifiedType{Type: t, NonNull: nonNull}
}

func (qt *QualifiedType) Name() string {
	if qt.NonNull {
		return qt.Type.Name() + "!"
	}
	return qt.Type.Name()
}

func (qt *QualifiedType) Apply(subs Subs) Substitutable {
	return &QualifiedType{
		Type:    qt.Type.Apply(subs).(Type),
		NonNull: qt.NonNull,
	}
}

func (qt *QualifiedType) FreeTypeVar() TypeVarSet {
	return qt.Type.FreeTypeVar()
}

func (qt *QualifiedType) Normalize(k, v TypeVarSet) (Type, error) {
	normalized, err := qt.Type.Normalize(k, v)
	if err != nil {
		return nil, err
	}
	return &QualifiedType{Type: normalized, NonNull: qt.NonNull}, nil
}

func (qt *QualifiedType) Types() Types {
	return qt.Type.Types()
}

func (qt *QualifiedType) Eq(other Type) bool {
	if ot, ok := other.(*QualifiedType); ok {
		return qt.NonNull == ot.NonNull && qt.Type.Eq(ot.Type)
	}
	return false
}

func (qt *QualifiedType) String() string {
	if qt.NonNull {
		return qt.Type.String() + "!"
	}
	return qt.Type.String()
}

func (qt *QualifiedType) Format(s fmt.State, c rune) {
	fmt.Fprintf(s, "%s", qt.String())
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

func (tv TypeVariable) String() string {
	return string(tv)
}

func (tv TypeVariable) Format(s fmt.State, c rune) {
	fmt.Fprintf(s, "%s", string(tv))
}

// TypeConst represents a constant type
type TypeConst string

func (tc TypeConst) Name() string {
	return string(tc)
}

func (tc TypeConst) Apply(subs Subs) Substitutable {
	return tc
}

func (tc TypeConst) FreeTypeVar() TypeVarSet {
	return nil
}

func (tc TypeConst) Normalize(k, v TypeVarSet) (Type, error) {
	return tc, nil
}

func (tc TypeConst) Types() Types {
	return nil
}

func (tc TypeConst) Eq(other Type) bool {
	if ot, ok := other.(TypeConst); ok {
		return tc == ot
	}
	return false
}

func (tc TypeConst) String() string {
	return string(tc)
}

func (tc TypeConst) Format(s fmt.State, c rune) {
	fmt.Fprintf(s, "%s", string(tc))
}

// FunctionType represents a function type
type FunctionType struct {
	arg Type
	ret Type
}

func NewFnType(arg, ret Type) *FunctionType {
	return &FunctionType{arg: arg, ret: ret}
}

func (ft *FunctionType) Name() string {
	return fmt.Sprintf("(%s -> %s)", ft.arg.Name(), ft.ret.Name())
}

func (ft *FunctionType) Apply(subs Subs) Substitutable {
	return &FunctionType{
		arg: ft.arg.Apply(subs).(Type),
		ret: ft.ret.Apply(subs).(Type),
	}
}

func (ft *FunctionType) FreeTypeVar() TypeVarSet {
	return ft.arg.FreeTypeVar().Union(ft.ret.FreeTypeVar())
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
	return &FunctionType{arg: arg, ret: ret}, nil
}

func (ft *FunctionType) Types() Types {
	return Types{ft.arg, ft.ret}
}

func (ft *FunctionType) Eq(other Type) bool {
	if ot, ok := other.(*FunctionType); ok {
		return ft.arg.Eq(ot.arg) && ft.ret.Eq(ot.ret)
	}
	return false
}

func (ft *FunctionType) String() string {
	return fmt.Sprintf("(%s -> %s)", ft.arg.String(), ft.ret.String())
}

func (ft *FunctionType) Format(s fmt.State, c rune) {
	fmt.Fprintf(s, "%s", ft.String())
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

// Types represents a slice of types
type Types []Type

// Common type constants
var (
	IntType    = TypeConst("Int")
	BoolType   = TypeConst("Bool")
	StringType = TypeConst("String")
	UnitType   = TypeConst("()")
)

// BorrowTypes creates a new slice of types with the given capacity
// This is for compatibility with object pooling patterns
func BorrowTypes(capacity int) Types {
	return make(Types, capacity)
}